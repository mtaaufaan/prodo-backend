package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// projectMemberRepository -- interface didefinisikan di consumer, §3.9.
type projectMemberRepository interface {
	GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error)
	AddMember(ctx context.Context, exec db.Executor, projectID, workspaceID, userID, role string, isScoped bool, addedBy, actorRole string) error
	UpdateRole(ctx context.Context, exec db.Executor, projectID, userID, role, actorID, actorRole string) error
	RemoveMember(ctx context.Context, exec db.Executor, projectID, userID, actorID, actorRole string) error
	ListMembers(ctx context.Context, exec db.Executor, projectID string) ([]repository.ProjectMember, error)
	ListCrossOrgMemberships(ctx context.Context, exec db.Executor, groupID, orgIDFilter string) ([]repository.CrossOrgMembership, error)
	RevokeAllScopedForUser(ctx context.Context, exec db.Executor, userID string) (int64, error)
}

// projectRoleChecker -- interface didefinisikan di consumer, §3.9.
// Diimplementasikan *RBACService (GetMemberRole, GetWorkspaceOrgID).
type projectRoleChecker interface {
	GetMemberRole(ctx context.Context, exec db.Executor, workspaceID, userID string) (string, error)
	GetWorkspaceOrgID(ctx context.Context, exec db.Executor, workspaceID string) (string, error)
}

// ProjectMemberService -- S3-21/22/23/25/26/27, US-009b. Route
// /projects/:id/members TIDAK punya middleware RequireRole (target
// scope-nya projectID, bukan :wsId) -- otorisasi (Platform Admin, Group
// Admin pengelola org, atau Admin Workspace/Project Manager di workspace
// project ini) PENUH dicek di sini, sama pola GroupService (S3-20).
type ProjectMemberService struct {
	repo projectMemberRepository
	orgs orgAuthorizer
	rbac projectRoleChecker
}

func NewProjectMemberService(repo projectMemberRepository, orgs orgAuthorizer, rbac projectRoleChecker) *ProjectMemberService {
	return &ProjectMemberService{repo: repo, orgs: orgs, rbac: rbac}
}

// authorize menolak actor yang bukan PA/GA-of-org/AW/PM di workspace
// pemilik projectID -- mengembalikan workspaceID supaya caller tidak perlu
// query ulang.
func (s *ProjectMemberService) authorize(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) (string, error) {
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

// AddMember menambahkan project member (S3-21). isScoped otomatis
// ditentukan dari status keanggotaan workspace target -- lihat
// DATABASE_SCHEMA.md §5.13.
func (s *ProjectMemberService) AddMember(ctx context.Context, exec db.Executor, projectID, targetUserID, role, actorID, actorRole string) error {
	if projectID == "" || targetUserID == "" || role == "" {
		return fmt.Errorf("service.AddMember: %w", domain.ErrInvalidInput)
	}
	workspaceID, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return err
	}

	existingRole, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, targetUserID)
	if err != nil {
		return fmt.Errorf("service.AddMember: %w", err)
	}
	isScoped := existingRole == ""

	if err := s.repo.AddMember(ctx, exec, projectID, workspaceID, targetUserID, role, isScoped, actorID, actorRole); err != nil {
		return fmt.Errorf("service.AddMember: %w", err)
	}
	return nil
}

// UpdateMemberRole mengubah role project member existing (S3-22).
func (s *ProjectMemberService) UpdateMemberRole(ctx context.Context, exec db.Executor, projectID, targetUserID, role, actorID, actorRole string) error {
	if projectID == "" || targetUserID == "" || role == "" {
		return fmt.Errorf("service.UpdateMemberRole: %w", domain.ErrInvalidInput)
	}
	if _, err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.UpdateRole(ctx, exec, projectID, targetUserID, role, actorID, actorRole); err != nil {
		return fmt.Errorf("service.UpdateMemberRole: %w", err)
	}
	return nil
}

// RemoveMember mengeluarkan member dari project (S3-23) -- TIDAK
// menyentuh workspace_members.
func (s *ProjectMemberService) RemoveMember(ctx context.Context, exec db.Executor, projectID, targetUserID, actorID, actorRole string) error {
	if projectID == "" || targetUserID == "" {
		return fmt.Errorf("service.RemoveMember: %w", domain.ErrInvalidInput)
	}
	if _, err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.RemoveMember(ctx, exec, projectID, targetUserID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.RemoveMember: %w", err)
	}
	return nil
}

// ListMembers mengembalikan seluruh project member (S3-24 prasyarat FE).
// TIDAK ada pengecekan otorisasi tambahan -- scoping sepenuhnya lewat RLS
// pm_select, sama pola OrganizationService.ListOrganizations.
func (s *ProjectMemberService) ListMembers(ctx context.Context, exec db.Executor, projectID string) ([]repository.ProjectMember, error) {
	if projectID == "" {
		return nil, fmt.Errorf("service.ListMembers: %w", domain.ErrInvalidInput)
	}
	members, err := s.repo.ListMembers(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.ListMembers: %w", err)
	}
	return members, nil
}

// ListCrossOrgMemberships mengembalikan project-scoped member lintas org
// dalam satu grup (S3-25/27). TIDAK ada pengecekan otorisasi tambahan --
// GA sudah punya visibility penuh lewat RLS pm_select
// (prodo_is_group_admin_of_project), route sendiri sudah digerbangi
// requireOrgAdmin (platform_admin/group_admin) di routing.
func (s *ProjectMemberService) ListCrossOrgMemberships(ctx context.Context, exec db.Executor, groupID, orgIDFilter string) ([]repository.CrossOrgMembership, error) {
	if groupID == "" {
		return nil, fmt.Errorf("service.ListCrossOrgMemberships: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.ListCrossOrgMemberships(ctx, exec, groupID, orgIDFilter)
	if err != nil {
		return nil, fmt.Errorf("service.ListCrossOrgMemberships: %w", err)
	}
	return list, nil
}

// RevokeAllScopedForUser mencabut seluruh keanggotaan project-scoped milik
// userID (S3-26) -- BELUM DIPASANG ke endpoint manapun, lihat catatan
// ProjectMemberRepository.RevokeAllScopedForUser dan
// implementation_gaps.md IG-18. Disediakan siap pakai untuk endpoint
// "nonaktifkan akun user" begitu fitur itu dibangun.
func (s *ProjectMemberService) RevokeAllScopedForUser(ctx context.Context, exec db.Executor, userID string) (int64, error) {
	if userID == "" {
		return 0, fmt.Errorf("service.RevokeAllScopedForUser: %w", domain.ErrInvalidInput)
	}
	count, err := s.repo.RevokeAllScopedForUser(ctx, exec, userID)
	if err != nil {
		return 0, fmt.Errorf("service.RevokeAllScopedForUser: %w", err)
	}
	return count, nil
}
