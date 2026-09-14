package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

var projectCodePattern = regexp.MustCompile(`^[A-Z]{2,5}$`)

// projectRepository -- interface didefinisikan di consumer, §3.9.
type projectRepository interface {
	GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error)
	Create(ctx context.Context, exec db.Executor, workspaceID, name, code, pmUserID, actorID, actorRole string) (*repository.Project, error)
	List(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.Project, error)
	NameExists(ctx context.Context, exec db.Executor, workspaceID, name, excludeProjectID string) (bool, error)
	Update(ctx context.Context, exec db.Executor, projectID, name, pmUserID, actorID, actorRole string) error
	SetArchived(ctx context.Context, exec db.Executor, projectID string, archive bool, actorID, actorRole string) error
	SoftDelete(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error
	Restore(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error
	SetAllowEditorStoryPoints(ctx context.Context, exec db.Executor, projectID string, allow bool) error
	AssignPendingPM(ctx context.Context, exec db.Executor, projectID, userID string) error
	RemovePM(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error
	SetPM(ctx context.Context, exec db.Executor, projectID, userID, actorID, actorRole string) error
	GetPendingPMInvitationID(ctx context.Context, exec db.Executor, projectID string) (string, error)
}

// projectUserFinder -- interface didefinisikan di consumer, diimplementasikan
// *AccountRepository.FindUserIDByEmail (sama dipakai WorkspaceService.
// CreateWorkspace) -- dipakai resolvePM cek apakah email PM yang diketik
// AW sudah terdaftar user PRODO di mana pun (bukan cuma di workspace ini).
type projectUserFinder interface {
	FindUserIDByEmail(ctx context.Context, email string) (string, error)
}

// projectPMInviter -- interface didefinisikan di consumer, diimplementasikan
// *InvitationService (CreateInvitation/CancelInvitation/GetWorkspaceName
// sudah ada semua) -- dipakai resolvePM/AssignPM jalur "undang PM baru"
// (email belum terdaftar sama sekali).
type projectPMInviter interface {
	CreateInvitation(ctx context.Context, exec db.Executor, email, workspaceID, role, invitedByUserID, workspaceName, inviterName, projectID, displayName string) (*Invitation, error)
	CancelInvitation(ctx context.Context, exec db.Executor, workspaceID, invitationID, actorID string) error
	GetWorkspaceName(ctx context.Context, exec db.Executor, workspaceID string) (string, error)
}

// projectWebhookDispatcher -- WebhookService.Dispatch (Track S4G), 3 dari 10
// event desain "GA Add Webhook.dc.html" yang punya trigger nyata sekarang --
// lihat implementation_gaps.md IG-44. Kegagalan Dispatch TIDAK PERNAH
// menggagalkan mutasi project itu sendiri (lihat pemanggil) -- pengiriman
// webhook best-effort, bukan bagian dari kontrak API project.
type projectWebhookDispatcher interface {
	Dispatch(ctx context.Context, exec db.Executor, orgID, eventType string, data map[string]any) error
}

// ProjectService -- S4-02/03, US-012. Route POST/GET /workspaces/:wsId/projects
// digerbangi middleware.RequireRole (punya :wsId). Route PUT/DELETE/archive
// /projects/:id TIDAK (tidak ada :wsId di path) -- otorisasi penuh lewat
// authorize() di sini, sama pola ProjectMemberService.
type ProjectService struct {
	repo     projectRepository
	orgs     orgAuthorizer
	rbac     projectRoleChecker
	webhooks projectWebhookDispatcher
	contacts projectUserFinder
	invites  projectPMInviter
	logger   *zap.Logger
}

func NewProjectService(repo projectRepository, orgs orgAuthorizer, rbac projectRoleChecker, webhooks projectWebhookDispatcher, contacts projectUserFinder, invites projectPMInviter, logger *zap.Logger) *ProjectService {
	return &ProjectService{repo: repo, orgs: orgs, rbac: rbac, webhooks: webhooks, contacts: contacts, invites: invites, logger: logger}
}

// dispatchWebhook -- best-effort: kegagalan HANYA di-log, TIDAK PERNAH
// menggagalkan mutasi project yang sudah berhasil (lihat pemanggil).
// Pengiriman sungguhan (dengan retry) terjadi di job async, panggilan ini
// cuma mengantre.
func (s *ProjectService) dispatchWebhook(ctx context.Context, exec db.Executor, workspaceID, eventType string, data map[string]any) {
	if s.webhooks == nil {
		return
	}
	orgID, err := s.rbac.GetWorkspaceOrgID(ctx, exec, workspaceID)
	if err != nil {
		s.logger.Warn("dispatchWebhook: gagal resolve org dari workspace", zap.String("workspace_id", workspaceID), zap.Error(err))
		return
	}
	if err := s.webhooks.Dispatch(ctx, exec, orgID, eventType, data); err != nil {
		s.logger.Warn("dispatchWebhook: gagal antre pengiriman", zap.String("event_type", eventType), zap.Error(err))
	}
}

// authorize menolak actor yang bukan PA/GA-of-org/AW/PM di workspace
// pemilik projectID -- dipakai Update/SetArchived/SoftDelete (soft-delete,
// BUKAN hard-delete, jadi AW/PM ikut boleh -- lihat komentar
// ProjectRepository.SoftDelete). Mengembalikan workspaceID untuk caller.
func (s *ProjectService) authorize(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) (string, error) {
	workspaceID, err := s.repo.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return "", err
	}
	orgID, err := s.rbac.GetWorkspaceOrgID(ctx, exec, workspaceID)
	if err != nil {
		return "", fmt.Errorf("service.authorize: %w", err)
	}
	if err := s.orgs.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err == nil {
		return workspaceID, nil
	}

	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return "", fmt.Errorf("service.authorize: %w", err)
	}
	if role != "admin_workspace" && role != "project_manager" {
		return "", fmt.Errorf("service.authorize: %w", domain.ErrForbidden)
	}
	return workspaceID, nil
}

