package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeWorkspaceRepo struct {
	created   *repository.Workspace
	createErr error
}

func (f *fakeWorkspaceRepo) Create(_ context.Context, _ db.Executor, orgID, name, _, _ string) (*repository.Workspace, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = &repository.Workspace{ID: "ws-new", OrgID: orgID, Name: name}
	return f.created, nil
}

type fakeOrgAuthorizer struct {
	err error
}

func (f *fakeOrgAuthorizer) AuthorizeOrgAccess(_ context.Context, _ db.Executor, _, _, _ string) error {
	return f.err
}

type fakeRoleAssigner struct {
	err error
}

func (f *fakeRoleAssigner) AssignRole(_ context.Context, _ db.Executor, _, _, _ string, _ *string, _, _ string) (*RoleChangeResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &RoleChangeResult{NewRole: "admin_workspace"}, nil
}

func TestWorkspaceService_CreateWorkspace_Success(t *testing.T) {
	svc := NewWorkspaceService(&fakeWorkspaceRepo{}, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	ws, err := svc.CreateWorkspace(context.Background(), nil, "org-1", "Engineering", "aw-1", "ga-1", "group_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws.ID != "ws-new" || ws.OrgID != "org-1" {
		t.Errorf("ws = %+v, unexpected", ws)
	}
}

func TestWorkspaceService_CreateWorkspace_Forbidden(t *testing.T) {
	svc := NewWorkspaceService(&fakeWorkspaceRepo{}, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeRoleAssigner{})

	_, err := svc.CreateWorkspace(context.Background(), nil, "org-1", "Engineering", "aw-1", "ga-2", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestWorkspaceService_CreateWorkspace_MissingFields(t *testing.T) {
	svc := NewWorkspaceService(&fakeWorkspaceRepo{}, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	_, err := svc.CreateWorkspace(context.Background(), nil, "org-1", "", "aw-1", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}
