package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// S6E-08 (IG-139): izin Editor untuk buat task, sub-task, dan lampiran.

// --- Buat task ---

type createCapture struct {
	*fakeTaskRepo
	assignees []string
	auditRole string
	sp        *int
}

func (c *createCapture) Create(_ context.Context, _ db.Executor, _ string, _, _ *string, _, _ string, _ json.RawMessage, _ string, _, _ *time.Time, _ *float64, sp *int, _ string, assignees []string, auditRole, _ string) (*repository.Task, error) {
	c.assignees, c.auditRole, c.sp = assignees, auditRole, sp
	return &repository.Task{ID: "new"}, nil
}

type createStatuses struct{ *fakeTaskStatuses }

func (createStatuses) GetBacklogStatus(_ context.Context, _ db.Executor, _, _ string) (*repository.CustomStatus, error) {
	return &repository.CustomStatus{ID: "backlog-status", Name: "BACKLOG"}, nil
}

type createProjects struct{ allowSP bool }

func (createProjects) GetWorkspaceID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return "ws1", nil
}
func (p createProjects) GetAllowEditorStoryPoints(_ context.Context, _ db.Executor, _ string) (bool, error) {
	return p.allowSP, nil
}

func newCreateSvc(role string, allowSP bool) (*TaskService, *createCapture) {
	repo := &createCapture{fakeTaskRepo: &fakeTaskRepo{}}
	svc := NewTaskService(repo, &fakeTaskPics{}, &fakeTaskDeps{}, &fakeTaskSessions{},
		createProjects{allowSP: allowSP}, createStatuses{&fakeTaskStatuses{}},
		&fakeSprintRBAC{role: role}, &fakeSprintProjectRoles{found: false}, &fakeTaskRules{}, &fakeTaskSprints{})
	return svc, repo
}

func TestTaskService_Create_EditorDefaultsAssigneeToSelf(t *testing.T) {
	svc, repo := newCreateSvc("editor", false)
	if _, err := svc.Create(context.Background(), nil, "p1", "Task dari Editor", nil, "medium", nil, nil, nil, nil, nil, nil, "u1", ""); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(repo.assignees) != 1 || repo.assignees[0] != "u1" {
		t.Fatalf("assignees = %v, want [u1]", repo.assignees)
	}
	if repo.auditRole != "editor" {
		t.Fatalf("auditRole = %q, want editor (role efektif, bukan role rute)", repo.auditRole)
	}
}

func TestTaskService_Create_EditorKeepsExplicitAssignees(t *testing.T) {
	svc, repo := newCreateSvc("editor", false)
	if _, err := svc.Create(context.Background(), nil, "p1", "Task dari Editor", nil, "medium", nil, nil, nil, nil, nil, []string{"u2"}, "u1", ""); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(repo.assignees) != 1 || repo.assignees[0] != "u2" {
		t.Fatalf("assignees = %v, want [u2]", repo.assignees)
	}
}

func TestTaskService_Create_NonEditorStillNeedsAssignee(t *testing.T) {
	svc, _ := newCreateSvc("project_manager", false)
	_, err := svc.Create(context.Background(), nil, "p1", "Task dari PM", nil, "medium", nil, nil, nil, nil, nil, nil, "u1", "")
	if !errors.Is(err, domain.ErrTaskAssigneeRequired) {
		t.Fatalf("err = %v, want ErrTaskAssigneeRequired", err)
	}
}

