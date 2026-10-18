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

// ---- TimesheetReportService ----

type fakeTimesheetRepo struct {
	exported int
	statuses []repository.TimesheetStatus
	tasks    []repository.TimesheetTask
}

func (f *fakeTimesheetRepo) ProjectInfo(_ context.Context, _ db.Executor, _ string) (workspaceID, name string, err error) {
	return "ws-1", "PRODO", nil
}
func (f *fakeTimesheetRepo) ListStatuses(_ context.Context, _ db.Executor, _ string) ([]repository.TimesheetStatus, error) {
	return f.statuses, nil
}
func (f *fakeTimesheetRepo) ListTasks(_ context.Context, _ db.Executor, _ string) ([]repository.TimesheetTask, error) {
	return f.tasks, nil
}
func (f *fakeTimesheetRepo) ListMembers(_ context.Context, _ db.Executor, _ string) ([]repository.TimesheetMember, error) {
	return nil, nil
}
func (f *fakeTimesheetRepo) PendingCount(_ context.Context, _ db.Executor, _ string) (int, error) {
	return 0, nil
}
func (f *fakeTimesheetRepo) ListManualEntries(_ context.Context, _ db.Executor, _, _ string) ([]repository.TimesheetPendingEntry, error) {
	return nil, nil
}
func (f *fakeTimesheetRepo) ListMine(_ context.Context, _ db.Executor, _ string, _, _ *time.Time) ([]repository.MyTimesheetEntry, error) {
	return nil, nil
}
func (f *fakeTimesheetRepo) LogExport(_ context.Context, _ db.Executor, _, _, _, _, _ string, _, _ int) error {
	f.exported++
	return nil
}

func TestBuildTimesheetSummary(t *testing.T) {
	statuses := []repository.TimesheetStatus{{Name: "BACKLOG"}, {Name: "IN PROGRESS", ColorToken: "signal"}, {Name: "DONE"}}
	tasks := []repository.TimesheetTask{
		{Code: "P-1", StatusName: "IN PROGRESS", EstimatedHours: 4, LoggedMinutes: 300}, // 5 jam > 4 -> melebihi
		{Code: "P-2", StatusName: "DONE", EstimatedHours: 8, LoggedMinutes: 120},        // 2 jam
		{Code: "P-3", StatusName: "DONE", EstimatedHours: 2, LoggedMinutes: 180},        // 3 jam > 2 -> melebihi
		{Code: "P-4", StatusName: "BACKLOG", EstimatedHours: 0, LoggedMinutes: 60},      // tanpa estimasi
		{Code: "P-5", StatusName: "DONE", EstimatedHours: 1, LoggedMinutes: 120},        // 2 jam > 1 -> melebihi (kode ke-3 tidak ikut catatan)
	}
	members := []repository.TimesheetMember{{UserID: "u1", Name: "Taufan", LoggedMinutes: 600, TaskCount: 3, ActiveCount: 2}, {UserID: "u2", Name: "Joyse"}}
	got := buildTimesheetSummary("PRODO", statuses, tasks, members, 2)

	if got.LoggedHours != 13 { // 780 menit
		t.Fatalf("jam tercatat = %v, want 13", got.LoggedHours)
	}
	if got.EstimateHours != 15 {
		t.Fatalf("total estimasi = %v, want 15", got.EstimateHours)
	}
	if got.OverCount != 3 || len(got.OverCodes) != 2 || got.OverCodes[0] != "P-1" || got.OverCodes[1] != "P-3" {
		t.Fatalf("over = %d %v, want 3 [P-1 P-3]", got.OverCount, got.OverCodes)
	}
	if got.MembersInvolved != 2 || got.PendingCount != 2 {
		t.Fatalf("members/pending = %d/%d", got.MembersInvolved, got.PendingCount)
	}
	if len(got.Tasks) != 4 { // P-4 tanpa estimasi tidak masuk akurasi
		t.Fatalf("task berestimasi = %d, want 4", len(got.Tasks))
	}
	byName := map[string]TimesheetStatusHours{}
	for _, b := range got.ByStatus {
		byName[b.Name] = b
	}
	if byName["DONE"].Hours != 7 || byName["DONE"].TaskCount != 3 {
		t.Fatalf("DONE = %+v, want 7 jam / 3 task", byName["DONE"])
	}
	if byName["IN PROGRESS"].Hours != 5 || byName["BACKLOG"].Hours != 1 {
		t.Fatalf("bars = %+v", byName)
	}
	if got.Members[0].Hours != 10 || got.Members[0].ActiveCount != 2 || got.Members[1].Hours != 0 {
		t.Fatalf("members = %+v", got.Members)
	}
}

