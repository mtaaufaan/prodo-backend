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
