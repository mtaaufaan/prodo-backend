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

type fakeTaskRepo struct {
	byID               map[string]*repository.Task
	setPositions       map[string]float64
	setStatusCall      []struct{ id, statusID string }
	setStatusAuditRole string
}

func (f *fakeTaskRepo) Create(_ context.Context, _ db.Executor, _ string, _, _ *string, _, _ string, _ json.RawMessage, _ string, _ *time.Time, _ *float64, _ *int, _ string, _ []string, _, _ string) (*repository.Task, error) {
	return nil, nil
}
func (f *fakeTaskRepo) Get(_ context.Context, _ db.Executor, taskID string) (*repository.Task, error) {
	t, ok := f.byID[taskID]
	if !ok {
		return nil, domain.ErrTaskNotFound
	}
	return t, nil
}
func (f *fakeTaskRepo) List(_ context.Context, _ db.Executor, projectID string, filter repository.TaskFilter) ([]repository.Task, error) {
	var out []repository.Task
	for _, t := range f.byID {
		if t.ProjectID != projectID {
			continue
		}
		if filter.StatusID != "" && t.StatusID != filter.StatusID {
			continue
		}
		out = append(out, *t)
	}
	// urut posisi meniru ORDER BY position asli
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Position < out[i].Position {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}
func (f *fakeTaskRepo) Update(_ context.Context, _ db.Executor, _, _ string, _ json.RawMessage, _ string, _ *time.Time, _ *float64, _ *int, _ *string, _, _, _ string) error {
	return nil
}
func (f *fakeTaskRepo) SetStatus(_ context.Context, _ db.Executor, taskID, statusID string, _ bool, _, actorRole, _, _, _ string) error {
	f.setStatusCall = append(f.setStatusCall, struct{ id, statusID string }{taskID, statusID})
	f.setStatusAuditRole = actorRole
	if t, ok := f.byID[taskID]; ok {
		t.StatusID = statusID
	}
	return nil
}
func (f *fakeTaskRepo) SetPosition(_ context.Context, _ db.Executor, taskID string, position float64) error {
	if f.setPositions == nil {
		f.setPositions = map[string]float64{}
	}
	f.setPositions[taskID] = position
	return nil
}
func (f *fakeTaskRepo) SetCompleteness(_ context.Context, _ db.Executor, _, _, _, _, _ string) error {
	return nil
}
func (f *fakeTaskRepo) SoftDelete(_ context.Context, _ db.Executor, _, _, _, _ string) error {
	return nil
}
func (f *fakeTaskRepo) GetProjectID(_ context.Context, _ db.Executor, taskID string) (string, error) {
	t, ok := f.byID[taskID]
	if !ok {
		return "", domain.ErrTaskNotFound
	}
	return t.ProjectID, nil
}
func (f *fakeTaskRepo) AssignUser(_ context.Context, _ db.Executor, _, _, _ string) error { return nil }
func (f *fakeTaskRepo) CreateVersionSnapshot(_ context.Context, _ db.Executor, _, _ string, _ json.RawMessage, _, _ string) error {
	return nil
}
func (f *fakeTaskRepo) ListVersionSnapshots(_ context.Context, _ db.Executor, _ string) ([]repository.TaskVersionSnapshot, error) {
	return nil, nil
}
func (f *fakeTaskRepo) ListAudit(_ context.Context, _ db.Executor, _ string, _, _ int) ([]repository.AuditEntry, int, error) {
	return nil, 0, nil
}

type fakeTaskPics struct{ group []repository.PicGroupMember }

func (f *fakeTaskPics) DeactivateActiveForTask(_ context.Context, _ db.Executor, _ string) error {
	return nil
}
func (f *fakeTaskPics) CreatePhase(_ context.Context, _ db.Executor, _, _, _ string, _ *string) error {
	return nil
}
func (f *fakeTaskPics) ListGroupForStatus(_ context.Context, _ db.Executor, _, _ string) ([]repository.PicGroupMember, error) {
	return f.group, nil
}
func (f *fakeTaskPics) IsActivePic(_ context.Context, _ db.Executor, _, _ string) (bool, error) {
	return true, nil
}

type fakeTaskDeps struct{}

func (f *fakeTaskDeps) ListIncompletePredecessors(_ context.Context, _ db.Executor, _ string) ([]repository.TaskDependency, error) {
	return nil, nil
}
func (f *fakeTaskDeps) NotifySuccessorPics(_ context.Context, _ db.Executor, _ string, _ bool) error {
	return nil
}

type fakeTaskProjects struct{ workspaceID string }

func (f *fakeTaskProjects) GetWorkspaceID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return f.workspaceID, nil
}
func (f *fakeTaskProjects) GetAllowEditorStoryPoints(_ context.Context, _ db.Executor, _ string) (bool, error) {
	return true, nil
}

type fakeTaskStatuses struct {
	byID map[string]*repository.CustomStatus
}

func (f *fakeTaskStatuses) GetBacklogStatus(_ context.Context, _ db.Executor, _ string) (*repository.CustomStatus, error) {
	return nil, nil
}
func (f *fakeTaskStatuses) Get(_ context.Context, _ db.Executor, statusID string) (*repository.CustomStatus, error) {
	s, ok := f.byID[statusID]
	if !ok {
		return nil, domain.ErrCustomStatusNotFound
	}
	return s, nil
}

type fakeTaskSessions struct{}

