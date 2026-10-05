package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeSprintRepo struct {
	byID          map[string]*repository.Sprint
	activeByProj  map[string]*repository.Sprint
	countInProj   int
	nameTaken     bool
	takenCodes    map[string]bool
	nextNum       int
	lastCode      string
	createErr     error
	created       []string
	statusChanges []struct{ id, status, action, actorRole string }
	unassigned    []string
	closedIDs     []string
	assignCalls   []struct {
		sprintID string
		taskIDs  []string
	}
	deleted []string
}

func (f *fakeSprintRepo) CodeTaken(_ context.Context, _ db.Executor, _, code string) (bool, error) {
	return f.takenCodes[code], nil
}
func (f *fakeSprintRepo) NextAutoNumber(_ context.Context, _ db.Executor, _ string) (int, error) {
	return f.nextNum, nil
}
func (f *fakeSprintRepo) Create(_ context.Context, _ db.Executor, projectID, code, name string, _, _ *time.Time, _ *string, _, _, _ string) (*repository.Sprint, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.lastCode = code
	id := "sp_" + name
	s := &repository.Sprint{ID: id, ProjectID: projectID, Code: code, Name: name, Status: "backlog"}
	f.byID[id] = s
	f.created = append(f.created, name)
	return s, nil
}
func (f *fakeSprintRepo) List(_ context.Context, _ db.Executor, projectID string) ([]repository.Sprint, error) {
	var out []repository.Sprint
	for _, s := range f.byID {
		if s.ProjectID == projectID {
			out = append(out, *s)
		}
	}
	return out, nil
}
func (f *fakeSprintRepo) NameTaken(_ context.Context, _ db.Executor, _, _ string, _ *string) (bool, error) {
	return f.nameTaken, nil
}
func (f *fakeSprintRepo) CountInProject(_ context.Context, _ db.Executor, _ string) (int, error) {
	return f.countInProj, nil
}
func (f *fakeSprintRepo) Get(_ context.Context, _ db.Executor, sprintID string) (*repository.Sprint, error) {
	s, ok := f.byID[sprintID]
	if !ok {
		return nil, domain.ErrSprintNotFound
	}
	return s, nil
}
func (f *fakeSprintRepo) GetActiveInProject(_ context.Context, _ db.Executor, projectID string) (*repository.Sprint, error) {
	return f.activeByProj[projectID], nil
}
func (f *fakeSprintRepo) Update(_ context.Context, _ db.Executor, sprintID, name string, _, _ *time.Time, _ *string, _, _, _ string, _ map[string]any) error {
	s, ok := f.byID[sprintID]
	if !ok {
		return domain.ErrSprintNotFound
	}
	s.Name = name
	return nil
}
func (f *fakeSprintRepo) SetStatus(_ context.Context, _ db.Executor, sprintID, status, action, _, _, actorRole, _ string) error {
	s, ok := f.byID[sprintID]
	if !ok {
		return domain.ErrSprintNotFound
	}
	s.Status = status
	f.statusChanges = append(f.statusChanges, struct{ id, status, action, actorRole string }{sprintID, status, action, actorRole})
	if status == "active" {
		f.activeByProj[s.ProjectID] = s
	} else if f.activeByProj[s.ProjectID] != nil && f.activeByProj[s.ProjectID].ID == sprintID {
		delete(f.activeByProj, s.ProjectID)
	}
	return nil
}
func (f *fakeSprintRepo) Delete(_ context.Context, _ db.Executor, sprintID, _, _, _, _ string) error {
	if _, ok := f.byID[sprintID]; !ok {
		return domain.ErrSprintNotFound
	}
	f.deleted = append(f.deleted, sprintID)
	delete(f.byID, sprintID)
	return nil
}
func (f *fakeSprintRepo) UnassignIncompleteTasks(_ context.Context, _ db.Executor, sprintID string, closedStatusIDs []string) error {
	f.unassigned = append(f.unassigned, sprintID)
	f.closedIDs = closedStatusIDs
	return nil
}
func (f *fakeSprintRepo) Summary(_ context.Context, _ db.Executor, _ string) (totalSP, doneSP, unestimatedCount, taskCount int, err error) {
	return 10, 4, 1, 5, nil
}
func (f *fakeSprintRepo) AssignTasks(_ context.Context, _ db.Executor, sprintID, _, _, _, _ string, taskIDs []string) error {
	f.assignCalls = append(f.assignCalls, struct {
		sprintID string
		taskIDs  []string
	}{sprintID, taskIDs})
	return nil
}