func newTestTimesheetService(repo *fakeTimesheetRepo, pmID, role string) *TimesheetReportService {
	return NewTimesheetReportService(repo, &fakePerformancePMChecker{pmUserID: pmID}, &fakePerformanceRoleChecker{role: role}, nil)
}

func TestTimesheetAuthorize(t *testing.T) {
	repo := &fakeTimesheetRepo{}
	svc := newTestTimesheetService(repo, "pm-1", "editor")
	ctx := context.Background()

	if _, err := svc.Summary(ctx, nil, "p1", "pm-1", "member"); err != nil {
		t.Fatalf("PM project harus boleh: %v", err)
	}
	if _, err := svc.Summary(ctx, nil, "p1", "x", "group_admin"); err != nil {
		t.Fatalf("GA harus boleh: %v", err)
	}
	if _, err := svc.Summary(ctx, nil, "p1", "x", "member"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("editor non-PM harus ditolak, got %v", err)
	}
	aw := newTestTimesheetService(repo, "pm-1", "admin_workspace")
	if _, err := aw.Summary(ctx, nil, "p1", "x", "member"); err != nil {
		t.Fatalf("AW harus boleh: %v", err)
	}
	if _, err := svc.Summary(ctx, nil, "", "pm-1", "member"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("project kosong harus invalid, got %v", err)
	}
}

func TestTimesheetExportLogsAudit(t *testing.T) {
	repo := &fakeTimesheetRepo{tasks: []repository.TimesheetTask{{Code: "P-1", Title: "A", StatusName: "DONE", LoggedMinutes: 90, ActivePICs: []string{"Taufan"}}}}
	svc := newTestTimesheetService(repo, "pm-1", "editor")
	name, rows, err := svc.Export(context.Background(), nil, "p1", "pm-1", "member")
	if err != nil || name != "PRODO" || len(rows) != 1 || rows[0].LoggedHours != 1.5 {
		t.Fatalf("export = %q %+v %v", name, rows, err)
	}
	if repo.exported != 1 {
		t.Fatalf("audit export tercatat %d kali, want 1", repo.exported)
	}
	if _, _, err := newTestTimesheetService(repo, "pm-1", "editor").Export(context.Background(), nil, "p1", "x", "member"); !errors.Is(err, domain.ErrForbidden) || repo.exported != 1 {
		t.Fatalf("non-PM tidak boleh ekspor/tercatat, err=%v exported=%d", err, repo.exported)
	}
}

func TestTimesheetMineDateValidation(t *testing.T) {
	svc := newTestTimesheetService(&fakeTimesheetRepo{}, "pm-1", "editor")
	ctx := context.Background()
	for _, tc := range []struct{ from, to string }{{"2026-13-01", ""}, {"", "bukan-tanggal"}, {"2026-10-10", "2026-10-01"}} {
		if _, err := svc.Mine(ctx, nil, "u1", tc.from, tc.to); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("from=%q to=%q harus invalid, got %v", tc.from, tc.to, err)
		}
	}
	if _, err := svc.Mine(ctx, nil, "u1", "2026-10-01", "2026-10-01"); err != nil {
		t.Fatalf("from == to (satu hari) harus valid: %v", err)
	}
}

// ---- TimeEntryService: audit, notifikasi, edit setelah ditolak ----

type fakeTimeEntryRepo struct {
	entry     *repository.TimeEntry
	approvers []string
	notified  []string // "<userID>:<type>"
	audits    []string
	updated   bool

	// S6E-10 (catat jam)
	dayMinutes  int
	dayLastEnd  *time.Time
	logged      *repository.TimeEntry
	loggedNote  *string
	auditRoles  []string
	auditStates []map[string]any
	userTotal   int
}

func (f *fakeTimeEntryRepo) CreateLogged(_ context.Context, _ db.Executor, taskID, userID string, startedAt, endedAt time.Time, note *string) (*repository.TimeEntry, error) {
	mins := int(endedAt.Sub(startedAt).Minutes())
	approved := true
	f.logged = &repository.TimeEntry{ID: "e-logged", TaskID: taskID, UserID: userID, StartedAt: startedAt, EndedAt: &endedAt, DurationMinutes: &mins, EntryType: "manual", IsApproved: &approved}
	f.loggedNote = note
	return f.logged, nil
}
func (f *fakeTimeEntryRepo) DayUsage(context.Context, db.Executor, string, time.Time, time.Time) (int, *time.Time, error) {
	return f.dayMinutes, f.dayLastEnd, nil
}
func (f *fakeTimeEntryRepo) SumUserMinutesForTask(context.Context, db.Executor, string, string) (int, error) {
	return f.userTotal, nil
}

