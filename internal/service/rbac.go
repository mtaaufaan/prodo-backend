package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/cache"
	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// workspaceMemberRepository -- interface didefinisikan di consumer, §3.9.
// exec (db.Executor, S2-10/11) adalah transaksi request-scoped yang sudah
// membawa session variable RLS -- lihat WorkspaceMemberRepository.
type workspaceMemberRepository interface {
	GetRole(ctx context.Context, exec db.Executor, workspaceID, userID string) (string, error)
	AssignRole(ctx context.Context, exec db.Executor, workspaceID, userID, role string, invitedBy *string, actorID, actorRole string, before, after map[string]string, notifTitle, notifBody string) error
	ListMembers(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.Member, error)
	ListOrgCandidates(ctx context.Context, exec db.Executor, orgID string) ([]repository.Member, error)
	GetWorkspaceOrgID(ctx context.Context, exec db.Executor, workspaceID string) (string, error)
	RemoveMember(ctx context.Context, exec db.Executor, workspaceID, userID, actorID, actorRole string) error
}

// RBACService menangani assignment role per-workspace (S2-03/05/06, US-002).
type RBACService struct {
	repo  workspaceMemberRepository
	cache cache.Cache
}

func NewRBACService(repo workspaceMemberRepository, c cache.Cache) *RBACService {
	return &RBACService{repo: repo, cache: c}
}

// roleCacheKey -- dibaca RequireRole middleware (S2-09) lewat GetMemberRole
// untuk hindari query DB di setiap request, sama pola dengan
// session:revoked:<jti> (S1-32).
func roleCacheKey(userID, workspaceID string) string {
	return "role:" + userID + ":" + workspaceID
}

// roleCacheTTL -- jaring pengaman kalau invalidate di AssignRole terlewat
// (mis. proses crash tepat setelah commit, sebelum Del terpanggil); role
// jarang berubah jadi TTL longgar tidak masalah -- perubahan role NORMAL
// tetap langsung ter-invalidate lewat AssignRole, TTL ini bukan jalur utama.
const roleCacheTTL = 5 * time.Minute

// RoleChangeResult -- hasil AssignRole.
type RoleChangeResult struct {
	PreviousRole string
	NewRole      string
}

// AssignRole menetapkan role user di workspace -- INSERT kalau belum
// member, UPDATE kalau sudah (S2-03), sekaligus mencatat audit trail
// (S2-06) dan mengirim in-app notification ke target (S2-05) dalam SATU
// transaksi DB (exec, transaksi request-scoped dari
// middleware.DBContextMiddleware, S2-10/11 -- awalnya repo yang buka
// transaksi lokal sendiri, gagal di tengah jalan meninggalkan state
// setengah-jadi, ketahuan saat verifikasi live; sekarang dijamin transaksi
// luar yang sama dipakai RequireRole untuk cek otorisasi). Cache Redis
// role:<user>:<workspace> di-invalidate di sini, SEBELUM transaksi luar
// commit (middleware yang commit, setelah handler selesai) -- kalau
// commit itu gagal (jarang, mis. koneksi putus), cache cuma jadi miss
// sesaat dan self-heal ke nilai lama yang benar di GetMemberRole
// berikutnya, bukan korupsi data.
// actorID/actorRole adalah user yang MELAKUKAN perubahan, beda dari
// userID (target yang role-nya berubah).
func (s *RBACService) AssignRole(ctx context.Context, exec db.Executor, workspaceID, userID, role string, invitedBy *string, actorID, actorRole string) (*RoleChangeResult, error) {
	previousRole, err := s.repo.GetRole(ctx, exec, workspaceID, userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("service.AssignRole: cek role lama: %w", err)
	}

	var before map[string]string
	if previousRole != "" {
		before = map[string]string{"role": previousRole}
	}
	after := map[string]string{"role": role}
	title := "Role Anda diperbarui"
	body := fmt.Sprintf("Role Anda di workspace ini diubah menjadi %s.", role)

	if err := s.repo.AssignRole(ctx, exec, workspaceID, userID, role, invitedBy, actorID, actorRole, before, after, title, body); err != nil {
		return nil, fmt.Errorf("service.AssignRole: %w", err)
	}

	if err := s.cache.Del(ctx, roleCacheKey(userID, workspaceID)); err != nil {
		return nil, fmt.Errorf("service.AssignRole: invalidate cache: %w", err)
	}

	return &RoleChangeResult{PreviousRole: previousRole, NewRole: role}, nil
}