func TestTaskService_Create_ViewerForbidden(t *testing.T) {
	svc, _ := newCreateSvc("viewer", false)
	_, err := svc.Create(context.Background(), nil, "p1", "Task", nil, "medium", nil, nil, nil, nil, nil, []string{"u1"}, "u1", "")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestTaskService_Create_StoryPointGate(t *testing.T) {
	three := 3
	cases := []struct {
		name    string
		role    string
		allowSP bool
		sp      *int
		wantErr error
	}{
		{"editor tanpa izin project + SP", "editor", false, &three, domain.ErrStoryPointsNotAllowed},
		{"editor dengan izin project + SP", "editor", true, &three, nil},
		{"editor tanpa izin tanpa SP", "editor", false, nil, nil},
		{"PM tanpa setting + SP", "project_manager", false, &three, nil},
		{"AW tanpa setting + SP", "admin_workspace", false, &three, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newCreateSvc(tc.role, tc.allowSP)
			_, err := svc.Create(context.Background(), nil, "p1", "Task SP", nil, "medium", nil, nil, nil, tc.sp, nil, []string{"u1"}, "u1", "")
			if tc.wantErr == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// --- Sub-task ---

func TestCheckChecklistWrite_DecisionTable(t *testing.T) {
	cases := []struct {
		role    string
		owned   bool
		wantErr error
	}{
		{"", false, nil}, // PA/GA
		{"admin_workspace", false, nil},
		{"project_manager", false, nil},
		{"editor", true, nil},
		{"editor", false, domain.ErrNotYourTask},
		{"approver", true, domain.ErrForbidden},
		{"viewer", true, domain.ErrForbidden},
		{"division_viewer", true, domain.ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.role+map[bool]string{true: "/milik", false: "/bukan-milik"}[tc.owned], func(t *testing.T) {
			owners := &fakeTaskRepo{owners: map[string]bool{"t1": tc.owned}}
			err := checkChecklistWrite(context.Background(), nil, owners, tc.role, "t1", "u1")
			if tc.wantErr == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

type fakeChecklistRepo struct{ created, updated, deleted int }

func (f *fakeChecklistRepo) Create(_ context.Context, _ db.Executor, taskID, title, _ string) (*repository.TaskChecklistItem, error) {
	f.created++
	return &repository.TaskChecklistItem{ID: "i1", TaskID: taskID, Title: title}, nil
}
func (f *fakeChecklistRepo) ListForTask(_ context.Context, _ db.Executor, _ string) ([]repository.TaskChecklistItem, error) {
	return nil, nil
}
func (f *fakeChecklistRepo) GetTaskID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return "t1", nil
}
func (f *fakeChecklistRepo) Update(_ context.Context, _ db.Executor, _ string, _ *string, _ *bool) error {
	f.updated++
	return nil
}
func (f *fakeChecklistRepo) Delete(_ context.Context, _ db.Executor, _ string) error {
	f.deleted++
	return nil
}

func newChecklistSvc(role string, owned bool) (*TaskChecklistItemService, *fakeChecklistRepo) {
	repo := &fakeChecklistRepo{}
	svc := NewTaskChecklistItemService(repo, &fakeAttachmentTaskResolver{projectID: "p1", notOwner: !owned},
		&fakeAttachmentProjectResolver{workspaceID: "ws1"}, &fakeSprintRBAC{role: role}, &fakeSprintProjectRoles{found: false})
	return svc, repo
}

func TestChecklistService_WriteGate(t *testing.T) {
	ctx := context.Background()
	done := true
	cases := []struct {
		role      string
		owned     bool
		actorRole string
		wantErr   error
	}{
		{"editor", true, "", nil},
		{"editor", false, "", domain.ErrNotYourTask},
		{"viewer", true, "", domain.ErrForbidden},
		{"approver", true, "", domain.ErrForbidden},
		{"project_manager", false, "", nil},
		{"", false, "platform_admin", nil},
	}
	for _, tc := range cases {
		name := tc.role + "/" + tc.actorRole + map[bool]string{true: "/milik", false: "/bukan-milik"}[tc.owned]
		t.Run(name, func(t *testing.T) {
			svc, repo := newChecklistSvc(tc.role, tc.owned)
			_, errC := svc.Create(ctx, nil, "t1", "Sub-task", "u1", tc.actorRole)
			errU := svc.Update(ctx, nil, "i1", nil, &done, "u1", tc.actorRole)
			errD := svc.Delete(ctx, nil, "i1", "u1", tc.actorRole)
			for op, err := range map[string]error{"Create": errC, "Update": errU, "Delete": errD} {
				if tc.wantErr == nil && err != nil {
					t.Fatalf("%s err = %v, want nil", op, err)
				}
				if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
					t.Fatalf("%s err = %v, want %v", op, err, tc.wantErr)
				}
			}
			wrote := repo.created + repo.updated + repo.deleted
			if tc.wantErr != nil && wrote != 0 {
				t.Fatalf("repo ditulis %d kali padahal ditolak", wrote)
			}
			if tc.wantErr == nil && wrote != 3 {
				t.Fatalf("repo ditulis %d kali, want 3", wrote)
			}
		})
	}
}

// --- Lampiran ---

func TestUpload_EditorOnlyOnOwnTask(t *testing.T) {
	repo := &fakeAttachmentRepo{}
	svc := newTestAttachmentService(repo, "editor", nil, nil, nil)
	svc.tasks = &fakeAttachmentTaskResolver{projectID: "proj-1", notOwner: true}

	_, err := svc.Upload(context.Background(), nil, "task-1", "berkas.txt", []byte("data"), "user-1", "")
	if !errors.Is(err, domain.ErrNotYourTask) {
		t.Fatalf("err = %v, want ErrNotYourTask", err)
	}
	if repo.createCalls != 0 {
		t.Fatalf("createCalls = %d, want 0", repo.createCalls)
	}
}

func TestUpload_AuditUsesEffectiveRole(t *testing.T) {
	cases := []struct {
		name      string
		wsRole    string
		actorRole string
		want      string
	}{
		{"editor lewat rute tanpa role", "editor", "", "editor"},
		{"PM lewat rute tanpa role", "project_manager", "", "project_manager"},
		{"platform admin tetap role-nya", "", "platform_admin", "platform_admin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeAttachmentRepo{}
			svc := newTestAttachmentService(repo, tc.wsRole, nil, nil, nil)
			if _, err := svc.Upload(context.Background(), nil, "task-1", "berkas.txt", []byte("halo"), "user-1", tc.actorRole); err != nil {
				t.Fatalf("err = %v", err)
			}
			if repo.createRole != tc.want {
				t.Fatalf("createRole = %q, want %q", repo.createRole, tc.want)
			}
		})
	}
}
