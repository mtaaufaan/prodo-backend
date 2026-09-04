package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
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
	ListByGroup(ctx context.Context, exec db.Executor, groupID string) ([]repository.WorkspaceListRow, error)
	MoveToOrg(ctx context.Context, exec db.Executor, workspaceID, targetOrgID, actorID, actorRole string) error
}

// orgAuthorizer -- interface didefinisikan di consumer, §3.9.
// Diimplementasikan *OrganizationService (AuthorizeOrgAccess). IsActive
// ditambahkan S4G-04 (Track S4G) untuk guard MoveWorkspace -- org tujuan
// pindah tidak boleh sedang nonaktif. IsGroupAdminOfGroup ditambahkan
// S4G-05 (Track S4G) untuk ListWorkspacesByGroup -- pola sama persis
// authorizeGroup di OrganizationService/GroupService (duplikasi kecil
// disengaja, bukan diekstrak jadi helper -- konsisten dengan pola yang
// sudah ada di kedua service itu).
type orgAuthorizer interface {
	AuthorizeOrgAccess(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error
	IsActive(ctx context.Context, exec db.Executor, orgID string) (bool, error)
	IsGroupAdminOfGroup(ctx context.Context, exec db.Executor, userID, groupID string) (bool, error)
}

// roleAssigner -- interface didefinisikan di consumer, §3.9.
// Diimplementasikan *RBACService (AssignRole S2-03, ListMembers ditambahkan
// S4G-04 untuk ReassignAdmin -- cari siapa admin_workspace saat ini).
// ListOrgCandidates ditambahkan S4G-05 (Track S4G) untuk picker "MEMBER
// YANG ADA" (Tambah Workspace) dan dropdown ganti admin (Kelola Workspace).
type roleAssigner interface {
	AssignRole(ctx context.Context, exec db.Executor, workspaceID, userID, role string, invitedBy *string, actorID, actorRole string) (*RoleChangeResult, error)
	ListMembers(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.Member, error)
	ListOrgCandidates(ctx context.Context, exec db.Executor, orgID string) ([]repository.Member, error)
}

// contactLookup -- interface didefinisikan di consumer, §3.9. Diimplementasikan
// *AccountRepository.FindUserContactByID -- dipakai ReassignAdmin (S4G-04)
// untuk tahu email+nama admin lama/baru saat mengirim notifikasi.
// FindUserIDByEmail ditambahkan S4G-05 (Track S4G) -- CreateWorkspace jalur
// "UNDANG BARU" cek dulu apakah email itu sudah terdaftar (kalau ya, tambah
// langsung sebagai member alih-alih bikin undangan, sama pola S2-23
// InvitationService.CreateBulkInvitations).
type contactLookup interface {
	FindUserContactByID(ctx context.Context, userID string) (*repository.UserContact, error)
	FindUserIDByEmail(ctx context.Context, email string) (string, error)
}

// invitationCreator -- interface didefinisikan di consumer, §3.9.
// Diimplementasikan *InvitationService.CreateInvitation -- dipakai
// CreateWorkspace (S4G-05) jalur "UNDANG BARU" kalau email admin belum
// terdaftar sebagai user, reuse penuh mekanisme undangan 72 jam yang sudah
// ada (POST /workspaces/:wsId/invitations) alih-alih membangun jalur baru.
type invitationCreator interface {
	CreateInvitation(ctx context.Context, exec db.Executor, email, workspaceID, role, invitedByUserID, workspaceName, inviterName string) (*Invitation, error)
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
	invites  invitationCreator
	logger   *zap.Logger
}

func NewWorkspaceService(repo workspaceRepository, orgs orgAuthorizer, rbac roleAssigner, contacts contactLookup, email adminChangeNotifier, invites invitationCreator, logger *zap.Logger) *WorkspaceService {
	return &WorkspaceService{repo: repo, orgs: orgs, rbac: rbac, contacts: contacts, email: email, invites: invites, logger: logger}
}

// CreateWorkspace membuat workspace baru dalam orgID + menunjuk Admin
// Workspace-nya (S3-09 AC, diperluas S4G-05/Track S4G sesuai desain
// "GA Add Workspace.dc.html" 2 tab "MEMBER YANG ADA"/"UNDANG BARU").
// PERSIS SATU dari adminWorkspaceUserID (member existing, tab pertama) ATAU
// adminEmail (tab kedua, +adminName kalau email itu belum terdaftar sebagai
// user) wajib diisi -- caller (handler) yang menegakkan validasi mutual
// exclusion ini, service cuma percaya parameter yang sudah bersih.
// inviterName disuplai caller (handler sudah resolve dari actorID, sama
// pola InvitationHandler.CreateInvitations) -- service ini tidak query
// ulang nama actor.
func (s *WorkspaceService) CreateWorkspace(ctx context.Context, exec db.Executor, orgID, name, adminWorkspaceUserID, adminEmail, adminName, actorID, actorRole, inviterName string) (*repository.Workspace, error) {
	if orgID == "" || name == "" || (adminWorkspaceUserID == "" && adminEmail == "") {
		return nil, fmt.Errorf("service.CreateWorkspace: %w", domain.ErrInvalidInput)
	}
	if err := s.orgs.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return nil, err
	}

	ws, err := s.repo.Create(ctx, exec, orgID, name, actorID, actorRole)
	if err != nil {
		return nil, fmt.Errorf("service.CreateWorkspace: %w", err)
	}

	if adminWorkspaceUserID != "" {
		if _, err := s.rbac.AssignRole(ctx, exec, ws.ID, adminWorkspaceUserID, "admin_workspace", nil, actorID, actorRole); err != nil {
			return nil, fmt.Errorf("service.CreateWorkspace: assign admin_workspace: %w", err)
		}
		return ws, nil
	}

	// Jalur "UNDANG BARU" -- cek dulu email itu sudah user terdaftar atau
	// belum (S2-23, sama pola CreateBulkInvitations): sudah terdaftar ->
	// tambah langsung, TIDAK ADA undangan baru dikirim.
	existingUserID, err := s.contacts.FindUserIDByEmail(ctx, adminEmail)
	switch {
	case err == nil:
		if _, err := s.rbac.AssignRole(ctx, exec, ws.ID, existingUserID, "admin_workspace", &actorID, actorID, actorRole); err != nil {
			return nil, fmt.Errorf("service.CreateWorkspace: assign admin_workspace (email existing): %w", err)
		}
	case errors.Is(err, pgx.ErrNoRows):
		if adminName == "" {
			return nil, fmt.Errorf("service.CreateWorkspace: %w", domain.ErrInvalidInput)
		}
		if _, err := s.invites.CreateInvitation(ctx, exec, adminEmail, ws.ID, "admin_workspace", actorID, name, inviterName); err != nil {
			return nil, fmt.Errorf("service.CreateWorkspace: undang admin_workspace: %w", err)
		}
	default:
		return nil, fmt.Errorf("service.CreateWorkspace: cek email admin: %w", err)
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

// ListWorkspacesByGroup mengembalikan workspace lintas SELURUH organisasi
// dalam satu grup (S4G-05, Track S4G, desain "GA Workspaces.dc.html").
// groupID kosong -- Platform Admin bare-render, TIDAK difilter, RLS
// `workspaces_select` yang menentukan visibility (PA lihat semua). groupID
// terisi -- Group Admin, ditegakkan actor benar-benar pengelola grup itu
// (sama pola authorizeGroup di OrganizationService/GroupService).
func (s *WorkspaceService) ListWorkspacesByGroup(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) ([]repository.WorkspaceListRow, error) {
	if groupID != "" && actorRole != "platform_admin" {
		isGA, err := s.orgs.IsGroupAdminOfGroup(ctx, exec, actorID, groupID)
		if err != nil {
			return nil, fmt.Errorf("service.ListWorkspacesByGroup: %w", err)
		}
		if !isGA {
			return nil, fmt.Errorf("service.ListWorkspacesByGroup: %w", domain.ErrForbidden)
		}
	}
	list, err := s.repo.ListByGroup(ctx, exec, groupID)
	if err != nil {
		return nil, fmt.Errorf("service.ListWorkspacesByGroup: %w", err)
	}
	return list, nil
}

// ListCandidateAdmins mengembalikan member unik lintas seluruh workspace
// dalam satu organisasi (S4G-05, Track S4G) -- picker "MEMBER YANG ADA" di
// Tambah Workspace, dan dropdown ganti admin di Kelola Workspace. Otorisasi
// sama seperti CreateWorkspace (Platform Admin/Group Admin pengelola org).
func (s *WorkspaceService) ListCandidateAdmins(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) ([]repository.Member, error) {
	if orgID == "" {
		return nil, fmt.Errorf("service.ListCandidateAdmins: %w", domain.ErrInvalidInput)
	}
	if err := s.orgs.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return nil, err
	}
	list, err := s.rbac.ListOrgCandidates(ctx, exec, orgID)
	if err != nil {
		return nil, fmt.Errorf("service.ListCandidateAdmins: %w", err)
	}
	return list, nil
}
