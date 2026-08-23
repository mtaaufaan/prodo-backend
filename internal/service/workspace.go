package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// workspaceRepository -- interface didefinisikan di consumer, §3.9.
type workspaceRepository interface {
	Create(ctx context.Context, exec db.Executor, orgID, name, actorID, actorRole string) (*repository.Workspace, error)
	GetOrgID(ctx context.Context, exec db.Executor, workspaceID string) (string, error)
	Update(ctx context.Context, exec db.Executor, workspaceID, name, actorID, actorRole string) error
	Deactivate(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error
	Reactivate(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error
	Delete(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error
	List(ctx context.Context, exec db.Executor, orgID string) ([]repository.Workspace, error)
}

// orgAuthorizer -- interface didefinisikan di consumer, §3.9.
// Diimplementasikan *OrganizationService (AuthorizeOrgAccess).
type orgAuthorizer interface {
	AuthorizeOrgAccess(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error
}

// roleAssigner -- interface didefinisikan di consumer, §3.9.
// Diimplementasikan *RBACService (AssignRole, S2-03).
type roleAssigner interface {
	AssignRole(ctx context.Context, exec db.Executor, workspaceID, userID, role string, invitedBy *string, actorID, actorRole string) (*RoleChangeResult, error)
}

// WorkspaceService -- S3-09, US-008. Otorisasi sama seperti organizations
// (Platform Admin ATAU Group Admin pengelola org target, reuse
// OrganizationService.AuthorizeOrgAccess) karena workspace dibuat DI DALAM
// org tertentu.
type WorkspaceService struct {
	repo workspaceRepository
	orgs orgAuthorizer
	rbac roleAssigner
}

func NewWorkspaceService(repo workspaceRepository, orgs orgAuthorizer, rbac roleAssigner) *WorkspaceService {
	return &WorkspaceService{repo: repo, orgs: orgs, rbac: rbac}
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
