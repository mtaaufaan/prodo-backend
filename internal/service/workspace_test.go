package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeWorkspaceRepo struct {
	created   *repository.Workspace
	createErr error

	orgID          map[string]string
	getOrgIDErr    error
	updateErr      error
	archiveErr     error
	unarchiveErr   error
	deactivateErr  error
	reactivateErr  error
	deleteErr      error
	moveErr        error
	listResult     []repository.Workspace
	listErr        error
	listByGroup    []repository.WorkspaceListRow
	listByGroupErr error
}

func (f *fakeWorkspaceRepo) Create(_ context.Context, _ db.Executor, orgID, name, _, _ string) (*repository.Workspace, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = &repository.Workspace{ID: "ws-new", OrgID: orgID, Name: name}
	return f.created, nil
}

func (f *fakeWorkspaceRepo) GetOrgID(_ context.Context, _ db.Executor, workspaceID string) (string, error) {
	if f.getOrgIDErr != nil {
		return "", f.getOrgIDErr
	}
	orgID, ok := f.orgID[workspaceID]
	if !ok {
		return "", domain.ErrWorkspaceNotFound
	}
	return orgID, nil
}

func (f *fakeWorkspaceRepo) Get(_ context.Context, _ db.Executor, workspaceID string) (*repository.Workspace, error) {
	orgID, ok := f.orgID[workspaceID]
	if !ok {
		return nil, domain.ErrWorkspaceNotFound
	}
	return &repository.Workspace{ID: workspaceID, OrgID: orgID}, nil
}

func (f *fakeWorkspaceRepo) Update(_ context.Context, _ db.Executor, _, _, _, _ string) error {
	return f.updateErr
}

func (f *fakeWorkspaceRepo) Archive(_ context.Context, _ db.Executor, _, _, _ string) error {
	return f.archiveErr
}

func (f *fakeWorkspaceRepo) Unarchive(_ context.Context, _ db.Executor, _, _, _ string) error {
	return f.unarchiveErr
}

func (f *fakeWorkspaceRepo) Deactivate(_ context.Context, _ db.Executor, _, _, _ string) error {
	return f.deactivateErr
}

func (f *fakeWorkspaceRepo) Reactivate(_ context.Context, _ db.Executor, _, _, _ string) error {
	return f.reactivateErr
}

func (f *fakeWorkspaceRepo) Delete(_ context.Context, _ db.Executor, _, _, _ string) error {
	return f.deleteErr
}

func (f *fakeWorkspaceRepo) List(_ context.Context, _ db.Executor, _ string) ([]repository.Workspace, error) {
	return f.listResult, f.listErr
}

func (f *fakeWorkspaceRepo) MoveToOrg(_ context.Context, _ db.Executor, _, _, _, _ string) error {
	return f.moveErr
}

func (f *fakeWorkspaceRepo) ListByGroup(_ context.Context, _ db.Executor, _ string) ([]repository.WorkspaceListRow, error) {
	return f.listByGroup, f.listByGroupErr
}

type fakeOrgAuthorizer struct {
	err            error
	isActiveResult bool
	isActiveErr    error
	isGA           bool
	isGAErr        error
}

func (f *fakeOrgAuthorizer) AuthorizeOrgAccess(_ context.Context, _ db.Executor, _, _, _ string) error {
	return f.err
}

func (f *fakeOrgAuthorizer) IsActive(_ context.Context, _ db.Executor, _ string) (bool, error) {
	return f.isActiveResult, f.isActiveErr
}

func (f *fakeOrgAuthorizer) IsGroupAdminOfGroup(_ context.Context, _ db.Executor, _, _ string) (bool, error) {
	return f.isGA, f.isGAErr
}

type fakeRoleAssigner struct {
	err        error
	members    []repository.Member
	candidates []repository.Member
}

func (f *fakeRoleAssigner) AssignRole(_ context.Context, _ db.Executor, _, _, _ string, _ *string, _, _ string) (*RoleChangeResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &RoleChangeResult{NewRole: "admin_workspace"}, nil
}

func (f *fakeRoleAssigner) ListMembers(_ context.Context, _ db.Executor, _ string) ([]repository.Member, error) {
	return f.members, nil
}

func (f *fakeRoleAssigner) ListOrgCandidates(_ context.Context, _ db.Executor, _ string) ([]repository.Member, error) {
	return f.candidates, nil
}

type fakeContactLookup struct {
	emailToUserID map[string]string
}

func (f *fakeContactLookup) FindUserContactByID(_ context.Context, userID string) (*repository.UserContact, error) {
	return &repository.UserContact{Email: userID + "@prodo.local", DisplayName: userID}, nil
}

