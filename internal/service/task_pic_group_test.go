package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// Fake dengan interface ter-embed (nil) -- hanya method yang dipakai
// ReplaceGroup yang diimplementasi.
type fakePicGroupRepo struct {
	taskPicHistoryRepository
	gotUserIDs []string
	called     bool
}

func (f *fakePicGroupRepo) ReplaceGroup(_ context.Context, _ db.Executor, _, _ string, userIDs []string, _, _, _ string) ([]repository.PicGroupMember, error) {
	f.called = true
	f.gotUserIDs = userIDs
	return nil, nil
}

type fakePicGroupProjects struct{ taskProjectResolver }

func (fakePicGroupProjects) GetWorkspaceID(context.Context, db.Executor, string) (string, error) {
	return "ws1", nil
}

type fakePicGroupRBAC struct{ role string }

func (f fakePicGroupRBAC) GetMemberRole(context.Context, db.Executor, string, string) (string, error) {
	return f.role, nil
}

type fakePicGroupStatuses struct{ status *repository.CustomStatus }

func (f fakePicGroupStatuses) Get(context.Context, db.Executor, string) (*repository.CustomStatus, error) {
	if f.status == nil {
		return nil, errors.New("not found")
	}
	return f.status, nil
}

type fakePicGroupMembers struct {
	list  []repository.ProjectMember
	asked bool
}

func (f *fakePicGroupMembers) ListAssignableMembers(context.Context, db.Executor, string) ([]repository.ProjectMember, error) {
	f.asked = true
	return f.list, nil
}

func newPicGroupSvc(role string, status *repository.CustomStatus, members []repository.ProjectMember) (*TaskPicService, *fakePicGroupRepo, *fakePicGroupMembers) {
	repo := &fakePicGroupRepo{}
	m := &fakePicGroupMembers{list: members}
	svc := &TaskPicService{repo: repo, projects: fakePicGroupProjects{}, rbac: fakePicGroupRBAC{role: role}, statuses: fakePicGroupStatuses{status: status}, members: m}
	return svc, repo, m
}

var picGroupProjectStatus = &repository.CustomStatus{ID: "st1", ScopeType: "project", ScopeID: "p1", Name: "IN PROGRESS"}

func TestReplaceGroup_Forbidden(t *testing.T) {
	// projectRoles nil -> authorizePicGroupManage jatuh ke rbac workspace.
	svc, repo, _ := newPicGroupSvc("editor", picGroupProjectStatus, nil)
	_, err := svc.ReplaceGroup(context.Background(), nil, "p1", "st1", nil, "u1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if repo.called {
		t.Error("repo tidak boleh dipanggil untuk aktor non-PM/AW")
	}
}

func TestReplaceGroup_StatusFromOtherProject(t *testing.T) {
	other := &repository.CustomStatus{ID: "st1", ScopeType: "project", ScopeID: "p-lain"}
	svc, repo, _ := newPicGroupSvc("project_manager", other, nil)
	_, err := svc.ReplaceGroup(context.Background(), nil, "p1", "st1", nil, "u1", "member")
	if !errors.Is(err, domain.ErrInvalidInput) || repo.called {
		t.Fatalf("err = %v called=%v, want ErrInvalidInput tanpa menulis", err, repo.called)
	}
	// status template workspace (bukan scope project) juga ditolak
	ws := &repository.CustomStatus{ID: "st1", ScopeType: "workspace", ScopeID: "ws1"}
	svc, _, _ = newPicGroupSvc("project_manager", ws, nil)
	if _, err := svc.ReplaceGroup(context.Background(), nil, "p1", "st1", nil, "u1", "member"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("status workspace: err = %v, want ErrInvalidInput", err)
	}
}

func TestReplaceGroup_IneligibleMembers(t *testing.T) {
	members := []repository.ProjectMember{
		{UserID: "pm", Role: "project_manager"},
		{UserID: "ed", Role: "editor"},
		{UserID: "vw", Role: "viewer"},
		{UserID: "aw", Role: "admin_workspace"},
	}
	for _, bad := range []string{"vw", "aw", "bukan-member"} {
		svc, repo, _ := newPicGroupSvc("project_manager", picGroupProjectStatus, members)
		_, err := svc.ReplaceGroup(context.Background(), nil, "p1", "st1", []string{"ed", bad}, "u1", "member")
		if !errors.Is(err, domain.ErrPicGroupIneligibleMember) {
			t.Errorf("%s: err = %v, want ErrPicGroupIneligibleMember", bad, err)
		}
		if repo.called {
			t.Errorf("%s: repo tidak boleh menulis kalau ada anggota tidak valid", bad)
		}
	}
}

func TestReplaceGroup_ValidDedupes(t *testing.T) {
	members := []repository.ProjectMember{{UserID: "pm", Role: "project_manager"}, {UserID: "ed", Role: "editor"}, {UserID: "ap", Role: "approver"}}
	svc, repo, _ := newPicGroupSvc("project_manager", picGroupProjectStatus, members)
	if _, err := svc.ReplaceGroup(context.Background(), nil, "p1", "st1", []string{"ed", "ap", "ed", "pm"}, "u1", "member"); err != nil {
		t.Fatal(err)
	}
	if len(repo.gotUserIDs) != 3 || repo.gotUserIDs[0] != "ed" || repo.gotUserIDs[1] != "ap" || repo.gotUserIDs[2] != "pm" {
		t.Errorf("user_ids = %v, want [ed ap pm] (duplikat dibuang, urutan awal dipertahankan)", repo.gotUserIDs)
	}
}

func TestReplaceGroup_EmptyClearsWithoutMemberLookup(t *testing.T) {
	svc, repo, members := newPicGroupSvc("admin_workspace", picGroupProjectStatus, nil)
	if _, err := svc.ReplaceGroup(context.Background(), nil, "p1", "st1", nil, "u1", "member"); err != nil {
		t.Fatal(err)
	}
	if !repo.called || len(repo.gotUserIDs) != 0 {
		t.Errorf("called=%v ids=%v, want panggilan dengan daftar kosong (KOSONGKAN)", repo.called, repo.gotUserIDs)
	}
	if members.asked {
		t.Error("daftar kosong tidak perlu memuat member project")
	}
}
