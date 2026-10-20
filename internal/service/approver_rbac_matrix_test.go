package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// Tabel keputusan hak akses Approver (S7A-02, IG-148; rbac_matrix.xlsx No 8, 9, 10a, 13):
// Approver tidak membuat task, tidak mengubah field task (termasuk Story Point), tidak bulk,
// dan tidak mengelola dependency. Ubah status/PIC oleh Approver menunggu endpoint approval (S7A-04/06).

func TestRequireTaskWriter(t *testing.T) {
	for role, wantErr := range map[string]bool{
		"approver": true, "": false, "platform_admin": false, "group_admin": false,
		"admin_workspace": false, "project_manager": false, "editor": false,
	} {
		err := requireTaskWriter(role)
		if wantErr && !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("requireTaskWriter(%q) = %v, want ErrForbidden", role, err)
		}
		if !wantErr && err != nil {
			t.Errorf("requireTaskWriter(%q) = %v, want nil", role, err)
		}
	}
}

func TestTaskService_Approver_CannotCreateOrUpdate(t *testing.T) {
	ctx := context.Background()
	svc, repo := newCreateSvc("approver", true)
	sp := 3
	if _, err := svc.Create(ctx, nil, "p1", "Task dari Approver", nil, "medium", nil, nil, nil, &sp, nil, []string{"u2"}, "u1", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Create err = %v, want ErrForbidden", err)
	}
	if repo.auditRole != "" {
		t.Fatalf("tidak boleh ada task yang dibuat, auditRole = %q", repo.auditRole)
	}

	upd, _ := taskSvcWithRole("approver", true)
	if err := upd.Update(ctx, nil, "t1", "Judul baru", nil, "medium", nil, nil, nil, nil, nil, "u1", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Update err = %v, want ErrForbidden", err)
	}
	// Story Point juga lewat Update -- tetap ditolak sebelum aturan Story Point.
	if err := upd.Update(ctx, nil, "t1", "Judul task", nil, "medium", nil, nil, nil, &sp, nil, "u1", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Update story point err = %v, want ErrForbidden", err)
	}
}

func TestTaskService_Bulk_ApproverRejectedWholly(t *testing.T) {
	svc, repo := taskSvcWithRole("approver", false)
	results, err := svc.BulkSetStatus(context.Background(), nil, "p1", []string{"t1"}, "done-status", []string{"u1"}, "u1", "")
	if !errors.Is(err, domain.ErrBulkNotAllowed) || results != nil {
		t.Fatalf("results=%v err=%v, want ErrBulkNotAllowed", results, err)
	}
	if len(repo.setStatusCall) != 0 {
		t.Fatal("tidak boleh ada task yang diproses")
	}
}

// --- dependency ---

type depRecorder struct{ created, deleted int }

func (*depRecorder) ListPredecessors(context.Context, db.Executor, string) ([]repository.TaskDependency, error) {
	return nil, nil
}
func (*depRecorder) ListSuccessors(context.Context, db.Executor, string) ([]repository.TaskDependency, error) {
	return nil, nil
}
func (*depRecorder) ListForProject(context.Context, db.Executor, string) ([]repository.TaskDependency, error) {
	return nil, nil
}
func (*depRecorder) WouldCreateCycle(context.Context, db.Executor, string, string) ([]string, error) {
	return nil, nil
}
func (d *depRecorder) Create(context.Context, db.Executor, string, string, *string, string, string) error {
	d.created++
	return nil
}
func (d *depRecorder) Delete(context.Context, db.Executor, string, string, string, string, string) error {
	d.deleted++
	return nil
}

func TestTaskDependencyService_ApproverForbidden(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		role      string
		forbidden bool
	}{{"approver", true}, {"project_manager", false}, {"editor", false}, {"admin_workspace", false}}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			tasks := &fakeTaskRepo{byID: map[string]*repository.Task{
				"t1": {ID: "t1", ProjectID: "p1"}, "t2": {ID: "t2", ProjectID: "p1"},
			}}
			rec := &depRecorder{}
			svc := NewTaskDependencyService(rec, tasks, &fakeTaskProjects{workspaceID: "ws1"}, &fakeSprintRBAC{role: tc.role}, &fakeSprintProjectRoles{found: false})
			_, addErr := svc.Add(ctx, nil, "t1", "t2", "u1", "")
			rmErr := svc.Remove(ctx, nil, "t1", "t2", "u1", "")
			if tc.forbidden {
				if !errors.Is(addErr, domain.ErrForbidden) || !errors.Is(rmErr, domain.ErrForbidden) {
					t.Fatalf("add=%v remove=%v, want ErrForbidden", addErr, rmErr)
				}
				if rec.created != 0 || rec.deleted != 0 {
					t.Fatalf("tidak boleh menulis: %+v", rec)
				}
				return
			}
			if errors.Is(addErr, domain.ErrForbidden) || errors.Is(rmErr, domain.ErrForbidden) {
				t.Fatalf("%s tidak boleh ditolak: add=%v remove=%v", tc.role, addErr, rmErr)
			}
		})
	}
}

// --- S7A-06 (IG-152): gerbang jalur status/Board untuk Approver; matriks RBAC No 9, 10a, 10b, 12 ---

