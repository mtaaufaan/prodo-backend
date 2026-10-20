package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// Tabel keputusan hak akses Editor pada task (S6E-06, IG-137): Editor hanya boleh mengubah task
// miliknya (assignee atau PIC aktif); bulk ditolak untuk Editor; hapus hanya AW/PM; role lain tidak berubah.

func taskSvcWithRole(role string, owned bool) (*TaskService, *fakeTaskRepo) {
	sprintID := "sp1"
	repo := &fakeTaskRepo{
		byID: map[string]*repository.Task{
			"t1": {ID: "t1", ProjectID: "p1", StatusID: "backlog-status", StatusName: "BACKLOG", Title: "Judul task", SprintID: &sprintID, Position: 1},
			"t2": {ID: "t2", ProjectID: "p1", StatusID: "backlog-status", StatusName: "BACKLOG", Title: "Task lain", SprintID: &sprintID, Position: 2},
		},
		owners: map[string]bool{"t1": owned},
	}
	statuses := map[string]*repository.CustomStatus{
		"backlog-status": {ID: "backlog-status", Name: "BACKLOG"},
		"done-status":    {ID: "done-status", Name: "DONE"},
	}
	svc := NewTaskService(repo, &fakeTaskPics{}, &fakeTaskDeps{}, &fakeTaskSessions{},
		&fakeTaskProjects{workspaceID: "ws1"}, &fakeTaskStatuses{byID: statuses},
		&fakeSprintRBAC{role: role}, &fakeSprintProjectRoles{found: false}, &fakeTaskRules{}, &fakeTaskSprints{})
	return svc, repo
}

func TestTaskService_EditorOwnershipMatrix(t *testing.T) {
	ctx := context.Background()
	ops := map[string]func(*TaskService) error{
		"Update": func(s *TaskService) error {
			return s.Update(ctx, nil, "t1", "Judul baru", nil, "medium", nil, nil, nil, nil, nil, "u1", "")
		},
		"SetStatus": func(s *TaskService) error {
			return s.SetStatus(ctx, nil, "t1", "done-status", []string{"u1"}, "u1", "")
		},
		"SetCompleteness": func(s *TaskService) error {
			return s.SetCompleteness(ctx, nil, "t1", "complete", "u1", "")
		},
		"Reorder": func(s *TaskService) error { return s.Reorder(ctx, nil, "t1", "t2", true, "u1", "") },
	}
	cases := []struct {
		role    string
		owned   bool
		wantErr error
	}{
		{"editor", true, nil},
		{"editor", false, domain.ErrNotYourTask},
		{"project_manager", false, nil},
		{"admin_workspace", false, nil},
		{"approver", false, nil}, // ubah status/PIC/reorder oleh Approver menunggu endpoint approval (S7A-04/06); Update ditolak (S7A-02)
	}
	for name, op := range ops {
		for _, tc := range cases {
			t.Run(name+"/"+tc.role+map[bool]string{true: "/milik", false: "/bukan-milik"}[tc.owned], func(t *testing.T) {
				svc, _ := taskSvcWithRole(tc.role, tc.owned)
				err := op(svc)
				if name == "Update" && tc.role == "approver" {
					tc.wantErr = domain.ErrForbidden // S7A-02: Approver tidak mengubah field task
				}
				if tc.wantErr == nil && err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			})
		}
	}
}

func TestTaskService_EditorNotYourTask_NoWrite(t *testing.T) {
	svc, repo := taskSvcWithRole("editor", false)
	if err := svc.SetStatus(context.Background(), nil, "t1", "done-status", []string{"u1"}, "u1", ""); !errors.Is(err, domain.ErrNotYourTask) {
		t.Fatalf("err = %v", err)
	}
	if len(repo.setStatusCall) != 0 {
		t.Fatalf("status tidak boleh berubah: %v", repo.setStatusCall)
	}
	if err := svc.Reorder(context.Background(), nil, "t1", "t2", true, "u1", ""); !errors.Is(err, domain.ErrNotYourTask) || len(repo.setPositions) != 0 {
		t.Fatalf("reorder: err=%v posisi=%v", err, repo.setPositions)
	}
}

