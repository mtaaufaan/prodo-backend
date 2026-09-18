// Package service -- CustomStatusService (Task Management Core Phase 1;
// require_start_confirmation toggle Phase 4, US-018b; CRUD template
// workspace S4W-05, US-020/021). CRUD status custom PROJECT-level (PM,
// US-019) tetap scope terpisah, belum dibangun -- lihat komentar package
// repository.
package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// customStatusMaxPerWorkspace -- batas AW Add Status.dc.html ("Batas 12
// status per workspace tercapai").
const customStatusMaxPerWorkspace = 12

// customStatusUntracked -- 3 dari 5 status sistem yang bukan status kerja
// aktif (task di situ menunggu keputusan, bukan sedang dikerjakan) --
// konfirmasi "Mulai Pengerjaan" tidak berlaku, sama persis konstanta
// UNTRACKED di AW Custom Status.dc.html.
var customStatusUntracked = map[string]bool{"BACKLOG": true, "DONE": true, "BLOCKED": true}

// customStatusColorTokens -- 7 token warna resmi Tailwind (tailwind.config.ts,
// docs/design.md §2) yang dipakai color picker Add/Kelola Status.
var customStatusColorTokens = map[string]bool{
	"grey": true, "signal": true, "violet": true, "amber": true, "red": true, "blue": true, "mint": true,
}

type customStatusRepository interface {
	ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.CustomStatus, error)
	Get(ctx context.Context, exec db.Executor, statusID string) (*repository.CustomStatus, error)
	NameExists(ctx context.Context, exec db.Executor, workspaceID, name, excludeID string) (bool, error)
	Create(ctx context.Context, exec db.Executor, workspaceID, name, colorToken string, position int, actorID, actorRole string) (*repository.CustomStatus, error)
	UpdateNameColor(ctx context.Context, exec db.Executor, statusID, name, colorToken, actorID, actorRole string) error
	Move(ctx context.Context, exec db.Executor, workspaceID, statusID string, direction int, actorID, actorRole string) error
	SetUndefined(ctx context.Context, exec db.Executor, statusID string, undefined bool, actorID, actorRole string) error
	SetRequireStartConfirmation(ctx context.Context, exec db.Executor, statusID string, require bool, actorID, actorRole string) error
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
	repo  customStatusRepository
	rbac  sprintWorkspaceRoleChecker
	rules customStatusRuleDeactivator
}

func NewCustomStatusService(repo customStatusRepository, rbac sprintWorkspaceRoleChecker, rules customStatusRuleDeactivator) *CustomStatusService {
	return &CustomStatusService{repo: repo, rbac: rbac, rules: rules}
}

func (s *CustomStatusService) ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.CustomStatus, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.ListForWorkspace: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.ListForWorkspace(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.ListForWorkspace: %w", err)
	}
	return list, nil
}

// authorizeAdmin (S4W-05, dikonfirmasi user) -- CRUD template status
// (Create/UpdateNameColor/Move/Undefine/Restore) HANYA Admin Workspace
// (+GA/PA bypass) -- BEDA dari toggle konfirmasi-mulai di bawah yang sudah
// lebih dulu ada dan tetap mengizinkan PM (perilaku existing, tidak
// diubah). "AW Custom Status" adalah halaman admin workspace, PM
// mengatur status di level project-nya sendiri lewat US-019 (scope
// terpisah, belum dibangun).
func (s *CustomStatusService) authorizeAdmin(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return nil
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("service.authorizeAdmin: %w", err)
	}
	if role != "admin_workspace" {
		return fmt.Errorf("service.authorizeAdmin: %w", domain.ErrForbidden)
	}
	return nil
}

// validateNameColor -- aturan sama persis AW Add Status.dc.html: 3-24
// karakter (di-uppercase), unik per workspace (mengecualikan statusID
// sendiri saat update), warna salah satu dari 7 token resmi.
func (s *CustomStatusService) validateNameColor(ctx context.Context, exec db.Executor, workspaceID, name, colorToken, excludeID string) (string, error) {
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
	taken, err := s.repo.NameExists(ctx, exec, workspaceID, name, excludeID)
	if err != nil {
		return "", fmt.Errorf("service.validateNameColor: %w", err)
	}
	if taken {
		return "", fmt.Errorf("service.validateNameColor: %w", domain.ErrCustomStatusNameTaken)
	}
	return name, nil
}

