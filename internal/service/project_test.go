package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeProjectRepo struct {
	workspaceID map[string]string
	getWsIDErr  error
	createErr   error
	listResult  []repository.Project
	updateErr   error
	archiveErr  error
	deleteErr   error
	restoreErr  error
}

func (f *fakeProjectRepo) GetWorkspaceID(_ context.Context, _ db.Executor, projectID string) (string, error) {
	if f.getWsIDErr != nil {
		return "", f.getWsIDErr
	}
	ws, ok := f.workspaceID[projectID]
	if !ok {
		return "", domain.ErrProjectNotFound
	}
	return ws, nil
}

func (f *fakeProjectRepo) Create(_ context.Context, _ db.Executor, workspaceID, name, code, pmUserID, _, _ string) (*repository.Project, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &repository.Project{WorkspaceID: workspaceID, Name: name, Code: code, PMUserID: &pmUserID}, nil
}

func (f *fakeProjectRepo) List(_ context.Context, _ db.Executor, _ string) ([]repository.Project, error) {
	return f.listResult, nil
}

func (f *fakeProjectRepo) Update(_ context.Context, _ db.Executor, _, _, _, _, _ string) error {
	return f.updateErr
}

func (f *fakeProjectRepo) SetArchived(_ context.Context, _ db.Executor, _ string, _ bool, _, _ string) error {
	return f.archiveErr
}

func (f *fakeProjectRepo) SoftDelete(_ context.Context, _ db.Executor, _, _, _ string) error {
	return f.deleteErr
}

func (f *fakeProjectRepo) Restore(_ context.Context, _ db.Executor, _, _, _ string) error {
	return f.restoreErr
}

func TestProjectService_Create_PlatformAdminBypass(t *testing.T) {
	repo := &fakeProjectRepo{}
	svc := NewProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: "project_manager"})

	p, err := svc.Create(context.Background(), nil, "ws-1", "Rilis Q4", "ril", "pm-1", "pa-1", "platform_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Code != "RIL" {
		t.Fatalf("expected code uppercased to RIL, got %q", p.Code)
	}
}

func TestProjectService_Create_RejectsMissingFields(t *testing.T) {
	repo := &fakeProjectRepo{}
	svc := NewProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: "project_manager"})

	cases := []struct {
		name, code, pm string
	}{
		{"", "RIL", "pm-1"},
		{"Rilis Q4", "", "pm-1"},
		{"Rilis Q4", "RIL", ""},
		{"Rilis Q4", "R1L", "pm-1"}, // kode harus huruf saja
		{"Rilis Q4", "R", "pm-1"},   // kode minimal 2 huruf
	}
	for _, c := range cases {
		_, err := svc.Create(context.Background(), nil, "ws-1", c.name, c.code, c.pm, "aw-1", "member")
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("Create(%q,%q,%q): expected ErrInvalidInput, got %v", c.name, c.code, c.pm, err)
		}
	}
}

func TestProjectService_Create_RejectsPMNotProjectManagerRole(t *testing.T) {
	repo := &fakeProjectRepo{}
	svc := NewProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: "editor"})

	_, err := svc.Create(context.Background(), nil, "ws-1", "Rilis Q4", "RIL", "user-1", "aw-1", "member")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput saat pm bukan project_manager, got %v", err)
	}
}

func TestProjectService_Update_ForbiddenForNonAWPM(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := NewProjectService(repo, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeProjectRoleChecker{role: "viewer"})

	err := svc.Update(context.Background(), nil, "proj-1", "Nama Baru", "", "viewer-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestProjectService_Delete_AllowedForWorkspacePM(t *testing.T) {
	// Soft-delete (bukan hard-delete) sengaja mengizinkan AW/PM, bukan
	// cuma GA/PA -- lihat komentar ProjectRepository.SoftDelete.
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := NewProjectService(repo, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeProjectRoleChecker{role: "project_manager"})

	if err := svc.Delete(context.Background(), nil, "proj-1", "pm-1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProjectService_Restore_RejectsWorkspacePM(t *testing.T) {
	// Restore sengaja LEBIH ketat dari Delete: cuma GA/PA, AW/PM yang
	// boleh menghapus TIDAK otomatis boleh memulihkan.
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := NewProjectService(repo, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeProjectRoleChecker{role: "project_manager"})

	err := svc.Restore(context.Background(), nil, "proj-1", "pm-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden untuk AW/PM merestore, got %v", err)
	}
}

func TestProjectService_Restore_AllowedForGroupAdmin(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := NewProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: ""})

	if err := svc.Restore(context.Background(), nil, "proj-1", "ga-1", "group_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
