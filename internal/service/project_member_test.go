package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeProjectMemberRepo struct {
	workspaceID    map[string]string
	getWsIDErr     error
	addErr         error
	updateErr      error
	removeErr      error
	listResult     []repository.ProjectMember
	crossOrgResult []repository.CrossOrgMembership
	revokeCount    int64
}

func (f *fakeProjectMemberRepo) GetWorkspaceID(_ context.Context, _ db.Executor, projectID string) (string, error) {
	if f.getWsIDErr != nil {
		return "", f.getWsIDErr
	}
	ws, ok := f.workspaceID[projectID]
	if !ok {
		return "", domain.ErrProjectNotFound
	}
	return ws, nil
}

func (f *fakeProjectMemberRepo) AddMember(_ context.Context, _ db.Executor, _, _, _, _ string, _ bool, _, _ string) error {
	return f.addErr
}

func (f *fakeProjectMemberRepo) UpdateRole(_ context.Context, _ db.Executor, _, _, _, _, _ string) error {
	return f.updateErr
}

func (f *fakeProjectMemberRepo) RemoveMember(_ context.Context, _ db.Executor, _, _, _, _ string) error {
	return f.removeErr
}

func (f *fakeProjectMemberRepo) ListMembers(_ context.Context, _ db.Executor, _ string) ([]repository.ProjectMember, error) {
	return f.listResult, nil
}

func (f *fakeProjectMemberRepo) ListCrossOrgMemberships(_ context.Context, _ db.Executor, _, _ string) ([]repository.CrossOrgMembership, error) {
	return f.crossOrgResult, nil
}

func (f *fakeProjectMemberRepo) RevokeAllScopedForUser(_ context.Context, _ db.Executor, _ string) (int64, error) {
	return f.revokeCount, nil
}

type fakeProjectRoleChecker struct {
	role    string
	roleErr error
	orgID   string
	orgErr  error
}

func (f *fakeProjectRoleChecker) GetMemberRole(_ context.Context, _ db.Executor, _, _ string) (string, error) {
	return f.role, f.roleErr
}

func (f *fakeProjectRoleChecker) GetWorkspaceOrgID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return f.orgID, f.orgErr
}

func TestProjectMemberService_AddMember_PlatformAdminBypass(t *testing.T) {
	repo := &fakeProjectMemberRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := NewProjectMemberService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: ""})

	err := svc.AddMember(context.Background(), nil, "proj-1", "user-1", "editor", "pa-1", "platform_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProjectMemberService_AddMember_WorkspacePM_Allowed(t *testing.T) {
	repo := &fakeProjectMemberRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := NewProjectMemberService(repo, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeProjectRoleChecker{role: "project_manager"})

	err := svc.AddMember(context.Background(), nil, "proj-1", "user-1", "editor", "pm-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProjectMemberService_AddMember_ViewerForbidden(t *testing.T) {
	repo := &fakeProjectMemberRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := NewProjectMemberService(repo, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeProjectRoleChecker{role: "viewer"})

	err := svc.AddMember(context.Background(), nil, "proj-1", "user-1", "editor", "viewer-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestProjectMemberService_AddMember_ProjectNotFound(t *testing.T) {
	repo := &fakeProjectMemberRepo{workspaceID: map[string]string{}}
	svc := NewProjectMemberService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{})

	err := svc.AddMember(context.Background(), nil, "proj-missing", "user-1", "editor", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrProjectNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrProjectNotFound", err)
	}
}

func TestProjectMemberService_AddMember_MissingFields(t *testing.T) {
	svc := NewProjectMemberService(&fakeProjectMemberRepo{}, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{})

	err := svc.AddMember(context.Background(), nil, "proj-1", "", "editor", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}

func TestProjectMemberService_UpdateMemberRole_AlreadyExists(t *testing.T) {
	repo := &fakeProjectMemberRepo{workspaceID: map[string]string{"proj-1": "ws-1"}, updateErr: domain.ErrProjectMemberNotFound}
	svc := NewProjectMemberService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{})

	err := svc.UpdateMemberRole(context.Background(), nil, "proj-1", "user-1", "viewer", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrProjectMemberNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrProjectMemberNotFound", err)
	}
}

func TestProjectMemberService_RemoveMember_Forbidden(t *testing.T) {
	repo := &fakeProjectMemberRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := NewProjectMemberService(repo, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeProjectRoleChecker{role: "editor"})

	err := svc.RemoveMember(context.Background(), nil, "proj-1", "user-1", "editor-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestProjectMemberService_ListMembers_Success(t *testing.T) {
	repo := &fakeProjectMemberRepo{listResult: []repository.ProjectMember{{UserID: "user-1"}}}
	svc := NewProjectMemberService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{})

	members, err := svc.ListMembers(context.Background(), nil, "proj-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(members) != 1 {
		t.Errorf("members = %+v, unexpected", members)
	}
}

func TestProjectMemberService_ListCrossOrgMemberships_Success(t *testing.T) {
	repo := &fakeProjectMemberRepo{crossOrgResult: []repository.CrossOrgMembership{{UserID: "user-1"}}}
	svc := NewProjectMemberService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{})

	list, err := svc.ListCrossOrgMemberships(context.Background(), nil, "group-1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("list = %+v, unexpected", list)
	}
}

func TestProjectMemberService_RevokeAllScopedForUser_Success(t *testing.T) {
	repo := &fakeProjectMemberRepo{revokeCount: 3}
	svc := NewProjectMemberService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{})

	count, err := svc.RevokeAllScopedForUser(context.Background(), nil, "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}