// GetMemberRole mengembalikan role user di workspace, atau "" kalau user
// bukan member workspace tersebut. Dipakai untuk cek otorisasi (handler
// S2-04, middleware RequireRole S2-09) -- lihat komentar
// WorkspaceHandler.UpdateMemberRole soal gap scoping Group Admin. Cache
// Redis dulu (read-through, di-populate di sini kalau miss), baru DB kalau
// cache kosong. Hasil "bukan member" (role "") SENGAJA tidak di-cache --
// kasus itu jarang di jalur hot-path dibanding member sungguhan, dan
// menghindari perlu invalidate cache negatif saat user baru diundang.
func (s *RBACService) GetMemberRole(ctx context.Context, exec db.Executor, workspaceID, userID string) (string, error) {
	key := roleCacheKey(userID, workspaceID)
	if cached, err := s.cache.Get(ctx, key); err == nil {
		return cached, nil
	} else if !errors.Is(err, cache.ErrNotFound) {
		return "", fmt.Errorf("service.GetMemberRole: cek cache: %w", err)
	}

	role, err := s.repo.GetRole(ctx, exec, workspaceID, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("service.GetMemberRole: %w", err)
	}

	if err := s.cache.Set(ctx, key, role, roleCacheTTL); err != nil {
		return "", fmt.Errorf("service.GetMemberRole: set cache: %w", err)
	}
	return role, nil
}

// GetWorkspaceOrgID -- pass-through tipis ke repo (S3-41), dipakai
// middleware.RequireRole untuk scoping Group Admin.
func (s *RBACService) GetWorkspaceOrgID(ctx context.Context, exec db.Executor, workspaceID string) (string, error) {
	orgID, err := s.repo.GetWorkspaceOrgID(ctx, exec, workspaceID)
	if err != nil {
		return "", fmt.Errorf("service.GetWorkspaceOrgID: %w", err)
	}
	return orgID, nil
}

// ListMembers mengembalikan seluruh member workspace (S2-07/08 prasyarat --
// lihat komentar WorkspaceMemberRepository.ListMembers soal scope
// project_scoped_members yang belum dibangun).
func (s *RBACService) ListMembers(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.Member, error) {
	members, err := s.repo.ListMembers(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.ListMembers: %w", err)
	}
	return members, nil
}

// ListOrgCandidates -- pass-through tipis ke repo (S4G-05, Track S4G),
// dipakai WorkspaceService.ListCandidateAdmins.
func (s *RBACService) ListOrgCandidates(ctx context.Context, exec db.Executor, orgID string) ([]repository.Member, error) {
	members, err := s.repo.ListOrgCandidates(ctx, exec, orgID)
	if err != nil {
		return nil, fmt.Errorf("service.ListOrgCandidates: %w", err)
	}
	return members, nil
}

// RemoveMember mengeluarkan member dari workspace (S3-15). Cache
// role:<user>:<workspace> WAJIB di-invalidate sama seperti AssignRole --
// tanpa ini, request selanjutnya dari user yang baru dikeluarkan tetap
// lolos RequireRole (S2-09) selama sisa TTL walau baris workspace_members-
// nya sudah tidak ada.
func (s *RBACService) RemoveMember(ctx context.Context, exec db.Executor, workspaceID, userID, actorID, actorRole string) error {
	if err := s.repo.RemoveMember(ctx, exec, workspaceID, userID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.RemoveMember: %w", err)
	}
	if err := s.cache.Del(ctx, roleCacheKey(userID, workspaceID)); err != nil {
		return fmt.Errorf("service.RemoveMember: invalidate cache: %w", err)
	}
	return nil
}
