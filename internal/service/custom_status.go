// Package service -- CustomStatusService (Task Management Core Phase 1;
// require_start_confirmation toggle Phase 4, US-018b; CRUD template
// workspace S4W-05, US-020/021; CRUD project-scope Track S5B, US-019).
package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// customStatusMaxPerScope -- batas AW/PM Add Status.dc.html ("Batas 12
// status per workspace/project tercapai") -- sama angka, berlaku independen
// per scope (project baru mulai dari salinan workspace-nya, punya jatah 12
// sendiri, tidak berbagi kuota dengan template workspace asal).
const customStatusMaxPerScope = 12

// customStatusUntracked -- 4 dari 6 status sistem yang bukan status kerja
// aktif (task di situ menunggu keputusan, bukan sedang dikerjakan) --
// konfirmasi "Mulai Pengerjaan" tidak berlaku, sama persis konstanta
// UNTRACKED di AW Custom Status.dc.html.
var customStatusUntracked = map[string]bool{"BACKLOG": true, "DONE": true, "BLOCKED": true, "CANCELED": true}

// customStatusColorTokens -- 7 token warna resmi Tailwind (tailwind.config.ts,
// docs/design.md §2) yang dipakai color picker Add/Kelola Status.
var customStatusColorTokens = map[string]bool{
	"grey": true, "signal": true, "violet": true, "amber": true, "red": true, "blue": true, "mint": true,
}

type customStatusRepository interface {
	ListForScope(ctx context.Context, exec db.Executor, scopeType, scopeID string) ([]repository.CustomStatus, error)
	Get(ctx context.Context, exec db.Executor, statusID string) (*repository.CustomStatus, error)
	NameExists(ctx context.Context, exec db.Executor, scopeType, scopeID, name, excludeID string) (bool, error)
	Create(ctx context.Context, exec db.Executor, scopeType, scopeID, name, colorToken string, position int, actorID, actorRole, workspaceID string) (*repository.CustomStatus, error)
	UpdateNameColor(ctx context.Context, exec db.Executor, statusID, name, colorToken, actorID, actorRole, workspaceID string) error
	Move(ctx context.Context, exec db.Executor, scopeType, scopeID, statusID string, direction int, actorID, actorRole, workspaceID string) error
	SetUndefined(ctx context.Context, exec db.Executor, statusID string, undefined bool, actorID, actorRole, workspaceID string) error
	SetRequireStartConfirmation(ctx context.Context, exec db.Executor, statusID string, require bool, actorID, actorRole, workspaceID string) error
	SetRequirePic(ctx context.Context, exec db.Executor, statusID string, require bool, actorID, actorRole, workspaceID string) error
}

// customStatusProjectResolver -- reuse ProjectRepository.GetWorkspaceID
// (workspace pemilik project ini, dipakai resolveWorkspaceID+otorisasi
// admin_workspace) dan .IsPM (dipakai otorisasi PM-of-project, Track S5B).
type customStatusProjectResolver interface {
	GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error)
	IsPM(ctx context.Context, exec db.Executor, projectID, userID string) (bool, error)
}

// customStatusRuleDeactivator -- reuse RuleService.DeactivateForStatus
// (S4W-10, US-053), dipanggil Undefine SETELAH status berhasil di-undefine.
// nil diterima (rules belum ada saat CustomStatusService pertama dibangun
// S4W-05, H11 belum selesai) -- Undefine skip pemanggilan kalau nil, sama
// pola projectWebhookDispatcher yang boleh nil di ProjectService.
type customStatusRuleDeactivator interface {
	DeactivateForStatus(ctx context.Context, exec db.Executor, statusID, statusName, actorID, actorRole string) error
}

type CustomStatusService struct {
	repo     customStatusRepository
	rbac     sprintWorkspaceRoleChecker
	rules    customStatusRuleDeactivator
	projects customStatusProjectResolver
}

func NewCustomStatusService(repo customStatusRepository, rbac sprintWorkspaceRoleChecker, rules customStatusRuleDeactivator, projects customStatusProjectResolver) *CustomStatusService {
	return &CustomStatusService{repo: repo, rbac: rbac, rules: rules, projects: projects}
}

func (s *CustomStatusService) ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.CustomStatus, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.ListForWorkspace: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.ListForScope(ctx, exec, "workspace", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.ListForWorkspace: %w", err)
	}
	return list, nil
}