// authorizeOrgOnly -- Restore (S4-02): desain asli cuma bilang "dapat
// dipulihkan Group Admin", TIDAK menyebut AW/PM -- pemulihan sengaja lebih
// ketat dari hapusnya sendiri (siapa saja yang boleh hapus tidak otomatis
// boleh pulihkan, mencegah AW/PM menutupi kesalahannya sendiri tanpa jejak
// GA).
func (s *ProjectService) authorizeOrgOnly(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	workspaceID, err := s.repo.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return err
	}
	orgID, err := s.rbac.GetWorkspaceOrgID(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("service.authorizeOrgOnly: %w", err)
	}
	if err := s.orgs.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.authorizeOrgOnly: %w", domain.ErrForbidden)
	}
	return nil
}

// pmResolution -- hasil resolvePM. TEPAT SATU dari ResolvedUserID (PM
// langsung aktif sekarang) atau InviteEmail (project masuk status
// "menunggu PM", undangan project_manager perlu dibuat caller) terisi.
type pmResolution struct {
	ResolvedUserID string
	InviteEmail    string
	InviteName     string
}

// resolvePM (S4W susulan, dikonfirmasi user 2026-09-13 -- "buat seperti
// Tambah/Kelola Workspace, tapi tetap pertahankan daftar member yang
// sudah ada") menerima PERSIS SATU dari pmUserID/pmEmail, TEPAT sama pola
// WorkspaceService.CreateWorkspace jalur admin_workspace_user_id/
// admin_workspace_email:
//  1. pmUserID: member workspace MANAPUN (bukan cuma yang sudah
//     project_manager, beda dari AC lama) -- dinaikkan rolenya lewat
//     rbac.AssignRole (reuse penuh, termasuk guard "jangan turunkan admin
//     terakhir" kalau target itu kebetulan admin_workspace satu-satunya).
//  2. pmEmail yang SUDAH terdaftar user PRODO di mana pun -- diresolve
//     lewat FindUserIDByEmail, LANGSUNG jadi PM aktif (sama seperti #1,
//     tidak perlu undangan) -- efisiensi utama yang diminta user.
//  3. pmEmail yang BELUM terdaftar sama sekali -- TIDAK bisa resolve
//     sekarang, caller (Create/AssignPM) yang membuat undangan
//     project_manager tertaut project ini ("menunggu PM").
func (s *ProjectService) resolvePM(ctx context.Context, exec db.Executor, workspaceID, pmUserID, pmEmail, pmName, actorID, actorRole string) (*pmResolution, error) {
	if pmUserID != "" {
		role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, pmUserID)
		if err != nil {
			return nil, fmt.Errorf("service.resolvePM: %w", err)
		}
		if role == "" {
			return nil, fmt.Errorf("service.resolvePM: %w", domain.ErrInvalidInput)
		}
		if _, err := s.rbac.AssignRole(ctx, exec, workspaceID, pmUserID, "project_manager", nil, actorID, actorRole, ""); err != nil {
			return nil, fmt.Errorf("service.resolvePM: %w", err)
		}
		return &pmResolution{ResolvedUserID: pmUserID}, nil
	}

	existingID, err := s.contacts.FindUserIDByEmail(ctx, pmEmail)
	switch {
	case err == nil:
		if _, err := s.rbac.AssignRole(ctx, exec, workspaceID, existingID, "project_manager", &actorID, actorID, actorRole, ""); err != nil {
			return nil, fmt.Errorf("service.resolvePM: %w", err)
		}
		return &pmResolution{ResolvedUserID: existingID}, nil
	case errors.Is(err, pgx.ErrNoRows):
		if pmName == "" {
			return nil, fmt.Errorf("service.resolvePM: %w", domain.ErrInvalidInput)
		}
		return &pmResolution{InviteEmail: pmEmail, InviteName: pmName}, nil
	default:
		return nil, fmt.Errorf("service.resolvePM: %w", err)
	}
}