func TestTaskService_Bulk_EditorRejectedWholly(t *testing.T) {
	ctx := context.Background()
	svc, repo := taskSvcWithRole("editor", true) // bahkan task milik sendiri
	results, err := svc.BulkSetStatus(ctx, nil, "p1", []string{"t1"}, "done-status", []string{"u1"}, "u1", "")
	if !errors.Is(err, domain.ErrBulkNotAllowed) || results != nil {
		t.Fatalf("results=%v err=%v, want ErrBulkNotAllowed", results, err)
	}
	if len(repo.setStatusCall) != 0 {
		t.Fatal("tidak boleh ada task yang diproses")
	}
	for _, role := range []string{"project_manager", "admin_workspace"} {
		svc, _ := taskSvcWithRole(role, false)
		if res, err := svc.BulkSetStatus(ctx, nil, "p1", []string{"t1"}, "done-status", []string{"u1"}, "u1", ""); err != nil || len(res) != 1 || res[0].Err != nil {
			t.Fatalf("%s: res=%v err=%v", role, res, err)
		}
	}
}

func TestTaskService_Delete_RoleMatrix(t *testing.T) {
	cases := []struct {
		role string
		ok   bool
	}{
		{"admin_workspace", true},
		{"project_manager", true},
		{"editor", false},
		{"approver", false},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			svc, _ := taskSvcWithRole(tc.role, true)
			err := svc.Delete(context.Background(), nil, "t1", "u1", "")
			if tc.ok && err != nil {
				t.Fatalf("err = %v", err)
			}
			if !tc.ok && !errors.Is(err, domain.ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
		})
	}
}

// ---- TaskPicService: AddPic / HandoffPic ----

type fakePicOpsRepo struct {
	taskPicHistoryRepository
	added, handedOff int
}

func (f *fakePicOpsRepo) ListGroupForStatus(context.Context, db.Executor, string, string) ([]repository.PicGroupMember, error) {
	return nil, nil // grup kosong -> mode Bebas
}
func (f *fakePicOpsRepo) ListActiveForTask(context.Context, db.Executor, string) ([]repository.TaskPicPhase, error) {
	return []repository.TaskPicPhase{{UserID: "pic-lama", IsActive: true}}, nil
}
func (f *fakePicOpsRepo) AddPic(context.Context, db.Executor, string, string, string, string, string, string, string) error {
	f.added++
	return nil
}
func (f *fakePicOpsRepo) HandoffPic(context.Context, db.Executor, string, string, string, []string, string, string, string, string) error {
	f.handedOff++
	return nil
}

type fakePicOpsTasks struct{ owner bool }

func (f fakePicOpsTasks) Get(context.Context, db.Executor, string) (*repository.Task, error) {
	return &repository.Task{ID: "t1", ProjectID: "p1", StatusID: "st1", StatusName: "IN PROGRESS"}, nil
}
func (f fakePicOpsTasks) IsOwner(context.Context, db.Executor, string, string) (bool, error) {
	return f.owner, nil
}

func picSvcWithRole(role string, owner bool) (*TaskPicService, *fakePicOpsRepo) {
	repo := &fakePicOpsRepo{}
	return &TaskPicService{repo: repo, tasks: fakePicOpsTasks{owner: owner}, projects: fakePicGroupProjects{},
		rbac: fakePicGroupRBAC{role: role}, projectRoles: &fakeSprintProjectRoles{found: false}}, repo
}

func TestTaskPicService_EditorCannotBecomePicOfOthersTask(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		role  string
		owner bool
		ok    bool
	}{
		{"editor", true, true},
		{"editor", false, false}, // tanpa gerbang ini Editor menjadikan dirinya PIC lalu "memiliki" task orang lain
		{"project_manager", false, true},
		{"admin_workspace", false, true},
	} {
		svc, repo := picSvcWithRole(tc.role, tc.owner)
		errAdd := svc.AddPic(ctx, nil, "t1", "u-baru", "u1", "")
		svc2, repo2 := picSvcWithRole(tc.role, tc.owner)
		errHand := svc2.HandoffPic(ctx, nil, "t1", "", "u-baru", "u1", "")
		for name, err := range map[string]error{"AddPic": errAdd, "HandoffPic": errHand} {
			if tc.ok && err != nil {
				t.Fatalf("%s %s/owner=%v: err = %v", name, tc.role, tc.owner, err)
			}
			if !tc.ok && !errors.Is(err, domain.ErrNotYourTask) {
				t.Fatalf("%s %s/owner=%v: err = %v, want ErrNotYourTask", name, tc.role, tc.owner, err)
			}
		}
		if !tc.ok && (repo.added != 0 || repo2.handedOff != 0) {
			t.Fatalf("%s: tidak boleh menulis PIC (added=%d handedOff=%d)", tc.role, repo.added, repo2.handedOff)
		}
	}
}