type fakeSprintProjects struct{ workspaceID string }

func (f *fakeSprintProjects) GetWorkspaceID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return f.workspaceID, nil
}

type fakeSprintStatuses struct{ list []repository.CustomStatus }

func (f *fakeSprintStatuses) ListForScope(_ context.Context, _ db.Executor, _, _ string) ([]repository.CustomStatus, error) {
	return f.list, nil
}

type fakeSprintRBAC struct{ role string }

func (f *fakeSprintRBAC) GetMemberRole(_ context.Context, _ db.Executor, _, _ string) (string, error) {
	return f.role, nil
}

type fakeSprintProjectRoles struct {
	role  string
	found bool
}

func (f *fakeSprintProjectRoles) GetRole(_ context.Context, _ db.Executor, _, _ string) (role string, found bool, err error) {
	return f.role, f.found, nil
}

func newSprintServiceForTest(repo *fakeSprintRepo, wsRole string) *SprintService {
	return NewSprintService(repo, &fakeSprintProjects{workspaceID: "ws1"},
		&fakeSprintStatuses{list: []repository.CustomStatus{{ID: "done-status", Name: "DONE"}, {ID: "canceled-status", Name: "CANCELED"}, {ID: "wip-status", Name: "IN PROGRESS"}}},
		&fakeSprintRBAC{role: wsRole}, &fakeSprintProjectRoles{found: false})
}

func TestSprintService_Create_AutoName(t *testing.T) {
	repo := &fakeSprintRepo{byID: map[string]*repository.Sprint{}, activeByProj: map[string]*repository.Sprint{}, countInProj: 3, nextNum: 4}
	svc := newSprintServiceForTest(repo, "project_manager")
	sprint, err := svc.Create(context.Background(), nil, "proj1", "", "", nil, nil, nil, nil, "user1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sprint.Name != "Sprint 4" {
		t.Fatalf("expected auto-generated name 'Sprint 4', got %q", sprint.Name)
	}
}

func TestSprintService_Create_NameTaken(t *testing.T) {
	repo := &fakeSprintRepo{byID: map[string]*repository.Sprint{}, activeByProj: map[string]*repository.Sprint{}, nameTaken: true, countInProj: 1, nextNum: 2}
	svc := newSprintServiceForTest(repo, "project_manager")
	_, err := svc.Create(context.Background(), nil, "proj1", "", "Sprint 1", nil, nil, nil, nil, "user1", "member")
	if !errors.Is(err, domain.ErrSprintNameTaken) {
		t.Fatalf("expected ErrSprintNameTaken, got %v", err)
	}
}

