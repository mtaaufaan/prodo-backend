package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/cache"
)

// workspaceMemberRepository -- interface didefinisikan di consumer, §3.9.
type workspaceMemberRepository interface {
	GetRole(ctx context.Context, workspaceID, userID string) (string, error)
	AssignRole(ctx context.Context, workspaceID, userID, role string, invitedBy *string, actorID, actorRole string, before, after map[string]string, notifTitle, notifBody string) error
}

// RBACService menangani assignment role per-workspace (S2-03/05/06, US-002).
type RBACService struct {
	repo  workspaceMemberRepository
	cache cache.Cache
}

func NewRBACService(repo workspaceMemberRepository, c cache.Cache) *RBACService {
	return &RBACService{repo: repo, cache: c}
}

// roleCacheKey -- dibaca RequireRole middleware (S2-09) untuk hindari query
// DB di setiap request, sama pola dengan session:revoked:<jti> (S1-32).
func roleCacheKey(userID, workspaceID string) string {
	return "role:" + userID + ":" + workspaceID
}

// RoleChangeResult -- hasil AssignRole.
type RoleChangeResult struct {
	PreviousRole string
	NewRole      string
}

// AssignRole menetapkan role user di workspace -- INSERT kalau belum
// member, UPDATE kalau sudah (S2-03), sekaligus mencatat audit trail
// (S2-06) dan mengirim in-app notification ke target (S2-05) dalam SATU
// transaksi DB (lihat WorkspaceMemberRepository.AssignRole -- awalnya
// tiga query terpisah, gagal di tengah jalan meninggalkan state
// setengah-jadi, ketahuan saat verifikasi live). Cache Redis
// role:<user>:<workspace> di-invalidate SETELAH transaksi commit
// (cache bukan bagian transaksi DB, tapi harus nunggu commit sukses
// dulu baru boleh dianggap "role lama sudah tidak valid").
// actorID/actorRole adalah user yang MELAKUKAN perubahan, beda dari
// userID (target yang role-nya berubah).
func (s *RBACService) AssignRole(ctx context.Context, workspaceID, userID, role string, invitedBy *string, actorID, actorRole string) (*RoleChangeResult, error) {
	previousRole, err := s.repo.GetRole(ctx, workspaceID, userID)
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

	if err := s.repo.AssignRole(ctx, workspaceID, userID, role, invitedBy, actorID, actorRole, before, after, title, body); err != nil {
		return nil, fmt.Errorf("service.AssignRole: %w", err)
	}

	if err := s.cache.Del(ctx, roleCacheKey(userID, workspaceID)); err != nil {
		return nil, fmt.Errorf("service.AssignRole: invalidate cache: %w", err)
	}

	return &RoleChangeResult{PreviousRole: previousRole, NewRole: role}, nil
}

// GetMemberRole mengembalikan role user di workspace, atau "" kalau user
// bukan member workspace tersebut (dipakai handler S2-04 untuk cek
// otorisasi "AW only" -- lihat komentar WorkspaceHandler.UpdateMemberRole
// soal gap scoping Group Admin).
func (s *RBACService) GetMemberRole(ctx context.Context, workspaceID, userID string) (string, error) {
	role, err := s.repo.GetRole(ctx, workspaceID, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("service.GetMemberRole: %w", err)
	}
	return role, nil
}