func (f *fakeContactLookup) FindUserIDByEmail(_ context.Context, email string) (string, error) {
	if id, ok := f.emailToUserID[email]; ok {
		return id, nil
	}
	return "", pgx.ErrNoRows
}

type fakeAdminChangeNotifier struct{}

func (f *fakeAdminChangeNotifier) SendWorkspaceAdminChangedEmail(_ context.Context, _, _, _ string, _ bool) error {
	return nil
}

type fakeInvitationCreator struct {
	err error
}

func (f *fakeInvitationCreator) CreateInvitation(_ context.Context, _ db.Executor, email, workspaceID, role, _, _, _ string) (*Invitation, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &Invitation{Email: email, WorkspaceID: workspaceID, Role: role}, nil
}

func newTestWorkspaceService(repo workspaceRepository, orgs orgAuthorizer, rbac roleAssigner) *WorkspaceService {
	return NewWorkspaceService(repo, orgs, rbac, &fakeContactLookup{}, &fakeAdminChangeNotifier{}, &fakeInvitationCreator{}, zap.NewNop())
}

func TestWorkspaceService_CreateWorkspace_Success(t *testing.T) {
	svc := newTestWorkspaceService(&fakeWorkspaceRepo{}, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	ws, err := svc.CreateWorkspace(context.Background(), nil, "org-1", "Engineering", "aw-1", "", "", "ga-1", "group_admin", "GA Satu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws.ID != "ws-new" || ws.OrgID != "org-1" {
		t.Errorf("ws = %+v, unexpected", ws)
	}
}

func TestWorkspaceService_CreateWorkspace_InviteNewEmail(t *testing.T) {
	svc := newTestWorkspaceService(&fakeWorkspaceRepo{}, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	ws, err := svc.CreateWorkspace(context.Background(), nil, "org-1", "Engineering", "", "baru@acme.co.id", "Admin Baru", "ga-1", "group_admin", "GA Satu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws.ID != "ws-new" {
		t.Errorf("ws = %+v, unexpected", ws)
	}
}

func TestWorkspaceService_CreateWorkspace_InviteEmailMissingName(t *testing.T) {
	svc := newTestWorkspaceService(&fakeWorkspaceRepo{}, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	_, err := svc.CreateWorkspace(context.Background(), nil, "org-1", "Engineering", "", "baru@acme.co.id", "", "ga-1", "group_admin", "GA Satu")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}

func TestWorkspaceService_CreateWorkspace_InviteExistingEmailAssignsDirectly(t *testing.T) {
	repo := &fakeWorkspaceRepo{}
	svc := NewWorkspaceService(repo, &fakeOrgAuthorizer{}, &fakeRoleAssigner{}, &fakeContactLookup{emailToUserID: map[string]string{"ada@acme.co.id": "user-existing"}}, &fakeAdminChangeNotifier{}, &fakeInvitationCreator{}, zap.NewNop())

	ws, err := svc.CreateWorkspace(context.Background(), nil, "org-1", "Engineering", "", "ada@acme.co.id", "", "ga-1", "group_admin", "GA Satu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws.ID != "ws-new" {
		t.Errorf("ws = %+v, unexpected", ws)
	}
}

func TestWorkspaceService_CreateWorkspace_Forbidden(t *testing.T) {
	svc := newTestWorkspaceService(&fakeWorkspaceRepo{}, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeRoleAssigner{})

	_, err := svc.CreateWorkspace(context.Background(), nil, "org-1", "Engineering", "aw-1", "", "", "ga-2", "group_admin", "GA Dua")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestWorkspaceService_CreateWorkspace_MissingFields(t *testing.T) {
	svc := newTestWorkspaceService(&fakeWorkspaceRepo{}, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	_, err := svc.CreateWorkspace(context.Background(), nil, "org-1", "", "aw-1", "", "", "pa-1", "platform_admin", "PA Satu")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}

func TestWorkspaceService_UpdateWorkspace_Success(t *testing.T) {
	svc := newTestWorkspaceService(&fakeWorkspaceRepo{}, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	if err := svc.UpdateWorkspace(context.Background(), nil, "ws-1", "Engineering Baru", "aw-1", "admin_workspace"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkspaceService_UpdateWorkspace_MissingFields(t *testing.T) {
	svc := newTestWorkspaceService(&fakeWorkspaceRepo{}, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	err := svc.UpdateWorkspace(context.Background(), nil, "ws-1", "", "aw-1", "admin_workspace")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}

func TestWorkspaceService_DeactivateWorkspace_NotFound(t *testing.T) {
	svc := newTestWorkspaceService(&fakeWorkspaceRepo{deactivateErr: domain.ErrWorkspaceNotFound}, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	err := svc.DeactivateWorkspace(context.Background(), nil, "ws-missing", "aw-1", "admin_workspace")
	if !errors.Is(err, domain.ErrWorkspaceNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrWorkspaceNotFound", err)
	}
}

func TestWorkspaceService_ReactivateWorkspace_Success(t *testing.T) {
	svc := newTestWorkspaceService(&fakeWorkspaceRepo{}, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	if err := svc.ReactivateWorkspace(context.Background(), nil, "ws-1", "pa-1", "platform_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkspaceService_DeleteWorkspace_ResolvesOrgAndAuthorizes(t *testing.T) {
	repo := &fakeWorkspaceRepo{orgID: map[string]string{"ws-1": "org-1"}}
	svc := newTestWorkspaceService(repo, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	if err := svc.DeleteWorkspace(context.Background(), nil, "ws-1", "pa-1", "platform_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkspaceService_DeleteWorkspace_GroupAdminNotAssigned_Forbidden(t *testing.T) {
	repo := &fakeWorkspaceRepo{orgID: map[string]string{"ws-1": "org-1"}}
	svc := newTestWorkspaceService(repo, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeRoleAssigner{})

	err := svc.DeleteWorkspace(context.Background(), nil, "ws-1", "ga-2", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestWorkspaceService_DeleteWorkspace_WorkspaceNotFound(t *testing.T) {
	svc := newTestWorkspaceService(&fakeWorkspaceRepo{}, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	err := svc.DeleteWorkspace(context.Background(), nil, "ws-missing", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrWorkspaceNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrWorkspaceNotFound", err)
	}
}

func TestWorkspaceService_DeleteWorkspace_HasActiveProjects(t *testing.T) {
	repo := &fakeWorkspaceRepo{orgID: map[string]string{"ws-1": "org-1"}, deleteErr: domain.ErrWorkspaceHasProjects}
	svc := newTestWorkspaceService(repo, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	err := svc.DeleteWorkspace(context.Background(), nil, "ws-1", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrWorkspaceHasProjects) {
		t.Errorf("err = %v, want wrapped domain.ErrWorkspaceHasProjects", err)
	}
}

func TestWorkspaceService_MoveWorkspace_Success(t *testing.T) {
	repo := &fakeWorkspaceRepo{orgID: map[string]string{"ws-1": "org-source"}}
	svc := newTestWorkspaceService(repo, &fakeOrgAuthorizer{isActiveResult: true}, &fakeRoleAssigner{})

	if err := svc.MoveWorkspace(context.Background(), nil, "ws-1", "org-target", "pa-1", "platform_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkspaceService_MoveWorkspace_TargetInactive(t *testing.T) {
	repo := &fakeWorkspaceRepo{orgID: map[string]string{"ws-1": "org-source"}}
	svc := newTestWorkspaceService(repo, &fakeOrgAuthorizer{isActiveResult: false}, &fakeRoleAssigner{})

	err := svc.MoveWorkspace(context.Background(), nil, "ws-1", "org-target", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrOrganizationInactive) {
		t.Errorf("err = %v, want wrapped domain.ErrOrganizationInactive", err)
	}
}

func TestWorkspaceService_MoveWorkspace_Forbidden(t *testing.T) {
	repo := &fakeWorkspaceRepo{orgID: map[string]string{"ws-1": "org-source"}}
	svc := newTestWorkspaceService(repo, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeRoleAssigner{})

	err := svc.MoveWorkspace(context.Background(), nil, "ws-1", "org-target", "ga-2", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestWorkspaceService_ReassignAdmin_DemotesOldAdminAssignsNew(t *testing.T) {
	repo := &fakeWorkspaceRepo{orgID: map[string]string{"ws-1": "org-1"}}
	assigner := &fakeRoleAssigner{members: []repository.Member{
		{UserID: "old-admin", Role: "admin_workspace"},
		{UserID: "other-member", Role: "editor"},
	}}
	svc := newTestWorkspaceService(repo, &fakeOrgAuthorizer{}, assigner)

	if err := svc.ReassignAdmin(context.Background(), nil, "ws-1", "new-admin", "pa-1", "platform_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkspaceService_ReassignAdmin_MissingFields(t *testing.T) {
	svc := newTestWorkspaceService(&fakeWorkspaceRepo{}, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	err := svc.ReassignAdmin(context.Background(), nil, "ws-1", "", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}

func TestWorkspaceService_ListWorkspaces_Success(t *testing.T) {
	repo := &fakeWorkspaceRepo{listResult: []repository.Workspace{{ID: "ws-1", Name: "Engineering"}}}
	svc := newTestWorkspaceService(repo, &fakeOrgAuthorizer{}, &fakeRoleAssigner{})

	list, err := svc.ListWorkspaces(context.Background(), nil, "org-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list) != 1 || list[0].ID != "ws-1" {
		t.Errorf("list = %+v, unexpected", list)
	}
}