// ListForProject (Track S5B, US-019, "PM Custom Status.dc.html") -- daftar
// status project ini (salinan independen, lihat komentar package
// repository). Tidak ada gerbang otorisasi tambahan di sini -- sama pola
// ListForWorkspace, cukup RLS (project member) + middleware route.
func (s *CustomStatusService) ListForProject(ctx context.Context, exec db.Executor, projectID string) ([]repository.CustomStatus, error) {
	if projectID == "" {
		return nil, fmt.Errorf("service.ListForProject: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.ListForScope(ctx, exec, "project", projectID)
	if err != nil {
		return nil, fmt.Errorf("service.ListForProject: %w", err)
	}
	return list, nil
}

// resolveWorkspaceID -- workspace pemilik status ini, dipakai audit log
// (insertCustomStatusAudit SELALU butuh workspace_id nyata, bukan scope_id
// yang untuk status project-scoped adalah project ID) dan otorisasi.
func (s *CustomStatusService) resolveWorkspaceID(ctx context.Context, exec db.Executor, scopeType, scopeID string) (string, error) {
	if scopeType == "workspace" {
		return scopeID, nil
	}
	return s.projects.GetWorkspaceID(ctx, exec, scopeID)
}

// authorizeScope (S4W-05/S5B, dikonfirmasi user) -- CRUD status
// (Create/UpdateNameColor/Move/Undefine/Restore): scope workspace tetap
// Admin Workspace-only (+GA/PA bypass, perilaku existing S4W-05 TIDAK
// diubah); scope project (Track S5B, US-019) menambah PM-of-project
// sebagai otorisasi kedua ("AW Custom Status" adalah halaman admin
// workspace, "PM Custom Status" halaman PM mengatur status project-nya
// sendiri -- keduanya admin_workspace TETAP bisa, konsisten AW sebagai
// pemilik tertinggi workspace).
func (s *CustomStatusService) authorizeScope(ctx context.Context, exec db.Executor, scopeType, scopeID, actorID, actorRole string) error {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return nil
	}
	workspaceID, err := s.resolveWorkspaceID(ctx, exec, scopeType, scopeID)
	if err != nil {
		return fmt.Errorf("service.authorizeScope: %w", err)
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("service.authorizeScope: %w", err)
	}
	if role == "admin_workspace" {
		return nil
	}
	if scopeType == "project" {
		isPM, err := s.projects.IsPM(ctx, exec, scopeID, actorID)
		if err != nil {
			return fmt.Errorf("service.authorizeScope: %w", err)
		}
		if isPM {
			return nil
		}
	}
	return fmt.Errorf("service.authorizeScope: %w", domain.ErrForbidden)
}

// validateNameColor -- aturan sama persis AW/PM Add Status.dc.html: 3-24
// karakter (di-uppercase), unik per scope (mengecualikan statusID sendiri
// saat update), warna salah satu dari 7 token resmi.
func (s *CustomStatusService) validateNameColor(ctx context.Context, exec db.Executor, scopeType, scopeID, name, colorToken, excludeID string) (string, error) {
	name = strings.ToUpper(strings.TrimSpace(name))
	if len(name) < 3 {
		return "", fmt.Errorf("service.validateNameColor: nama status minimal 3 karakter: %w", domain.ErrInvalidInput)
	}
	if len(name) > 24 {
		return "", fmt.Errorf("service.validateNameColor: nama status maksimal 24 karakter: %w", domain.ErrInvalidInput)
	}
	if !customStatusColorTokens[colorToken] {
		return "", fmt.Errorf("service.validateNameColor: warna tidak dikenal: %w", domain.ErrInvalidInput)
	}
	taken, err := s.repo.NameExists(ctx, exec, scopeType, scopeID, name, excludeID)
	if err != nil {
		return "", fmt.Errorf("service.validateNameColor: %w", err)
	}
	if taken {
		return "", fmt.Errorf("service.validateNameColor: %w", domain.ErrCustomStatusNameTaken)
	}
	return name, nil
}

// create -- inti Create/CreateForProject, scopeType eksplisit supaya
// keduanya berbagi validasi+limit+audit yang sama persis. position
// 1-indexed dari FE ("POSISI URUTAN"), 0 atau tidak valid berarti taruh di
// akhir daftar (default form: panjang daftar + 1).
func (s *CustomStatusService) create(ctx context.Context, exec db.Executor, scopeType, scopeID, name, colorToken string, position int, actorID, actorRole string) (*repository.CustomStatus, error) {
	if scopeID == "" {
		return nil, fmt.Errorf("service.create: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeScope(ctx, exec, scopeType, scopeID, actorID, actorRole); err != nil {
		return nil, err
	}
	list, err := s.repo.ListForScope(ctx, exec, scopeType, scopeID)
	if err != nil {
		return nil, fmt.Errorf("service.create: %w", err)
	}
	if len(list) >= customStatusMaxPerScope {
		return nil, fmt.Errorf("service.create: %w", domain.ErrCustomStatusLimitReached)
	}
	name, err = s.validateNameColor(ctx, exec, scopeType, scopeID, name, colorToken, "")
	if err != nil {
		return nil, err
	}
	slot := position - 1
	if slot < 0 || slot > len(list) {
		slot = len(list)
	}
	workspaceID, err := s.resolveWorkspaceID(ctx, exec, scopeType, scopeID)
	if err != nil {
		return nil, fmt.Errorf("service.create: %w", err)
	}
	created, err := s.repo.Create(ctx, exec, scopeType, scopeID, name, colorToken, slot, actorID, actorRole, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.create: %w", err)
	}
	return created, nil
}

// Create -- POST /workspaces/:wsId/statuses (S4W-05, US-020).
func (s *CustomStatusService) Create(ctx context.Context, exec db.Executor, workspaceID, name, colorToken string, position int, actorID, actorRole string) (*repository.CustomStatus, error) {
	return s.create(ctx, exec, "workspace", workspaceID, name, colorToken, position, actorID, actorRole)
}

// CreateForProject -- POST /projects/:id/statuses (Track S5B, US-019, "PM
// Add Status.dc.html").
func (s *CustomStatusService) CreateForProject(ctx context.Context, exec db.Executor, projectID, name, colorToken string, position int, actorID, actorRole string) (*repository.CustomStatus, error) {
	return s.create(ctx, exec, "project", projectID, name, colorToken, position, actorID, actorRole)
}

// UpdateNameColor -- PUT /statuses/:id/appearance (S4W-05, panel Kelola ->
// SIMPAN PERUBAHAN). Nama status SISTEM dikunci (diabaikan walau FE
// terlanjur kirim nilai lain -- pertahanan berlapis, FE sudah disable
// input-nya); warna tetap bisa diubah untuk status sistem maupun kustom.
func (s *CustomStatusService) UpdateNameColor(ctx context.Context, exec db.Executor, statusID, name, colorToken, actorID, actorRole string) error {
	if statusID == "" {
		return fmt.Errorf("service.UpdateNameColor: %w", domain.ErrInvalidInput)
	}
	status, err := s.repo.Get(ctx, exec, statusID)
	if err != nil {
		return err
	}
	if err := s.authorizeScope(ctx, exec, status.ScopeType, status.ScopeID, actorID, actorRole); err != nil {
		return err
	}
	if !customStatusColorTokens[colorToken] {
		return fmt.Errorf("service.UpdateNameColor: %w", domain.ErrInvalidInput)
	}
	finalName := status.Name
	if !status.IsSystem {
		finalName, err = s.validateNameColor(ctx, exec, status.ScopeType, status.ScopeID, name, colorToken, statusID)
		if err != nil {
			return err
		}
	}
	workspaceID, err := s.resolveWorkspaceID(ctx, exec, status.ScopeType, status.ScopeID)
	if err != nil {
		return fmt.Errorf("service.UpdateNameColor: %w", err)
	}
	if err := s.repo.UpdateNameColor(ctx, exec, statusID, finalName, colorToken, actorID, actorRole, workspaceID); err != nil {
		return fmt.Errorf("service.UpdateNameColor: %w", err)
	}
	return nil
}

// Move -- POST /statuses/:id/move (S4W-05, tombol ▲▼). direction: -1 naik,
// +1 turun. Berlaku untuk status apa pun termasuk sistem (urutan board
// tetap diatur AW, cuma nama+undefine sistem yang dikunci).
func (s *CustomStatusService) Move(ctx context.Context, exec db.Executor, statusID string, direction int, actorID, actorRole string) error {
	if statusID == "" || (direction != -1 && direction != 1) {
		return fmt.Errorf("service.Move: %w", domain.ErrInvalidInput)
	}
	status, err := s.repo.Get(ctx, exec, statusID)
	if err != nil {
		return err
	}
	if err := s.authorizeScope(ctx, exec, status.ScopeType, status.ScopeID, actorID, actorRole); err != nil {
		return err
	}
	workspaceID, err := s.resolveWorkspaceID(ctx, exec, status.ScopeType, status.ScopeID)
	if err != nil {
		return fmt.Errorf("service.Move: %w", err)
	}
	if err := s.repo.Move(ctx, exec, status.ScopeType, status.ScopeID, statusID, direction, actorID, actorRole, workspaceID); err != nil {
		return fmt.Errorf("service.Move: %w", err)
	}
	return nil
}

// Undefine -- POST /statuses/:id/undefine (S4W-05, US-021, "⊘ JADIKAN
// UNDEFINED"). Status sistem TIDAK BISA di-undefine (wajib ada di setiap
// project). Dampak ke rule automation (US-053) ditutup S4W-10 -- best-
// effort lewat s.rules, TIDAK PERNAH menggagalkan Undefine yang sudah
// berhasil kalau notifikasi/deaktivasi rule gagal (mengikuti pola best-
// effort dispatchWebhook).
func (s *CustomStatusService) Undefine(ctx context.Context, exec db.Executor, statusID, actorID, actorRole string) error {
	if statusID == "" {
		return fmt.Errorf("service.Undefine: %w", domain.ErrInvalidInput)
	}
	status, err := s.repo.Get(ctx, exec, statusID)
	if err != nil {
		return err
	}
	if err := s.authorizeScope(ctx, exec, status.ScopeType, status.ScopeID, actorID, actorRole); err != nil {
		return err
	}
	if status.IsSystem {
		return fmt.Errorf("service.Undefine: %w", domain.ErrCustomStatusIsSystem)
	}
	workspaceID, err := s.resolveWorkspaceID(ctx, exec, status.ScopeType, status.ScopeID)
	if err != nil {
		return fmt.Errorf("service.Undefine: %w", err)
	}
	if err := s.repo.SetUndefined(ctx, exec, statusID, true, actorID, actorRole, workspaceID); err != nil {
		return fmt.Errorf("service.Undefine: %w", err)
	}
	if s.rules != nil {
		_ = s.rules.DeactivateForStatus(ctx, exec, statusID, status.Name, actorID, actorRole)
	}
	return nil
}

// Restore -- POST /statuses/:id/restore (S4W-05, US-020, "↺ PULIHKAN KE
// TEMPLATE") -- kebalikan Undefine, cuma berlaku untuk status yang memang
// sedang UNDEFINED.
func (s *CustomStatusService) Restore(ctx context.Context, exec db.Executor, statusID, actorID, actorRole string) error {
	if statusID == "" {
		return fmt.Errorf("service.Restore: %w", domain.ErrInvalidInput)
	}
	status, err := s.repo.Get(ctx, exec, statusID)
	if err != nil {
		return err
	}
	if err := s.authorizeScope(ctx, exec, status.ScopeType, status.ScopeID, actorID, actorRole); err != nil {
		return err
	}
	if !status.IsUndefined {
		return fmt.Errorf("service.Restore: %w", domain.ErrCustomStatusNotUndefined)
	}
	workspaceID, err := s.resolveWorkspaceID(ctx, exec, status.ScopeType, status.ScopeID)
	if err != nil {
		return fmt.Errorf("service.Restore: %w", err)
	}
	if err := s.repo.SetUndefined(ctx, exec, statusID, false, actorID, actorRole, workspaceID); err != nil {
		return fmt.Errorf("service.Restore: %w", err)
	}
	return nil
}

// authorizeConfirmToggle -- otorisasi SetRequireStartConfirmation, SENGAJA
// TERPISAH dari authorizeScope (CRUD status AW/PM-of-project-only): untuk
// status workspace-scope, perilaku Phase 4 lama dipertahankan PERSIS --
// SEMBARANG project_manager di workspace ini boleh (bukan cuma PM-of-
// project tertentu, karena toggle ini dulu workspace-wide sebelum Track
// S5B); untuk status project-scope, DIPERKETAT ke PM project ini SAJA --
// konsekuensi wajar dari toggle kini per-project, bukan lagi
// workspace-wide.
func (s *CustomStatusService) authorizeConfirmToggle(ctx context.Context, exec db.Executor, scopeType, scopeID, actorID, actorRole string) error {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return nil
	}
	workspaceID, err := s.resolveWorkspaceID(ctx, exec, scopeType, scopeID)
	if err != nil {
		return fmt.Errorf("service.authorizeConfirmToggle: %w", err)
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("service.authorizeConfirmToggle: %w", err)
	}
	if role == "admin_workspace" {
		return nil
	}
	if scopeType == "workspace" && role == "project_manager" {
		return nil
	}
	if scopeType == "project" {
		isPM, err := s.projects.IsPM(ctx, exec, scopeID, actorID)
		if err != nil {
			return fmt.Errorf("service.authorizeConfirmToggle: %w", err)
		}
		if isPM {
			return nil
		}
	}
	return fmt.Errorf("service.authorizeConfirmToggle: %w", domain.ErrForbidden)
}

// SetRequireStartConfirmation -- PUT /statuses/:id (Phase 4, US-018b/S4-64;
// digeneralisasi Track S5B untuk status project-scoped, lihat komentar
// authorizeConfirmToggle).
func (s *CustomStatusService) SetRequireStartConfirmation(ctx context.Context, exec db.Executor, statusID string, require bool, actorID, actorRole string) error {
	if statusID == "" {
		return fmt.Errorf("service.SetRequireStartConfirmation: %w", domain.ErrInvalidInput)
	}
	status, err := s.repo.Get(ctx, exec, statusID)
	if err != nil {
		return err
	}
	if err := s.authorizeConfirmToggle(ctx, exec, status.ScopeType, status.ScopeID, actorID, actorRole); err != nil {
		return err
	}
	// customStatusUntracked (S4W-05, susulan) -- BACKLOG/DONE/BLOCKED
	// bukan status kerja aktif, konfirmasi mulai tidak berlaku. Sebelumnya
	// cuma ditegakkan di UI prototype (tombol "TIDAK BERLAKU"), sekarang
	// juga di backend -- mencegah state tidak konsisten kalau dipanggil
	// langsung lewat API.
	if require && status.IsSystem && customStatusUntracked[status.Name] {
		return fmt.Errorf("service.SetRequireStartConfirmation: %w", domain.ErrCustomStatusNotTrackable)
	}
	workspaceID, err := s.resolveWorkspaceID(ctx, exec, status.ScopeType, status.ScopeID)
	if err != nil {
		return fmt.Errorf("service.SetRequireStartConfirmation: %w", err)
	}
	if err := s.repo.SetRequireStartConfirmation(ctx, exec, statusID, require, actorID, actorRole, workspaceID); err != nil {
		return fmt.Errorf("service.SetRequireStartConfirmation: %w", err)
	}
	return nil
}

// SetRequirePic -- PUT /statuses/:id/pic-requirement. Parameter per status:
// false = pindah KE status ini tidak menanyakan/menetapkan PIC (status akhir
// seperti DONE/BLOCKED, default sistem); true = perilaku US-017 (wajib pilih
// PIC). Otorisasi sama toggle konfirmasi mulai (authorizeConfirmToggle).
func (s *CustomStatusService) SetRequirePic(ctx context.Context, exec db.Executor, statusID string, require bool, actorID, actorRole string) error {
	if statusID == "" {
		return fmt.Errorf("service.SetRequirePic: %w", domain.ErrInvalidInput)
	}
	status, err := s.repo.Get(ctx, exec, statusID)
	if err != nil {
		return err
	}
	if err := s.authorizeConfirmToggle(ctx, exec, status.ScopeType, status.ScopeID, actorID, actorRole); err != nil {
		return err
	}
	workspaceID, err := s.resolveWorkspaceID(ctx, exec, status.ScopeType, status.ScopeID)
	if err != nil {
		return fmt.Errorf("service.SetRequirePic: %w", err)
	}
	if err := s.repo.SetRequirePic(ctx, exec, statusID, require, actorID, actorRole, workspaceID); err != nil {
		return fmt.Errorf("service.SetRequirePic: %w", err)
	}
	return nil
}