func TestSprintService_Create_ViewerForbidden(t *testing.T) {
	repo := &fakeSprintRepo{byID: map[string]*repository.Sprint{}, activeByProj: map[string]*repository.Sprint{}}
	svc := newSprintServiceForTest(repo, "viewer")
	_, err := svc.Create(context.Background(), nil, "proj1", "", "Sprint X", nil, nil, nil, nil, "user1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestSprintService_StartSprint_AutoClosesPreviousActive(t *testing.T) {
	repo := &fakeSprintRepo{byID: map[string]*repository.Sprint{}, activeByProj: map[string]*repository.Sprint{}}
	old := &repository.Sprint{ID: "sp_old", ProjectID: "proj1", Name: "Sprint 1", Status: "active"}
	next := &repository.Sprint{ID: "sp_next", ProjectID: "proj1", Name: "Sprint 2", Status: "backlog"}
	repo.byID[old.ID] = old
	repo.byID[next.ID] = next
	repo.activeByProj["proj1"] = old

	svc := newSprintServiceForTest(repo, "project_manager")
	if err := svc.StartSprint(context.Background(), nil, "sp_next", "user1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if old.Status != "done" {
		t.Fatalf("expected old sprint status 'done', got %q", old.Status)
	}
	if next.Status != "active" {
		t.Fatalf("expected new sprint status 'active', got %q", next.Status)
	}
	if len(repo.unassigned) != 1 || repo.unassigned[0] != "sp_old" {
		t.Fatalf("expected UnassignIncompleteTasks called for sp_old, got %v", repo.unassigned)
	}
}

func TestSprintService_CompleteSprint_UnassignsIncompleteTasks(t *testing.T) {
	repo := &fakeSprintRepo{byID: map[string]*repository.Sprint{}, activeByProj: map[string]*repository.Sprint{}}
	sp := &repository.Sprint{ID: "sp_1", ProjectID: "proj1", Name: "Sprint 1", Status: "active"}
	repo.byID[sp.ID] = sp
	repo.activeByProj["proj1"] = sp

	svc := newSprintServiceForTest(repo, "project_manager")
	if err := svc.CompleteSprint(context.Background(), nil, "sp_1", "user1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sp.Status != "done" {
		t.Fatalf("expected status 'done', got %q", sp.Status)
	}
	if len(repo.unassigned) != 1 {
		t.Fatalf("expected UnassignIncompleteTasks called once, got %d", len(repo.unassigned))
	}
	// DONE dan CANCELED tetap di sprint; IN PROGRESS dikembalikan ke backlog.
	if len(repo.closedIDs) != 2 || repo.closedIDs[0] != "done-status" || repo.closedIDs[1] != "canceled-status" {
		t.Fatalf("closed status IDs = %v, want [done-status canceled-status]", repo.closedIDs)
	}
}

func TestSprintService_ReopenSprint_OnlyFromDone(t *testing.T) {
	repo := &fakeSprintRepo{byID: map[string]*repository.Sprint{}, activeByProj: map[string]*repository.Sprint{}}
	active := &repository.Sprint{ID: "sp_active", ProjectID: "proj1", Status: "active"}
	done := &repository.Sprint{ID: "sp_done", ProjectID: "proj1", Status: "done"}
	repo.byID[active.ID] = active
	repo.byID[done.ID] = done

	svc := newSprintServiceForTest(repo, "project_manager")

	if err := svc.ReopenSprint(context.Background(), nil, "sp_active", "user1", "member"); !errors.Is(err, domain.ErrSprintNotDone) {
		t.Fatalf("expected ErrSprintNotDone for active sprint, got %v", err)
	}
	if err := svc.ReopenSprint(context.Background(), nil, "sp_done", "user1", "member"); err != nil {
		t.Fatalf("unexpected error reopening done sprint: %v", err)
	}
	if done.Status != "backlog" {
		t.Fatalf("expected reopened sprint status 'backlog', got %q", done.Status)
	}
}

func TestSprintService_AssignTasks(t *testing.T) {
	repo := &fakeSprintRepo{byID: map[string]*repository.Sprint{}, activeByProj: map[string]*repository.Sprint{}}
	sp := &repository.Sprint{ID: "sp_1", ProjectID: "proj1", Status: "backlog"}
	repo.byID[sp.ID] = sp

	svc := newSprintServiceForTest(repo, "project_manager")
	if err := svc.AssignTasks(context.Background(), nil, "sp_1", []string{"t1", "t2"}, "user1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.assignCalls) != 1 || len(repo.assignCalls[0].taskIDs) != 2 {
		t.Fatalf("expected AssignTasks called with 2 task ids, got %v", repo.assignCalls)
	}
}

// TestSprintService_AuditUsesResolvedWorkspaceRole -- IG-92 (ditemukan
// lewat verifikasi live, bukan review kode): rute sprint/task sengaja
// TIDAK dipasangi middleware RequireRole/RequirePlatformRole (route
// berbasis :projectId), jadi parameter actorRole yang diteruskan handler
// SELALU kosong. authorize() sendiri sudah resolve role asli lewat
// rbac/projectRoles untuk otorisasi -- audit trail HARUS pakai role hasil
// resolve itu, BUKAN parameter actorRole kosong dari handler.
func TestSprintService_AuditUsesResolvedWorkspaceRole(t *testing.T) {
	repo := &fakeSprintRepo{byID: map[string]*repository.Sprint{}, activeByProj: map[string]*repository.Sprint{}}
	sp := &repository.Sprint{ID: "sp_1", ProjectID: "proj1", Name: "Sprint 1", Status: "backlog"}
	repo.byID[sp.ID] = sp

	svc := newSprintServiceForTest(repo, "project_manager")
	// actorRole="" meniru parameter kosong yang benar-benar dikirim handler
	// (lihat komentar authorize) -- audit HARUS tetap terisi "project_manager".
	if err := svc.StartSprint(context.Background(), nil, "sp_1", "user1", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.statusChanges) != 1 || repo.statusChanges[0].actorRole != "project_manager" {
		t.Fatalf("expected audit actorRole 'project_manager' (resolved), got %+v", repo.statusChanges)
	}
}

func TestSprintService_Summary(t *testing.T) {
	repo := &fakeSprintRepo{byID: map[string]*repository.Sprint{}, activeByProj: map[string]*repository.Sprint{}}
	svc := newSprintServiceForTest(repo, "project_manager")
	total, done, unest, count, err := svc.Summary(context.Background(), nil, "sp_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 10 || done != 4 || unest != 1 || count != 5 {
		t.Fatalf("unexpected summary values: total=%d done=%d unest=%d count=%d", total, done, unest, count)
	}
}

func intPtr(n int) *int { return &n }

func TestSprintService_Create_CodeRules(t *testing.T) {
	cases := []struct {
		name      string
		count     int
		next      int
		taken     map[string]bool
		code      string
		startFrom *int
		wantCode  string
		wantName  string
		wantErr   error
	}{
		{name: "kode manual dinormalisasi", count: 2, code: " spr-9 ", wantCode: "SPR-9", wantName: "Sprint 3"},
		{name: "kode manual sudah dipakai", count: 2, code: "spr-01", taken: map[string]bool{"SPR-01": true}, wantErr: domain.ErrSprintCodeTaken},
		{name: "kode manual format salah", count: 2, code: "SPR 01", wantErr: domain.ErrInvalidInput},
		{name: "otomatis: increment dari terbesar", count: 3, next: 6, wantCode: "SPR-06", wantName: "Sprint 6"},
		{name: "sprint pertama tanpa pilihan -> ditanya", count: 0, wantErr: domain.ErrSprintStartRequired},
		{name: "sprint pertama nilai pilihan tidak sah", count: 0, startFrom: intPtr(2), wantErr: domain.ErrSprintStartRequired},
		{name: "sprint pertama mulai dari 0", count: 0, startFrom: intPtr(0), wantCode: "SPR-00", wantName: "Sprint 0"},
		{name: "sprint pertama mulai dari 1", count: 0, startFrom: intPtr(1), wantCode: "SPR-01", wantName: "Sprint 1"},
		{name: "start_from diabaikan kalau sudah ada sprint", count: 1, next: 1, startFrom: intPtr(0), wantCode: "SPR-01", wantName: "Sprint 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := &fakeSprintRepo{byID: map[string]*repository.Sprint{}, activeByProj: map[string]*repository.Sprint{}, countInProj: c.count, nextNum: c.next, takenCodes: c.taken}
			svc := newSprintServiceForTest(repo, "project_manager")
			sprint, err := svc.Create(context.Background(), nil, "proj1", c.code, "", c.startFrom, nil, nil, nil, "user1", "member")
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sprint.Code != c.wantCode || sprint.Name != c.wantName {
				t.Errorf("code/name = %q/%q, want %q/%q", sprint.Code, sprint.Name, c.wantCode, c.wantName)
			}
		})
	}
}