func (f *fakeTimeEntryRepo) StartTimer(context.Context, db.Executor, string, string) (*repository.ActiveTimer, error) {
	return nil, nil
}
func (f *fakeTimeEntryRepo) GetActiveTimer(context.Context, db.Executor, string) (*repository.ActiveTimer, error) {
	return nil, nil
}
func (f *fakeTimeEntryRepo) StopTimer(context.Context, db.Executor, string, string) (*repository.TimeEntry, error) {
	return nil, nil
}
func (f *fakeTimeEntryRepo) CreateManual(_ context.Context, _ db.Executor, taskID, userID string, startedAt, endedAt time.Time, _ *string) (*repository.TimeEntry, error) {
	mins := int(endedAt.Sub(startedAt).Minutes())
	f.entry = &repository.TimeEntry{ID: "e1", TaskID: taskID, UserID: userID, StartedAt: startedAt, EndedAt: &endedAt, DurationMinutes: &mins, EntryType: "manual"}
	return f.entry, nil
}
func (f *fakeTimeEntryRepo) HasOverlap(context.Context, db.Executor, string, time.Time, time.Time, string) (bool, error) {
	return false, nil
}
func (f *fakeTimeEntryRepo) Get(context.Context, db.Executor, string) (*repository.TimeEntry, error) {
	return f.entry, nil
}
func (f *fakeTimeEntryRepo) UpdateManual(_ context.Context, _ db.Executor, _ string, startedAt, endedAt time.Time, _ *string) (*repository.TimeEntry, error) {
	if f.entry.IsApproved != nil && *f.entry.IsApproved {
		return nil, domain.ErrTimeEntryAlreadyApproved
	}
	f.updated = true
	mins := int(endedAt.Sub(startedAt).Minutes())
	f.entry = &repository.TimeEntry{ID: f.entry.ID, TaskID: f.entry.TaskID, UserID: f.entry.UserID, StartedAt: startedAt, EndedAt: &endedAt, DurationMinutes: &mins, EntryType: "manual"}
	return f.entry, nil
}
func (f *fakeTimeEntryRepo) Approve(context.Context, db.Executor, string, string) error { return nil }
func (f *fakeTimeEntryRepo) Reject(context.Context, db.Executor, string, string, string) error {
	return nil
}
func (f *fakeTimeEntryRepo) ListForTask(context.Context, db.Executor, string, string, string) ([]repository.TimeEntry, error) {
	return nil, nil
}
func (f *fakeTimeEntryRepo) SumLoggedMinutesForTask(context.Context, db.Executor, string) (int, error) {
	return 0, nil
}
func (f *fakeTimeEntryRepo) ListApproverIDs(context.Context, db.Executor, string) ([]string, error) {
	return f.approvers, nil
}
func (f *fakeTimeEntryRepo) TaskLabel(context.Context, db.Executor, string) (code, title string, err error) {
	return "P-1", "Judul", nil
}
func (f *fakeTimeEntryRepo) Notify(_ context.Context, _ db.Executor, userID, _, notifType, _, _, _ string) error {
	f.notified = append(f.notified, userID+":"+notifType)
	return nil
}
func (f *fakeTimeEntryRepo) LogAudit(_ context.Context, _ db.Executor, _, actorRole, action, _, _ string, _, after map[string]any) error {
	f.audits = append(f.audits, action)
	f.auditRoles = append(f.auditRoles, actorRole)
	f.auditStates = append(f.auditStates, after)
	return nil
}

type fakeTimeEntryTasks struct{ notOwner bool }

func (fakeTimeEntryTasks) GetProjectID(context.Context, db.Executor, string) (string, error) {
	return "p1", nil
}
func (f fakeTimeEntryTasks) IsOwner(context.Context, db.Executor, string, string) (bool, error) {
	return !f.notOwner, nil
}

type fakeTimeEntryProjects struct{}

func (fakeTimeEntryProjects) GetWorkspaceID(context.Context, db.Executor, string) (string, error) {
	return "ws-1", nil
}
func (fakeTimeEntryProjects) GetAllowEditorStoryPoints(context.Context, db.Executor, string) (bool, error) {
	return false, nil
}