func (f *fakeTaskSessions) OpenSession(_ context.Context, _ db.Executor, _, _ string, _ bool, _ string) error {
	return nil
}
func (f *fakeTaskSessions) CloseActiveSession(_ context.Context, _ db.Executor, _ string) error {
	return nil
}
func (f *fakeTaskSessions) StartWork(_ context.Context, _ db.Executor, _ string) error { return nil }
func (f *fakeTaskSessions) ListForTask(_ context.Context, _ db.Executor, _ string) ([]repository.TaskStatusSession, error) {
	return nil, nil
}
func (f *fakeTaskSessions) NotifyRegression(_ context.Context, _ db.Executor, _, _ string) error {
	return nil
}

type fakeTaskRules struct{}

func (f *fakeTaskRules) Evaluate(_ context.Context, _ db.Executor, _, _ string, _ *repository.Task, _, _ string) {
}

func newTaskServiceForTest(repo *fakeTaskRepo, statuses map[string]*repository.CustomStatus) *TaskService {
	return NewTaskService(repo, &fakeTaskPics{}, &fakeTaskDeps{}, &fakeTaskSessions{},
		&fakeTaskProjects{workspaceID: "ws1"}, &fakeTaskStatuses{byID: statuses},
		&fakeSprintRBAC{role: "project_manager"}, &fakeSprintProjectRoles{found: false}, &fakeTaskRules{})
}

func TestTaskService_Reorder_MidpointBetweenNeighbors(t *testing.T) {
	repo := &fakeTaskRepo{byID: map[string]*repository.Task{
		"a": {ID: "a", ProjectID: "p1", StatusID: "s1", Position: 1},
		"b": {ID: "b", ProjectID: "p1", StatusID: "s1", Position: 2},
		"c": {ID: "c", ProjectID: "p1", StatusID: "s1", Position: 3},
	}}
	svc := newTaskServiceForTest(repo, nil)
	// pindahkan "c" ke sebelum "b" -> posisi baru harus di antara a(1) dan b(2)
	if err := svc.Reorder(context.Background(), nil, "c", "b", true, "user1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := repo.setPositions["c"]
	if got <= 1 || got >= 2 {
		t.Fatalf("expected new position between 1 and 2, got %v", got)
	}
}

func TestTaskService_Reorder_RejectsCrossColumn(t *testing.T) {
	repo := &fakeTaskRepo{byID: map[string]*repository.Task{
		"a": {ID: "a", ProjectID: "p1", StatusID: "s1", Position: 1},
		"b": {ID: "b", ProjectID: "p1", StatusID: "s2", Position: 1},
	}}
	svc := newTaskServiceForTest(repo, nil)
	if err := svc.Reorder(context.Background(), nil, "a", "b", true, "user1", "member"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for cross-column reorder, got %v", err)
	}
}

func TestTaskService_BulkSetStatus_PartialFailure(t *testing.T) {
	statuses := map[string]*repository.CustomStatus{
		"done-status":    {ID: "done-status", Name: "DONE"},
		"backlog-status": {ID: "backlog-status", Name: "BACKLOG"},
	}
	repo := &fakeTaskRepo{byID: map[string]*repository.Task{
		"ok1":  {ID: "ok1", ProjectID: "p1", StatusID: "backlog-status"},
		"ok2":  {ID: "ok2", ProjectID: "p1", StatusID: "backlog-status"},
		"real": {ID: "real", ProjectID: "p1", StatusID: "backlog-status"},
	}}
	svc := newTaskServiceForTest(repo, statuses)
	results := svc.BulkSetStatus(context.Background(), nil, []string{"ok1", "missing", "ok2"}, "done-status", []string{"pic1"}, "user1", "member")
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	successCount := 0
	failCount := 0
	for _, r := range results {
		if r.Err == nil {
			successCount++
		} else {
			failCount++
			if r.TaskID != "missing" {
				t.Fatalf("expected failure for 'missing', got failure for %q", r.TaskID)
			}
		}
	}
	if successCount != 2 || failCount != 1 {
		t.Fatalf("expected 2 success + 1 failure, got %d success + %d failure", successCount, failCount)
	}
}

// TestTaskService_AuditUsesResolvedWorkspaceRole -- IG-94/IG-97, sama pola
// regresi TestSprintService_AuditUsesResolvedWorkspaceRole (IG-92): rute
// task sengaja TIDAK dipasangi middleware RequireRole (route berbasis
// :projectId), jadi parameter actorRole yang diteruskan handler SELALU
// string kosong. authorize()/resolveRole() sendiri sudah resolve role asli
// -- audit trail (insertTaskAudit) HARUS pakai role hasil resolve itu,
// BUKAN parameter actorRole kosong mentah.
func TestTaskService_AuditUsesResolvedWorkspaceRole(t *testing.T) {
	statuses := map[string]*repository.CustomStatus{
		"done-status":    {ID: "done-status", Name: "DONE"},
		"backlog-status": {ID: "backlog-status", Name: "BACKLOG"},
	}
	repo := &fakeTaskRepo{byID: map[string]*repository.Task{
		"t1": {ID: "t1", ProjectID: "p1", StatusID: "backlog-status"},
	}}
	svc := newTaskServiceForTest(repo, statuses)
	// actorRole="" meniru parameter kosong yang benar-benar dikirim handler
	// (lihat komentar authorize) -- audit HARUS tetap terisi "project_manager"
	// (resolusi fallback fakeSprintRBAC di newTaskServiceForTest).
	if err := svc.SetStatus(context.Background(), nil, "t1", "done-status", []string{"pic1"}, "user1", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.setStatusAuditRole != "project_manager" {
		t.Fatalf("expected audit actorRole 'project_manager' (resolved), got %q", repo.setStatusAuditRole)
	}
}