// invitePM membuat undangan project_manager tertaut projectID (S4W
// susulan) -- dipanggil Create/AssignPM setelah resolvePM mengembalikan
// InviteEmail (email belum terdaftar). inviteeName (pm.InviteName) SEKARANG
// diteruskan ke CreateInvitation sebagai display_name (diperbaiki
// 2026-09-14, ditemukan user: nama PM yang diisi saat undang PM baru tidak
// pernah muncul di form aktivasi -- SEBELUMNYA cuma dipakai resolvePM
// sebagai syarat validasi lalu dibuang begitu saja). Mengambil nama
// workspace untuk isi email; kegagalan CreateInvitation PROPAGATE (bukan
// best-effort) karena tanpa undangan project akan permanen tanpa PM.
func (s *ProjectService) invitePM(ctx context.Context, exec db.Executor, workspaceID, projectID, email, actorID, inviterName, inviteeName string) error {
	workspaceName, err := s.invites.GetWorkspaceName(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("service.invitePM: %w", err)
	}
	if _, err := s.invites.CreateInvitation(ctx, exec, email, workspaceID, "project_manager", actorID, workspaceName, inviterName, projectID, inviteeName); err != nil {
		return fmt.Errorf("service.invitePM: %w", err)
	}
	return nil
}

// cancelExistingPMInvitation membatalkan undangan PM pending yang tertaut
// project ini kalau ada -- dipanggil sebelum menetapkan PM baru (resolved
// ATAU undangan baru) supaya satu project tidak pernah punya lebih dari
// satu undangan PM pending sekaligus.
func (s *ProjectService) cancelExistingPMInvitation(ctx context.Context, exec db.Executor, workspaceID, projectID, actorID string) error {
	pendingID, err := s.repo.GetPendingPMInvitationID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("service.cancelExistingPMInvitation: %w", err)
	}
	if pendingID == "" {
		return nil
	}
	if err := s.invites.CancelInvitation(ctx, exec, workspaceID, pendingID, actorID); err != nil {
		return fmt.Errorf("service.cancelExistingPMInvitation: %w", err)
	}
	return nil
}