func TestApproverGates_StatusPath(t *testing.T) {
	ctx := context.Background()
	ops := map[string]func(*TaskService) error{
		"SetStatus": func(s *TaskService) error {
			return s.SetStatus(ctx, nil, "t1", "done-status", []string{"u1"}, "u1", "")
		},
		"Reorder":         func(s *TaskService) error { return s.Reorder(ctx, nil, "t1", "t2", true, "u1", "") },
		"SetCompleteness": func(s *TaskService) error { return s.SetCompleteness(ctx, nil, "t1", "complete", "u1", "") },
	}
	want := map[string]error{"SetStatus": domain.ErrUseApproval, "Reorder": domain.ErrForbidden, "SetCompleteness": domain.ErrForbidden}
	for name, op := range ops {
		// Approver: ditolak, task miliknya sekali pun, dan tidak ada yang ditulis.
		for _, owned := range []bool{true, false} {
			svc, repo := taskSvcWithRole("approver", owned)
			if err := op(svc); !errors.Is(err, want[name]) {
				t.Errorf("%s approver owned=%v: err = %v, want %v", name, owned, err, want[name])
			}
			if len(repo.setStatusCall) != 0 || len(repo.setPositions) != 0 {
				t.Errorf("%s approver: tidak boleh menulis (status=%v posisi=%v)", name, repo.setStatusCall, repo.setPositions)
			}
		}
		// PM dan Admin Workspace tidak berubah.
		for _, role := range []string{"project_manager", "admin_workspace"} {
			svc, _ := taskSvcWithRole(role, true)
			err := op(svc)
			if errors.Is(err, domain.ErrUseApproval) || errors.Is(err, domain.ErrForbidden) {
				t.Errorf("%s %s: tidak boleh ditolak gerbang Approver: %v", name, role, err)
			}
		}
	}
	// Editor pemilik tetap bisa memindahkan status; bukan pemilik tetap ErrNotYourTask (tidak berubah).
	own, _ := taskSvcWithRole("editor", true)
	if err := ops["SetStatus"](own); err != nil {
		t.Errorf("editor pemilik: %v", err)
	}
	other, _ := taskSvcWithRole("editor", false)
	if err := ops["SetStatus"](other); !errors.Is(err, domain.ErrNotYourTask) {
		t.Errorf("editor bukan pemilik: %v", err)
	}
}

func startWorkSvc(role string, owned, picActive bool) *TaskService {
	repo := &fakeTaskRepo{
		byID:   map[string]*repository.Task{"t1": {ID: "t1", ProjectID: "p1", StatusID: "st1", StatusName: "IN PROGRESS"}},
		owners: map[string]bool{"t1": owned},
	}
	return NewTaskService(repo, &fakeTaskPics{notPic: !picActive}, &fakeTaskDeps{}, &fakeTaskSessions{}, &fakeTaskProjects{workspaceID: "ws1"},
		&fakeTaskStatuses{}, &fakeSprintRBAC{role: role}, &fakeSprintProjectRoles{found: false}, &fakeTaskRules{}, &fakeTaskSprints{})
}

func TestStartWork_RoleMatrix(t *testing.T) { // No 10b "Mulai Pengerjaan"
	ctx := context.Background()
	cases := []struct {
		name, role string
		owned, pic bool
		want       error
	}{
		{"approver PIC aktif", "approver", false, true, nil},
		{"approver bukan PIC", "approver", true, false, domain.ErrForbidden}, // assignee saja tidak cukup
		{"editor pemilik", "editor", true, false, nil},
		{"editor bukan pemilik (celah lama S6E-06)", "editor", false, false, domain.ErrNotYourTask},
		{"project manager", "project_manager", false, false, nil},
		{"admin workspace", "admin_workspace", false, false, nil},
		{"viewer", "viewer", false, false, domain.ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := startWorkSvc(tc.role, tc.owned, tc.pic).StartWork(ctx, nil, "t1", "u1", "")
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPicOps_ApproverOnlyWhenActivePic(t *testing.T) { // No 12 "Phase PIC Handoff": Approver Terbatas
	ctx := context.Background()
	cases := []struct {
		name, actor string
		ok          bool
	}{{"approver PIC aktif", "pic-lama", true}, {"approver bukan PIC aktif", "u1", false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addSvc, addRepo := picSvcWithRole("approver", false)
			errAdd := addSvc.AddPic(ctx, nil, "t1", "u-baru", tc.actor, "")
			hSvc, hRepo := picSvcWithRole("approver", false)
			errHand := hSvc.HandoffPic(ctx, nil, "t1", "", "u-baru", tc.actor, "")
			for name, err := range map[string]error{"AddPic": errAdd, "HandoffPic": errHand} {
				if tc.ok && err != nil {
					t.Errorf("%s: err = %v, want nil", name, err)
				}
				if !tc.ok && !errors.Is(err, domain.ErrForbidden) {
					t.Errorf("%s: err = %v, want ErrForbidden", name, err)
				}
			}
			if !tc.ok && (addRepo.added != 0 || hRepo.handedOff != 0) {
				t.Errorf("tidak boleh menulis PIC (added=%d handedOff=%d)", addRepo.added, hRepo.handedOff)
			}
		})
	}
	for _, role := range []string{"project_manager", "admin_workspace"} {
		svc, _ := picSvcWithRole(role, false)
		if err := svc.AddPic(ctx, nil, "t1", "u-baru", "u1", ""); err != nil {
			t.Errorf("%s tidak boleh dibatasi: %v", role, err)
		}
	}
}

// Keputusan approval tetap berfungsi bagi Approver setelah gerbang jalur status (tidak lewat jalur SetStatus publik).
func TestApproverGates_ApprovalStillWorks(t *testing.T) {
	f := newApprovalFixture("approver", sess("IN PROGRESS", "UNDER REVIEW"), nil)
	if _, err := f.svc.DecideApproval(context.Background(), nil, "t1", "approve", "", "a1", ""); err != nil {
		t.Fatalf("setuju via approval harus tetap berfungsi: %v", err)
	}
	if err := f.svc.SetStatus(context.Background(), nil, "t1", "st-done", nil, "a1", ""); !errors.Is(err, domain.ErrUseApproval) {
		t.Fatalf("SetStatus biasa harus USE_APPROVAL: %v", err)
	}
}
