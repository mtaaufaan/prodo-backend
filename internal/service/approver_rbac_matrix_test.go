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
