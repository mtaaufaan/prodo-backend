package service

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// workspaceRepository -- interface didefinisikan di consumer, §3.9.
// Deactivate/Reactivate (S4G-04, Track S4G) sekarang menunjuk kolom
// deactivated_at (NONAKTIF, akses diblokir) -- Archive/Unarchive BARU
// dipisah untuk kolom archived_at lama (ARSIP, read-only), lihat komentar
// WorkspaceRepository.Archive.
type workspaceRepository interface {
	Create(ctx context.Context, exec db.Executor, orgID, name, actorID, actorRole string) (*repository.Workspace, error)
	GetOrgID(ctx context.Context, exec db.Executor, workspaceID string) (string, error)
	Get(ctx context.Context, exec db.Executor, workspaceID string) (*repository.Workspace, error)
	Update(ctx context.Context, exec db.Executor, workspaceID, name, actorID, actorRole string) error
	Archive(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error
	Unarchive(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error
	Deactivate(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error
	Reactivate(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error
	Delete(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error
	List(ctx context.Context, exec db.Executor, orgID string) ([]repository.Workspace, error)
	MoveToOrg(ctx context.Context, exec db.Executor, workspaceID, targetOrgID, actorID, actorRole string) error
}

// orgAuthorizer -- interface didefinisikan di consumer, §3.9.
// Diimplementasikan *OrganizationService (AuthorizeOrgAccess). IsActive
// ditambahkan S4G-04 (Track S4G) untuk guard MoveWorkspace -- org tujuan
// pindah tidak boleh sedang nonaktif.
type orgAuthorizer interface {
	AuthorizeOrgAccess(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error
	IsActive(ctx context.Context, exec db.Executor, orgID string) (bool, error)
}

// roleAssigner -- interface didefinisikan di consumer, §3.9.
// Diimplementasikan *RBACService (AssignRole S2-03, ListMembers ditambahkan
// S4G-04 untuk ReassignAdmin -- cari siapa admin_workspace saat ini).
type roleAssigner interface {
	AssignRole(ctx context.Context, exec db.Executor, workspaceID, userID, role string, invitedBy *string, actorID, actorRole string) (*RoleChangeResult, error)
	ListMembers(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.Member, error)
}

// contactLookup -- interface didefinisikan di consumer, §3.9. Diimplementasikan
// *AccountRepository.FindUserContactByID -- dipakai ReassignAdmin (S4G-04)
// untuk tahu email+nama admin lama/baru saat mengirim notifikasi.
type contactLookup interface {
	FindUserContactByID(ctx context.Context, userID string) (*repository.UserContact, error)
}

// adminChangeNotifier -- interface didefinisikan di consumer, §3.9.
// Diimplementasikan *EmailService (SendWorkspaceAdminChangedEmail).
type adminChangeNotifier interface {
	SendWorkspaceAdminChangedEmail(ctx context.Context, to, displayName, workspaceName string, isNewAdmin bool) error
}

// WorkspaceService -- S3-09, US-008. Otorisasi sama seperti organizations
// (Platform Admin ATAU Group Admin pengelola org target, reuse
// OrganizationService.AuthorizeOrgAccess) karena workspace dibuat DI DALAM
// org tertentu.
type WorkspaceService struct {
	repo     workspaceRepository
	orgs     orgAuthorizer
	rbac     roleAssigner
	contacts contactLookup
	email    adminChangeNotifier
	logger   *zap.Logger
}

func NewWorkspaceService(repo workspaceRepository, orgs orgAuthorizer, rbac roleAssigner, contacts contactLookup, email adminChangeNotifier, logger *zap.Logger) *WorkspaceService {
	return &WorkspaceService{repo: repo, orgs: orgs, rbac: rbac, contacts: contacts, email: email, logger: logger}
}

// CreateWorkspace membuat workspace baru dalam orgID + menunjuk
// adminWorkspaceUserID sebagai Admin Workspace-nya (S3-09 AC) -- reuse
// RBACService.AssignRole (S2-03) untuk assignment role, bukan insert
// workspace_members manual di sini.
func (s *WorkspaceService) CreateWorkspace(ctx context.Context, exec db.Executor, orgID, name, adminWorkspaceUserID, actorID, actorRole string) (*repository.Workspace, error) {
	if orgID == "" || name == "" || adminWorkspaceUserID == "" {
		return nil, fmt.Errorf("service.CreateWorkspace: %w", domain.ErrInvalidInput)
	}
	if err := s.orgs.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return nil, err
	}

	ws, err := s.repo.Create(ctx, exec, orgID, name, actorID, actorRole)
	if err != nil {
		return nil, fmt.Errorf("service.CreateWorkspace: %w", err)
	}

	if _, err := s.rbac.AssignRole(ctx, exec, ws.ID, adminWorkspaceUserID, "admin_workspace", nil, actorID, actorRole); err != nil {
		return nil, fmt.Errorf("service.CreateWorkspace: assign admin_workspace: %w", err)
	}
	return ws, nil
}

// UpdateWorkspace mengubah name workspace (S3-10). Otorisasi ("Admin
// Workspace di workspace ini, atau Platform Admin/Group Admin pengelola
// org", konsisten RLS workspaces_update) sudah ditegakkan
// middleware.RequireRole di routing -- sama pola UpdateMemberRole, tidak
// ada pengecekan tambahan di sini.
func (s *WorkspaceService) UpdateWorkspace(ctx context.Context, exec db.Executor, workspaceID, name, actorID, actorRole string) error {
	if workspaceID == "" || name == "" {
		return fmt.Errorf("service.UpdateWorkspace: %w", domain.ErrInvalidInput)
	}
	if err := s.repo.Update(ctx, exec, workspaceID, name, actorID, actorRole); err != nil {
		return fmt.Errorf("service.UpdateWorkspace: %w", err)
	}
	return nil
}

// DeactivateWorkspace menyetel archived_at (S3-11) -- US-008 AC: akses
// seluruh member diblokir, data tetap tersimpan.
func (s *WorkspaceService) DeactivateWorkspace(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error {
	if workspaceID == "" {
		return fmt.Errorf("service.DeactivateWorkspace: %w", domain.ErrInvalidInput)
	}
	if err := s.repo.Deactivate(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.DeactivateWorkspace: %w", err)
	}
	return nil
}

// ReactivateWorkspace membatalkan deactivate (kebalikan DeactivateWorkspace).
func (s *WorkspaceService) ReactivateWorkspace(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error {
	if workspaceID == "" {
		return fmt.Errorf("service.ReactivateWorkspace: %w", domain.ErrInvalidInput)
	}
	if err := s.repo.Reactivate(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.ReactivateWorkspace: %w", err)
	}
	return nil
}

// ArchiveWorkspace menyetel archived_at (S4G-04, Track S4G, desain
// "GA Workspaces.dc.html") -- ARSIP: read-only, project/task tetap bisa
// dibuka, storage tetap dihitung pada kuota organisasi. Beda dari
// DeactivateWorkspace (akses seluruh member diblokir).
func (s *WorkspaceService) ArchiveWorkspace(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error {
	if workspaceID == "" {
		return fmt.Errorf("service.ArchiveWorkspace: %w", domain.ErrInvalidInput)
	}
	if err := s.repo.Archive(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.ArchiveWorkspace: %w", err)
	}
	return nil
}

// UnarchiveWorkspace membatalkan Archive (kebalikan ArchiveWorkspace).
func (s *WorkspaceService) UnarchiveWorkspace(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error {
	if workspaceID == "" {
		return fmt.Errorf("service.UnarchiveWorkspace: %w", domain.ErrInvalidInput)
	}
	if err := s.repo.Unarchive(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.UnarchiveWorkspace: %w", err)
	}
	return nil
}

// MoveWorkspace memindahkan workspace ke organisasi lain (S4G-04, Track
// S4G, desain "GA Workspaces.dc.html"). Otorisasi ditegakkan atas KEDUA
// organisasi (asal DAN tujuan) -- actor harus punya akses ke org asal
// (workspace ini) maupun org tujuan (supaya GA tidak bisa "mencuri"
// workspace ke org yang bukan kelolaannya). Org tujuan juga harus aktif.
// TANPA guard kuota storage tujuan -- lihat komentar
// WorkspaceRepository.MoveToOrg dan implementation_gaps.md IG-33.
func (s *WorkspaceService) MoveWorkspace(ctx context.Context, exec db.Executor, workspaceID, targetOrgID, actorID, actorRole string) error {
	if workspaceID == "" || targetOrgID == "" {
		return fmt.Errorf("service.MoveWorkspace: %w", domain.ErrInvalidInput)
	}
	sourceOrgID, err := s.repo.GetOrgID(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("service.MoveWorkspace: %w", err)
	}
	if err := s.orgs.AuthorizeOrgAccess(ctx, exec, sourceOrgID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.orgs.AuthorizeOrgAccess(ctx, exec, targetOrgID, actorID, actorRole); err != nil {
		return err
	}
	active, err := s.orgs.IsActive(ctx, exec, targetOrgID)
	if err != nil {
		return fmt.Errorf("service.MoveWorkspace: %w", err)
	}
	if !active {
		return fmt.Errorf("service.MoveWorkspace: %w", domain.ErrOrganizationInactive)
	}

	if err := s.repo.MoveToOrg(ctx, exec, workspaceID, targetOrgID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.MoveWorkspace: %w", err)
	}
	return nil
}

// ReassignAdmin mengganti Admin Workspace penanggung jawab (S4G-04, Track
// S4G, desain "GA Workspaces.dc.html": "wajib terisi -- workspace tidak
// boleh tanpa Admin Workspace"). Admin lama (kalau ada, beda dari yang
// baru) diturunkan ke role "editor" -- BUKAN dikeluarkan dari workspace,
// desain sendiri menyebut "notifikasi ke admin lama" yang mengasumsikan
// mereka masih member, cuma kehilangan status admin. Notifikasi email ke
// admin lama+baru dikirim best-effort (gagal kirim TIDAK menggagalkan
// pengalihan role, sama pola AuthService.sendPlatformAdminLoginAlert).
func (s *WorkspaceService) ReassignAdmin(ctx context.Context, exec db.Executor, workspaceID, newAdminUserID, actorID, actorRole string) error {
	if workspaceID == "" || newAdminUserID == "" {
		return fmt.Errorf("service.ReassignAdmin: %w", domain.ErrInvalidInput)
	}
	orgID, err := s.repo.GetOrgID(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("service.ReassignAdmin: %w", err)
	}
	if err := s.orgs.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return err
	}

	ws, err := s.repo.Get(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("service.ReassignAdmin: %w", err)
	}

	members, err := s.rbac.ListMembers(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("service.ReassignAdmin: %w", err)
	}
	var oldAdminID string
	for _, m := range members {
		if m.Role == "admin_workspace" {
			oldAdminID = m.UserID
			break
		}
	}

	if _, err := s.rbac.AssignRole(ctx, exec, workspaceID, newAdminUserID, "admin_workspace", nil, actorID, actorRole); err != nil {
		return fmt.Errorf("service.ReassignAdmin: assign admin baru: %w", err)
	}
	if oldAdminID != "" && oldAdminID != newAdminUserID {
		if _, err := s.rbac.AssignRole(ctx, exec, workspaceID, oldAdminID, "editor", nil, actorID, actorRole); err != nil {
			return fmt.Errorf("service.ReassignAdmin: turunkan admin lama: %w", err)
		}
	}

	s.notifyAdminChange(ctx, newAdminUserID, ws.Name, true)
	if oldAdminID != "" && oldAdminID != newAdminUserID {
		s.notifyAdminChange(ctx, oldAdminID, ws.Name, false)
	}
	return nil
}

func (s *WorkspaceService) notifyAdminChange(ctx context.Context, userID, workspaceName string, isNewAdmin bool) {
	contact, err := s.contacts.FindUserContactByID(ctx, userID)
	if err != nil {
		s.logger.Error("ReassignAdmin: gagal ambil kontak untuk notifikasi", zap.String("user_id", userID), zap.Error(err))
		return
	}
	if err := s.email.SendWorkspaceAdminChangedEmail(ctx, contact.Email, contact.DisplayName, workspaceName, isNewAdmin); err != nil {
		s.logger.Error("ReassignAdmin: gagal kirim email notifikasi", zap.String("user_id", userID), zap.Error(err))
	}
}

// DeleteWorkspace menghapus workspace permanen (S3-12). BEDA dari Update/
// Deactivate/Reactivate -- middleware routing-nya cuma gerbang platform-role
// kasar (requireOrgAdmin), BUKAN RequireRole(admin_workspace), karena RLS
// `workspaces_delete` sengaja TIDAK mengizinkan Admin Workspace (cuma
// Platform Admin/Group Admin pemilik org) -- lihat catatan
// WorkspaceRepository.GetOrgID. Otorisasi halus (GA benar-benar pengelola
// org PEMILIK workspace ini) karena itu ditegakkan EKSPLISIT di sini,
// resolve org_id dari workspace_id dulu baru panggil AuthorizeOrgAccess --
// sama pola OrganizationService.DeleteOrganization.
func (s *WorkspaceService) DeleteWorkspace(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error {
	if workspaceID == "" {
		return fmt.Errorf("service.DeleteWorkspace: %w", domain.ErrInvalidInput)
	}
	orgID, err := s.repo.GetOrgID(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("service.DeleteWorkspace: %w", err)
	}
	if err := s.orgs.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return err
	}

	if err := s.repo.Delete(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.DeleteWorkspace: %w", err)
	}
	return nil
}

// GetWorkspace mengembalikan satu workspace (S4-04 prasyarat -- nama
// workspace untuk header WorkspaceLayout). TIDAK ada pengecekan otorisasi
// tambahan, scoping penuh lewat RLS workspaces_select.
func (s *WorkspaceService) GetWorkspace(ctx context.Context, exec db.Executor, workspaceID string) (*repository.Workspace, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.GetWorkspace: %w", domain.ErrInvalidInput)
	}
	ws, err := s.repo.Get(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.GetWorkspace: %w", err)
	}
	return ws, nil
}

// ListWorkspaces mengembalikan workspace yang terlihat oleh actor dalam
// satu organisasi -- scoping sepenuhnya lewat RLS (lihat repository.List),
// sama pola OrganizationService.ListOrganizations.
func (s *WorkspaceService) ListWorkspaces(ctx context.Context, exec db.Executor, orgID string) ([]repository.Workspace, error) {
	list, err := s.repo.List(ctx, exec, orgID)
	if err != nil {
		return nil, fmt.Errorf("service.ListWorkspaces: %w", err)
	}
	return list, nil
}