// Create membuat project baru (S4-02, diperluas S4W susulan). code WAJIB;
// PM ditunjuk lewat PERSIS SATU dari pmUserID/pmEmail (mutual exclusion
// ditegakkan handler, sama pola WorkspaceHandler.CreateWorkspace) --
// project TETAP dibuat meski PM masih undangan pending ("menunggu PM",
// pm_user_id NULL), BEDA dari AC lama yang mewajibkan PM aktif sejak awal.
// inviterName dipakai isi email undangan kalau jalur invite-baru terpakai.
func (s *ProjectService) Create(ctx context.Context, exec db.Executor, workspaceID, name, code, pmUserID, pmEmail, pmName, actorID, actorRole, inviterName string) (*repository.Project, error) {
	name = strings.TrimSpace(name)
	code = strings.ToUpper(strings.TrimSpace(code))
	if workspaceID == "" || name == "" || (pmUserID == "" && pmEmail == "") {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrInvalidInput)
	}
	if !projectCodePattern.MatchString(code) {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrInvalidInput)
	}
	nameTaken, err := s.repo.NameExists(ctx, exec, workspaceID, name, "")
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	if nameTaken {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrProjectNameTaken)
	}

	pm, err := s.resolvePM(ctx, exec, workspaceID, pmUserID, pmEmail, pmName, actorID, actorRole)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}

	p, err := s.repo.Create(ctx, exec, workspaceID, name, code, pm.ResolvedUserID, actorID, actorRole)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}

	if pm.InviteEmail != "" {
		if err := s.invitePM(ctx, exec, workspaceID, p.ID, pm.InviteEmail, actorID, inviterName, pm.InviteName); err != nil {
			return nil, fmt.Errorf("service.Create: %w", err)
		}
	}

	s.dispatchWebhook(ctx, exec, workspaceID, "project.created", map[string]any{"id": p.ID, "name": p.Name, "code": p.Code})
	return p, nil
}

// List mengembalikan seluruh project workspace (S4-04 ProjectListPage) --
// TIDAK ada pengecekan otorisasi tambahan, scoping penuh lewat RLS
// projects_select + middleware.RequireRole di routing (semua workspace
// role boleh lihat).
func (s *ProjectService) List(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.Project, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.List: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.List(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.List: %w", err)
	}
	return list, nil
}

// Update mengubah nama project (S4-02). PM penanggung jawab TIDAK LAGI
// diubah lewat sini sejak S4W susulan -- lihat AssignPM/RemovePM
// (panel Kelola punya seksi PM sendiri, terpisah dari "Simpan Perubahan"
// nama, sama pola ManageWorkspaceModal yang memisahkan ubah nama vs
// kelola admin).
func (s *ProjectService) Update(ctx context.Context, exec db.Executor, projectID, name, actorID, actorRole string) error {
	name = strings.TrimSpace(name)
	if projectID == "" || name == "" {
		return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
	}
	workspaceID, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return err
	}
	nameTaken, err := s.repo.NameExists(ctx, exec, workspaceID, name, projectID)
	if err != nil {
		return err
	}
	if nameTaken {
		return fmt.Errorf("service.Update: %w", domain.ErrProjectNameTaken)
	}
	if err := s.repo.Update(ctx, exec, projectID, name, "", actorID, actorRole); err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	s.dispatchWebhook(ctx, exec, workspaceID, "project.updated", map[string]any{"id": projectID, "name": name})
	return nil
}

// AssignPM (S4W susulan) menetapkan/mengganti PM penanggung jawab --
// dipakai panel Kelola baik saat project sudah punya PM aktif (ganti) MAUPUN
// saat masih "menunggu PM" (isi pertama kali). Sama pola resolvePM Create:
// pmUserID ATAU pmEmail (mutual exclusion ditegakkan handler). Undangan PM
// pending LAMA (kalau ada) otomatis dibatalkan dulu -- satu project cuma
// boleh punya SATU undangan PM pending sekaligus.
func (s *ProjectService) AssignPM(ctx context.Context, exec db.Executor, projectID, pmUserID, pmEmail, pmName, actorID, actorRole, inviterName string) error {
	if projectID == "" || (pmUserID == "" && pmEmail == "") {
		return fmt.Errorf("service.AssignPM: %w", domain.ErrInvalidInput)
	}
	workspaceID, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return err
	}
	pm, err := s.resolvePM(ctx, exec, workspaceID, pmUserID, pmEmail, pmName, actorID, actorRole)
	if err != nil {
		return fmt.Errorf("service.AssignPM: %w", err)
	}
	if err := s.cancelExistingPMInvitation(ctx, exec, workspaceID, projectID, actorID); err != nil {
		return fmt.Errorf("service.AssignPM: %w", err)
	}
	if pm.ResolvedUserID != "" {
		if err := s.repo.SetPM(ctx, exec, projectID, pm.ResolvedUserID, actorID, actorRole); err != nil {
			return fmt.Errorf("service.AssignPM: %w", err)
		}
		return nil
	}
	// Jalur undang-baru (email belum terdaftar): PM AKTIF saat ini (kalau
	// ada) dikosongkan dulu -- project balik ke "menunggu PM" sampai
	// undangan ini diterima, konsisten dengan Create yang juga membuat
	// project tanpa PM aktif untuk kasus yang sama.
	if err := s.repo.RemovePM(ctx, exec, projectID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.AssignPM: %w", err)
	}
	if err := s.invitePM(ctx, exec, workspaceID, projectID, pm.InviteEmail, actorID, inviterName, pm.InviteName); err != nil {
		return fmt.Errorf("service.AssignPM: %w", err)
	}
	return nil
}