func newTestTimeEntryService(repo *fakeTimeEntryRepo, role string) *TimeEntryService {
	return NewTimeEntryService(repo, fakeTimeEntryTasks{}, fakeTimeEntryProjects{}, &fakePerformanceRoleChecker{role: role}, &fakeSprintProjectRoles{found: false})
}

func TestCreateManualAuditsAndNotifiesApprovers(t *testing.T) {
	repo := &fakeTimeEntryRepo{approvers: []string{"pm-1", "editor-1", "appr-1"}}
	svc := newTestTimeEntryService(repo, "editor")
	start := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	if _, err := svc.CreateManual(context.Background(), nil, "t1", "editor-1", "member", start, start.Add(90*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if len(repo.audits) != 1 || repo.audits[0] != "task.time_entry_added" {
		t.Fatalf("audit = %v", repo.audits)
	}
	// pembuat entri (editor-1) tidak diberi notifikasi sendiri
	if len(repo.notified) != 2 || repo.notified[0] != "pm-1:timesheet_pending" || repo.notified[1] != "appr-1:timesheet_pending" {
		t.Fatalf("notified = %v", repo.notified)
	}
}

func TestRejectNotifiesOwnerAndAudits(t *testing.T) {
	repo := &fakeTimeEntryRepo{entry: &repository.TimeEntry{ID: "e1", TaskID: "t1", UserID: "editor-1", EntryType: "manual", IsApproved: nil}}
	svc := newTestTimeEntryService(repo, "project_manager")
	if err := svc.Reject(context.Background(), nil, "e1", "pm-1", "member", "jam tidak sesuai"); err != nil {
		t.Fatal(err)
	}
	if len(repo.audits) != 1 || repo.audits[0] != "task.time_entry_rejected" {
		t.Fatalf("audit = %v", repo.audits)
	}
	if len(repo.notified) != 1 || repo.notified[0] != "editor-1:timesheet_rejected" {
		t.Fatalf("notified = %v", repo.notified)
	}
	// approve oleh non-approver ditolak, tanpa audit/notif tambahan
	nonApprover := newTestTimeEntryService(repo, "editor")
	if err := nonApprover.Approve(context.Background(), nil, "e1", "x", "member"); !errors.Is(err, domain.ErrForbidden) || len(repo.audits) != 1 {
		t.Fatalf("approve non-approver err=%v audits=%v", err, repo.audits)
	}
}

func TestUpdateRejectedEntryReturnsToApprovalQueue(t *testing.T) {
	rejected := false
	start := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	repo := &fakeTimeEntryRepo{
		approvers: []string{"pm-1"},
		entry:     &repository.TimeEntry{ID: "e1", TaskID: "t1", UserID: "editor-1", EntryType: "manual", IsApproved: &rejected},
	}
	svc := newTestTimeEntryService(repo, "editor")
	// bukan pemilik -> forbidden
	if _, err := svc.UpdateManual(context.Background(), nil, "e1", "orang-lain", "member", start, start.Add(time.Hour), nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-owner harus ditolak, got %v", err)
	}
	if _, err := svc.UpdateManual(context.Background(), nil, "e1", "editor-1", "member", start, start.Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	if !repo.updated || len(repo.audits) != 1 || repo.audits[0] != "task.time_entry_updated" {
		t.Fatalf("updated=%v audits=%v", repo.updated, repo.audits)
	}
	if len(repo.notified) != 1 || repo.notified[0] != "pm-1:timesheet_pending" {
		t.Fatalf("approver harus diberi tahu lagi: %v", repo.notified)
	}
	// entri sudah disetujui -> tidak bisa diubah
	approved := true
	repo.entry = &repository.TimeEntry{ID: "e1", TaskID: "t1", UserID: "editor-1", EntryType: "manual", IsApproved: &approved}
	if _, err := svc.UpdateManual(context.Background(), nil, "e1", "editor-1", "member", start, start.Add(time.Hour), nil); !errors.Is(err, domain.ErrTimeEntryAlreadyApproved) {
		t.Fatalf("approved harus terkunci, got %v", err)
	}
}

func TestManualEntryRejectsOver24Hours(t *testing.T) {
	repo := &fakeTimeEntryRepo{}
	svc := newTestTimeEntryService(repo, "editor")
	start := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	if _, err := svc.CreateManual(context.Background(), nil, "t1", "u1", "member", start, start.Add(25*time.Hour), nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("25 jam harus ditolak, got %v", err)
	}
	if _, err := svc.CreateManual(context.Background(), nil, "t1", "u1", "member", start, start.Add(24*time.Hour), nil); err != nil {
		t.Fatalf("tepat 24 jam harus lolos: %v", err)
	}
}
