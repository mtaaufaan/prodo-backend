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

func (f *fakeTaskRepo) Create(_ context.Context, _ db.Executor, _ string, _, _ *string, _, _ string, _ json.RawMessage, _ string, _, _ *time.Time, _ *float64, _ *int, _ string, _ []string, _, _ string) (*repository.Task, error) {
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
func (f *fakeTaskRepo) Update(_ context.Context, _ db.Executor, _, _ string, _ json.RawMessage, _ string, _, _ *time.Time, _ *float64, _ *int, _ *string, _, _, _ string) error {
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

type fakeTaskPics struct {
	group       []repository.PicGroupMember
	deactivated int
	phases      []string
}

func (f *fakeTaskPics) DeactivateActiveForTask(_ context.Context, _ db.Executor, _ string) error {
	f.deactivated++
	return nil
}
func (f *fakeTaskPics) CreatePhase(_ context.Context, _ db.Executor, _, _, userID string, _ *string) error {
	f.phases = append(f.phases, userID)
	return nil
}
func (f *fakeTaskPics) ListGroupForStatus(_ context.Context, _ db.Executor, _, _ string) ([]repository.PicGroupMember, error) {
	return f.group, nil
}
func (f *fakeTaskPics) IsActivePic(_ context.Context, _ db.Executor, _, _ string) (bool, error) {
	return true, nil
}

type fakeTaskDeps struct{ notified []bool }

func (f *fakeTaskDeps) ListIncompletePredecessors(_ context.Context, _ db.Executor, _ string) ([]repository.TaskDependency, error) {
	return nil, nil
}
func (f *fakeTaskDeps) NotifySuccessorPics(_ context.Context, _ db.Executor, _ string, unblocked bool) error {
	f.notified = append(f.notified, unblocked)
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

func (f *fakeTaskStatuses) GetBacklogStatus(_ context.Context, _ db.Executor, _, _ string) (*repository.CustomStatus, error) {
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
func (f *fakeTaskSessions) ListForProject(_ context.Context, _ db.Executor, _ string) ([]repository.TaskStatusSession, error) {
	return nil, nil
}
func (f *fakeTaskSessions) NotifyRegression(_ context.Context, _ db.Executor, _, _ string) error {
	return nil
}

type fakeTaskRules struct{}

func (f *fakeTaskRules) Evaluate(_ context.Context, _ db.Executor, _, _, _ string, _ *repository.Task, _, _ string) {
}

// fakeTaskSprints -- permisif secara default (SELALU "active" apa pun
// sprintID-nya) supaya test lama yang tidak berkaitan dengan
// domain.ErrTaskNotInSprint tidak ikut kena guard baru ini -- setiap
// fixture task yang keluar dari BACKLOG tetap WAJIB mengisi SprintID
// (nil = tidak ada sprint untuk dicek sama sekali, langsung ditolak
// SEBELUM fake ini sempat dipanggil).
type fakeTaskSprints struct{}

func (f *fakeTaskSprints) Get(_ context.Context, _ db.Executor, sprintID string) (*repository.Sprint, error) {
	return &repository.Sprint{ID: sprintID, Status: "active"}, nil
}

// fakeTaskSprintsWithStatus -- variasi terkendali, dipakai test yang perlu
// sprint BUKAN 'active' (mis. 'done').
type fakeTaskSprintsWithStatus struct{ status string }

func (f *fakeTaskSprintsWithStatus) Get(_ context.Context, _ db.Executor, sprintID string) (*repository.Sprint, error) {
	return &repository.Sprint{ID: sprintID, Status: f.status}, nil
}

func newTaskServiceForTest(repo *fakeTaskRepo, statuses map[string]*repository.CustomStatus) *TaskService {
	return NewTaskService(repo, &fakeTaskPics{}, &fakeTaskDeps{}, &fakeTaskSessions{},
		&fakeTaskProjects{workspaceID: "ws1"}, &fakeTaskStatuses{byID: statuses},
		&fakeSprintRBAC{role: "project_manager"}, &fakeSprintProjectRoles{found: false}, &fakeTaskRules{}, &fakeTaskSprints{})
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
	sprintID := "s1"
	repo := &fakeTaskRepo{byID: map[string]*repository.Task{
		"ok1":  {ID: "ok1", ProjectID: "p1", StatusID: "backlog-status", SprintID: &sprintID},
		"ok2":  {ID: "ok2", ProjectID: "p1", StatusID: "backlog-status", SprintID: &sprintID},
		"real": {ID: "real", ProjectID: "p1", StatusID: "backlog-status", SprintID: &sprintID},
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
	sprintID := "s1"
	repo := &fakeTaskRepo{byID: map[string]*repository.Task{
		"t1": {ID: "t1", ProjectID: "p1", StatusID: "backlog-status", SprintID: &sprintID},
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

// TestTaskService_SetStatus_NoSprint_Rejected -- susulan domain.
// ErrTaskNotInSprint (diminta user: "task board dengan status backlog
// yang bukan sprint backlog dan sprint berjalan tidak dapat dipindahkan
// statusnya") -- task BACKLOG tanpa sprint_id sama sekali tidak boleh
// pindah ke status lain SELAIN BLOCKED.
func TestTaskService_SetStatus_NoSprint_Rejected(t *testing.T) {
	statuses := map[string]*repository.CustomStatus{
		"done-status":    {ID: "done-status", Name: "DONE"},
		"backlog-status": {ID: "backlog-status", Name: "BACKLOG"},
	}
	repo := &fakeTaskRepo{byID: map[string]*repository.Task{
		"t1": {ID: "t1", ProjectID: "p1", StatusID: "backlog-status", StatusName: "BACKLOG", SprintID: nil},
	}}
	svc := NewTaskService(repo, &fakeTaskPics{}, &fakeTaskDeps{}, &fakeTaskSessions{},
		&fakeTaskProjects{workspaceID: "ws1"}, &fakeTaskStatuses{byID: statuses},
		&fakeSprintRBAC{role: "project_manager"}, &fakeSprintProjectRoles{found: false}, &fakeTaskRules{}, &fakeTaskSprints{})

	err := svc.SetStatus(context.Background(), nil, "t1", "done-status", []string{"pic1"}, "user1", "member")
	if !errors.Is(err, domain.ErrTaskNotInSprint) {
		t.Errorf("err = %v, want domain.ErrTaskNotInSprint", err)
	}
}

// TestTaskService_SetStatus_NoSprint_AllowsBlocked -- carve-out yang sama
// seperti ErrTaskIncomplete: menandai BLOCKED tidak butuh sprint sama
// sekali, konsisten dengan guard completeness di atasnya.
func TestTaskService_SetStatus_NoSprint_AllowsBlocked(t *testing.T) {
	statuses := map[string]*repository.CustomStatus{
		"blocked-status": {ID: "blocked-status", Name: "BLOCKED"},
		"backlog-status": {ID: "backlog-status", Name: "BACKLOG"},
	}
	repo := &fakeTaskRepo{byID: map[string]*repository.Task{
		"t1": {ID: "t1", ProjectID: "p1", StatusID: "backlog-status", StatusName: "BACKLOG", SprintID: nil},
	}}
	svc := NewTaskService(repo, &fakeTaskPics{}, &fakeTaskDeps{}, &fakeTaskSessions{},
		&fakeTaskProjects{workspaceID: "ws1"}, &fakeTaskStatuses{byID: statuses},
		&fakeSprintRBAC{role: "project_manager"}, &fakeSprintProjectRoles{found: false}, &fakeTaskRules{}, &fakeTaskSprints{})

	if err := svc.SetStatus(context.Background(), nil, "t1", "blocked-status", []string{"pic1"}, "user1", "member"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestTaskService_SetStatus_FinalStatusNotifiesSuccessors -- DONE dan CANCELED
// sama-sama melepas blokir successor (IG-118); DONE<->CANCELED tidak.
func TestTaskService_SetStatus_FinalStatusNotifiesSuccessors(t *testing.T) {
	cases := []struct {
		name, from, to string
		want           []bool
	}{
		{"canceled releases", "IN PROGRESS", "CANCELED", []bool{true}},
		{"done releases", "IN PROGRESS", "DONE", []bool{true}},
		{"reopen from canceled re-blocks", "CANCELED", "IN PROGRESS", []bool{false}},
		{"done to canceled is no-op", "DONE", "CANCELED", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			statuses := map[string]*repository.CustomStatus{
				"from": {ID: "from", Name: c.from, RequirePic: c.from != "DONE" && c.from != "CANCELED"},
				"to":   {ID: "to", Name: c.to, RequirePic: c.to != "DONE" && c.to != "CANCELED"},
			}
			repo := &fakeTaskRepo{byID: map[string]*repository.Task{
				"t1": {ID: "t1", ProjectID: "p1", StatusID: "from", StatusName: c.from},
			}}
			deps := &fakeTaskDeps{}
			svc := NewTaskService(repo, &fakeTaskPics{}, deps, &fakeTaskSessions{},
				&fakeTaskProjects{workspaceID: "ws1"}, &fakeTaskStatuses{byID: statuses},
				&fakeSprintRBAC{role: "project_manager"}, &fakeSprintProjectRoles{found: false}, &fakeTaskRules{}, &fakeTaskSprints{})

			if err := svc.SetStatus(context.Background(), nil, "t1", "to", []string{"pic1"}, "user1", "member"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(deps.notified) != len(c.want) || (len(c.want) == 1 && deps.notified[0] != c.want[0]) {
				t.Errorf("notified = %v, want %v", deps.notified, c.want)
			}
		})
	}
}

// TestTaskService_SetStatus_NoSprint_AllowsCanceled -- CANCELED dikecualikan
// dari guard sprint seperti BLOCKED, dan tidak butuh PIC (require_pic=false).
func TestTaskService_SetStatus_NoSprint_AllowsCanceled(t *testing.T) {
	statuses := map[string]*repository.CustomStatus{
		"canceled-status": {ID: "canceled-status", Name: "CANCELED", RequirePic: false},
		"backlog-status":  {ID: "backlog-status", Name: "BACKLOG", RequirePic: true},
	}
	repo := &fakeTaskRepo{byID: map[string]*repository.Task{
		"t1": {ID: "t1", ProjectID: "p1", StatusID: "backlog-status", StatusName: "BACKLOG", SprintID: nil},
	}}
	svc := NewTaskService(repo, &fakeTaskPics{}, &fakeTaskDeps{}, &fakeTaskSessions{},
		&fakeTaskProjects{workspaceID: "ws1"}, &fakeTaskStatuses{byID: statuses},
		&fakeSprintRBAC{role: "project_manager"}, &fakeSprintProjectRoles{found: false}, &fakeTaskRules{}, &fakeTaskSprints{})

	if err := svc.SetStatus(context.Background(), nil, "t1", "canceled-status", nil, "user1", "member"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestTaskService_SetStatus_NonActiveSprint_Rejected -- task masih tertaut
// sprint yang BUKAN sprint aktif project ini ditolak, baik sprint itu
// belum dimulai ('backlog' -- ditemukan user via pengujian live: task
// Sprint 1 yang belum dimulai tetap ditolak selama Sprint 0 yang aktif,
// mengoreksi asumsi awal "sprint backlog" berarti status sprint 'backlog'
// boleh, padahal seharusnya HANYA sprint 'active') maupun sudah selesai
// ('done' -- jarang sekali kejadian nyata karena SprintService.
// UnassignIncompleteTasks otomatis mengosongkan sprint_id task non-DONE
// begitu sprint ditutup, tapi guard tetap ditulis eksplisit untuk
// konsistensi + jaga-jaga race).
func TestTaskService_SetStatus_NonActiveSprint_Rejected(t *testing.T) {
	for _, sprintStatus := range []string{"backlog", "done"} {
		t.Run(sprintStatus, func(t *testing.T) {
			statuses := map[string]*repository.CustomStatus{
				"done-status":    {ID: "done-status", Name: "DONE"},
				"backlog-status": {ID: "backlog-status", Name: "BACKLOG"},
			}
			sprintID := "s1"
			repo := &fakeTaskRepo{byID: map[string]*repository.Task{
				"t1": {ID: "t1", ProjectID: "p1", StatusID: "backlog-status", StatusName: "BACKLOG", SprintID: &sprintID},
			}}
			sprints := &fakeTaskSprintsWithStatus{status: sprintStatus}
			svc := NewTaskService(repo, &fakeTaskPics{}, &fakeTaskDeps{}, &fakeTaskSessions{},
				&fakeTaskProjects{workspaceID: "ws1"}, &fakeTaskStatuses{byID: statuses},
				&fakeSprintRBAC{role: "project_manager"}, &fakeSprintProjectRoles{found: false}, &fakeTaskRules{}, sprints)

			err := svc.SetStatus(context.Background(), nil, "t1", "done-status", []string{"pic1"}, "user1", "member")
			if !errors.Is(err, domain.ErrTaskNotInSprint) {
				t.Errorf("err = %v, want domain.ErrTaskNotInSprint", err)
			}
		})
	}
}

// require_pic (parameter per status, diminta user: status akhir DONE/BLOCKED
// tidak perlu PIC) -- RequirePic true tetap mewajibkan PIC (US-017).
func TestTaskService_SetStatus_RequirePic_Rejected_WithoutPic(t *testing.T) {
	statuses := map[string]*repository.CustomStatus{
		"review": {ID: "review", Name: "UNDER REVIEW", RequirePic: true},
	}
	sprintID := "s1"
	repo := &fakeTaskRepo{byID: map[string]*repository.Task{
		"t1": {ID: "t1", ProjectID: "p1", StatusID: "review", StatusName: "IN PROGRESS", SprintID: &sprintID},
	}}
	svc := newTaskServiceForTest(repo, statuses)
	if err := svc.SetStatus(context.Background(), nil, "t1", "review", nil, "user1", "member"); !errors.Is(err, domain.ErrPicRequired) {
		t.Fatalf("err = %v, want domain.ErrPicRequired", err)
	}
}

// RequirePic false: tanpa PIC diterima, PIC lama dinonaktifkan, tidak ada
// fase PIC baru -- bahkan kalau klien tetap mengirim pic_ids.
func TestTaskService_SetStatus_NoPicRequired_DeactivatesAndCreatesNoPhase(t *testing.T) {
	statuses := map[string]*repository.CustomStatus{
		"x":    {ID: "x", Name: "IN PROGRESS", Position: 1, RequirePic: true},
		"done": {ID: "done", Name: "DONE", Position: 3, RequirePic: false},
	}
	sprintID := "s1"
	for name, pics := range map[string][]string{"tanpa pic": nil, "pic ikut terkirim diabaikan": {"pic1"}} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeTaskRepo{byID: map[string]*repository.Task{
				"t1": {ID: "t1", ProjectID: "p1", StatusID: "x", StatusName: "IN PROGRESS", SprintID: &sprintID},
			}}
			picFake := &fakeTaskPics{}
			svc := NewTaskService(repo, picFake, &fakeTaskDeps{}, &fakeTaskSessions{},
				&fakeTaskProjects{workspaceID: "ws1"}, &fakeTaskStatuses{byID: statuses},
				&fakeSprintRBAC{role: "project_manager"}, &fakeSprintProjectRoles{found: false}, &fakeTaskRules{}, &fakeTaskSprints{})
			if err := svc.SetStatus(context.Background(), nil, "t1", "done", pics, "user1", "member"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if picFake.deactivated != 1 {
				t.Errorf("PIC aktif lama dinonaktifkan %d kali, want 1", picFake.deactivated)
			}
			if len(picFake.phases) != 0 {
				t.Errorf("fase PIC baru = %v, want kosong", picFake.phases)
			}
		})
	}
}

// Status yang butuh PIC tetap membuat fase untuk PIC terpilih (tidak regresi).
func TestTaskService_SetStatus_RequirePic_CreatesPhases(t *testing.T) {
	statuses := map[string]*repository.CustomStatus{
		"x":      {ID: "x", Name: "IN PROGRESS", Position: 1, RequirePic: true},
		"review": {ID: "review", Name: "UNDER REVIEW", Position: 2, RequirePic: true},
	}
	sprintID := "s1"
	repo := &fakeTaskRepo{byID: map[string]*repository.Task{
		"t1": {ID: "t1", ProjectID: "p1", StatusID: "x", StatusName: "IN PROGRESS", SprintID: &sprintID},
	}}
	picFake := &fakeTaskPics{}
	svc := NewTaskService(repo, picFake, &fakeTaskDeps{}, &fakeTaskSessions{},
		&fakeTaskProjects{workspaceID: "ws1"}, &fakeTaskStatuses{byID: statuses},
		&fakeSprintRBAC{role: "project_manager"}, &fakeSprintProjectRoles{found: false}, &fakeTaskRules{}, &fakeTaskSprints{})
	if err := svc.SetStatus(context.Background(), nil, "t1", "review", []string{"a", "b"}, "user1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(picFake.phases) != 2 {
		t.Errorf("fase PIC = %v, want 2", picFake.phases)
	}
}