// RemovePM (S4W susulan) mengosongkan PM aktif TANPA pengganti -- project
// masuk/kembali ke status "menunggu PM". Undangan PM pending (kalau ada,
// jarang -- biasanya cuma ada saat TIDAK ada PM aktif) ikut dibatalkan
// supaya tidak ada undangan mengambang begitu AW eksplisit menghapus.
func (s *ProjectService) RemovePM(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	if projectID == "" {
		return fmt.Errorf("service.RemovePM: %w", domain.ErrInvalidInput)
	}
	workspaceID, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return err
	}
	if err := s.cancelExistingPMInvitation(ctx, exec, workspaceID, projectID, actorID); err != nil {
		return fmt.Errorf("service.RemovePM: %w", err)
	}
	if err := s.repo.RemovePM(ctx, exec, projectID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.RemovePM: %w", err)
	}
	return nil
}

// SetAllowEditorStoryPoints -- PUT /projects/:id/settings (Task Management
// Core Phase 4, US-018a/S4-56). Gate sama seperti Update (PM/AW/org-access) --
// reuse s.authorize, tidak ada gate baru.
func (s *ProjectService) SetAllowEditorStoryPoints(ctx context.Context, exec db.Executor, projectID string, allow bool, actorID, actorRole string) error {
	if projectID == "" {
		return fmt.Errorf("service.SetAllowEditorStoryPoints: %w", domain.ErrInvalidInput)
	}
	if _, err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.SetAllowEditorStoryPoints(ctx, exec, projectID, allow); err != nil {
		return fmt.Errorf("service.SetAllowEditorStoryPoints: %w", err)
	}
	return nil
}

// SetArchived mengarsipkan/batal-arsip project (S4-03).
func (s *ProjectService) SetArchived(ctx context.Context, exec db.Executor, projectID string, archive bool, actorID, actorRole string) error {
	if projectID == "" {
		return fmt.Errorf("service.SetArchived: %w", domain.ErrInvalidInput)
	}
	if _, err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.SetArchived(ctx, exec, projectID, archive, actorID, actorRole); err != nil {
		return fmt.Errorf("service.SetArchived: %w", err)
	}
	return nil
}

// Delete melakukan soft-delete (S4-02) -- lihat komentar
// ProjectRepository.SoftDelete kenapa ini bukan hard-delete.
func (s *ProjectService) Delete(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	if projectID == "" {
		return fmt.Errorf("service.Delete: %w", domain.ErrInvalidInput)
	}
	workspaceID, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return err
	}
	if err := s.repo.SoftDelete(ctx, exec, projectID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	s.dispatchWebhook(ctx, exec, workspaceID, "project.deleted", map[string]any{"id": projectID})
	return nil
}

// Restore membatalkan soft-delete -- Group Admin/Platform Admin saja.
func (s *ProjectService) Restore(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	if projectID == "" {
		return fmt.Errorf("service.Restore: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeOrgOnly(ctx, exec, projectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.Restore(ctx, exec, projectID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.Restore: %w", err)
	}
	return nil
}