// Create -- POST /workspaces/:wsId/statuses (S4W-05, US-020). position
// 1-indexed dari FE ("POSISI URUTAN"), 0 atau tidak valid berarti taruh di
// akhir daftar (default form: panjang daftar + 1).
func (s *CustomStatusService) Create(ctx context.Context, exec db.Executor, workspaceID, name, colorToken string, position int, actorID, actorRole string) (*repository.CustomStatus, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeAdmin(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return nil, err
	}
	list, err := s.repo.ListForWorkspace(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	if len(list) >= customStatusMaxPerWorkspace {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrCustomStatusLimitReached)
	}
	name, err = s.validateNameColor(ctx, exec, workspaceID, name, colorToken, "")
	if err != nil {
		return nil, err
	}
	slot := position - 1
	if slot < 0 || slot > len(list) {
		slot = len(list)
	}
	created, err := s.repo.Create(ctx, exec, workspaceID, name, colorToken, slot, actorID, actorRole)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	return created, nil
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
	if err := s.authorizeAdmin(ctx, exec, status.ScopeID, actorID, actorRole); err != nil {
		return err
	}
	if !customStatusColorTokens[colorToken] {
		return fmt.Errorf("service.UpdateNameColor: %w", domain.ErrInvalidInput)
	}
	finalName := status.Name
	if !status.IsSystem {
		finalName, err = s.validateNameColor(ctx, exec, status.ScopeID, name, colorToken, statusID)
		if err != nil {
			return err
		}
	}
	if err := s.repo.UpdateNameColor(ctx, exec, statusID, finalName, colorToken, actorID, actorRole); err != nil {
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
	if err := s.authorizeAdmin(ctx, exec, status.ScopeID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.Move(ctx, exec, status.ScopeID, statusID, direction, actorID, actorRole); err != nil {
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
	if err := s.authorizeAdmin(ctx, exec, status.ScopeID, actorID, actorRole); err != nil {
		return err
	}
	if status.IsSystem {
		return fmt.Errorf("service.Undefine: %w", domain.ErrCustomStatusIsSystem)
	}
	if err := s.repo.SetUndefined(ctx, exec, statusID, true, actorID, actorRole); err != nil {
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
	if err := s.authorizeAdmin(ctx, exec, status.ScopeID, actorID, actorRole); err != nil {
		return err
	}
	if !status.IsUndefined {
		return fmt.Errorf("service.Restore: %w", domain.ErrCustomStatusNotUndefined)
	}
	if err := s.repo.SetUndefined(ctx, exec, statusID, false, actorID, actorRole); err != nil {
		return fmt.Errorf("service.Restore: %w", err)
	}
	return nil
}

// SetRequireStartConfirmation -- PUT /statuses/:id (Phase 4, US-018b/S4-64) --
// hanya PM dan Admin Workspace dari workspace pemilik status ini (status
// selalu scope_type='workspace' di codebase ini, lihat komentar package).
// Otorisasi SENGAJA lebih longgar dari authorizeAdmin di atas (PM tetap
// boleh) -- perilaku existing sejak Phase 4, tidak diubah oleh S4W-05.
func (s *CustomStatusService) SetRequireStartConfirmation(ctx context.Context, exec db.Executor, statusID string, require bool, actorID, actorRole string) error {
	if statusID == "" {
		return fmt.Errorf("service.SetRequireStartConfirmation: %w", domain.ErrInvalidInput)
	}
	status, err := s.repo.Get(ctx, exec, statusID)
	if err != nil {
		return err
	}
	if actorRole != "platform_admin" && actorRole != "group_admin" {
		role, err := s.rbac.GetMemberRole(ctx, exec, status.ScopeID, actorID)
		if err != nil {
			return fmt.Errorf("service.SetRequireStartConfirmation: %w", err)
		}
		if role != "admin_workspace" && role != "project_manager" {
			return fmt.Errorf("service.SetRequireStartConfirmation: %w", domain.ErrForbidden)
		}
	}
	// customStatusUntracked (S4W-05, susulan) -- BACKLOG/DONE/BLOCKED
	// bukan status kerja aktif, konfirmasi mulai tidak berlaku. Sebelumnya
	// cuma ditegakkan di UI prototype (tombol "TIDAK BERLAKU"), sekarang
	// juga di backend -- mencegah state tidak konsisten kalau dipanggil
	// langsung lewat API.
	if require && status.IsSystem && customStatusUntracked[status.Name] {
		return fmt.Errorf("service.SetRequireStartConfirmation: %w", domain.ErrCustomStatusNotTrackable)
	}
	if err := s.repo.SetRequireStartConfirmation(ctx, exec, statusID, require, actorID, actorRole); err != nil {
		return fmt.Errorf("service.SetRequireStartConfirmation: %w", err)
	}
	return nil
}
