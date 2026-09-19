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

type fakePerformanceRepo struct {
	tasks     []repository.PerfTask
	sessions  []repository.PerfSession
	firstWork map[string]time.Time
	assignees []repository.PerfAssignee
	picPhases []repository.PerfPicPhase
}

func (f *fakePerformanceRepo) ListTasks(_ context.Context, _ db.Executor, _, _ string, _ *time.Time) ([]repository.PerfTask, error) {
	return f.tasks, nil
}
func (f *fakePerformanceRepo) ListStatusSessions(_ context.Context, _ db.Executor, _, _ string, _ *time.Time) ([]repository.PerfSession, error) {
	return f.sessions, nil
}
func (f *fakePerformanceRepo) FirstWorkStarted(_ context.Context, _ db.Executor, _, _ string, _ *time.Time) (map[string]time.Time, error) {
	if f.firstWork == nil {
		return map[string]time.Time{}, nil
	}
	return f.firstWork, nil
}
func (f *fakePerformanceRepo) ListAssignees(_ context.Context, _ db.Executor, _, _ string, _ *time.Time) ([]repository.PerfAssignee, error) {
	return f.assignees, nil
}
func (f *fakePerformanceRepo) ListPicPhases(_ context.Context, _ db.Executor, _, _ string, _ *time.Time) ([]repository.PerfPicPhase, error) {
	return f.picPhases, nil
}

type fakePerformanceProjectResolver struct{ workspaceID string }

func (f *fakePerformanceProjectResolver) GetWorkspaceID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return f.workspaceID, nil
}

type fakePerformancePMChecker struct{ pmUserID string }

func (f *fakePerformancePMChecker) GetPMUserID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return f.pmUserID, nil
}

type fakePerformanceRoleChecker struct{ role string }

func (f *fakePerformanceRoleChecker) GetMemberRole(_ context.Context, _ db.Executor, _, _ string) (string, error) {
	return f.role, nil
}

func newTestPerformanceService(repo *fakePerformanceRepo, pmUserID, role string) *PerformanceService {
	if repo == nil {
		repo = &fakePerformanceRepo{}
	}
	return NewPerformanceService(repo, &fakePerformanceProjectResolver{workspaceID: "ws-1"}, &fakePerformancePMChecker{pmUserID: pmUserID}, &fakePerformanceRoleChecker{role: role})
}

// --- authorization ---

func TestDashboardForProject_AllowsOwningPM(t *testing.T) {
	svc := newTestPerformanceService(nil, "pm-1", "editor")
	_, err := svc.DashboardForProject(context.Background(), nil, "proj-1", 30, "pm-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDashboardForProject_ForbidsOtherPM(t *testing.T) {
	svc := newTestPerformanceService(nil, "pm-1", "editor")
	_, err := svc.DashboardForProject(context.Background(), nil, "proj-1", 30, "someone-else", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden (bukan PM project ini, bukan AW)", err)
	}
}

func TestDashboardForProject_AllowsAdminWorkspace(t *testing.T) {
	svc := newTestPerformanceService(nil, "pm-1", "admin_workspace")
	_, err := svc.DashboardForProject(context.Background(), nil, "proj-1", 30, "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDashboardForProject_AllowsGroupAdminFullMode(t *testing.T) {
	svc := newTestPerformanceService(nil, "pm-1", "editor")
	_, err := svc.DashboardForProject(context.Background(), nil, "proj-1", 30, "ga-1", "group_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDashboardForWorkspace_ForbidsNonAdminWorkspace(t *testing.T) {
	svc := newTestPerformanceService(nil, "", "editor")
	_, err := svc.DashboardForWorkspace(context.Background(), nil, "ws-1", "", 30, "user-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

func TestDashboardForWorkspace_AllowsAdminWorkspace(t *testing.T) {
	svc := newTestPerformanceService(nil, "", "admin_workspace")
	_, err := svc.DashboardForWorkspace(context.Background(), nil, "ws-1", "", 30, "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- aggregation ---

func priTask(taskID, priority, status string, due, completed *time.Time, completeness *string) repository.PerfTask {
	return repository.PerfTask{
		TaskID: taskID, ProjectID: "proj-1", ProjectName: "Project 1",
		Priority: priority, StatusName: status, CreatedAt: time.Now().Add(-72 * time.Hour),
		DueDate: due, CompletedAt: completed, Completeness: completeness,
	}
}

func TestDashboard_CompletionRateRawAndWeighted(t *testing.T) {
	repo := &fakePerformanceRepo{tasks: []repository.PerfTask{
		priTask("t1", "critical", "DONE", nil, nil, nil), // weight 5, done
		priTask("t2", "low", "BACKLOG", nil, nil, nil),   // weight 1, not done
	}}
	svc := newTestPerformanceService(repo, "", "admin_workspace")
	result, err := svc.DashboardForWorkspace(context.Background(), nil, "ws-1", "", 0, "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CompletionRateRaw != 50 {
		t.Errorf("CompletionRateRaw = %v, want 50 (1 of 2 done)", result.CompletionRateRaw)
	}
	wantWeighted := 5.0 / 6.0 * 100 // done weight 5 / total weight (5+1)
	if diff := result.CompletionRateWeighted - wantWeighted; diff > 0.01 || diff < -0.01 {
		t.Errorf("CompletionRateWeighted = %v, want %v", result.CompletionRateWeighted, wantWeighted)
	}
}

func TestDashboard_OnTimeRate_ExcludesTasksWithoutDueDate(t *testing.T) {
	due := time.Now().Add(-24 * time.Hour)
	completedOnTime := due.Add(-1 * time.Hour)
	repo := &fakePerformanceRepo{tasks: []repository.PerfTask{
		priTask("t1", "high", "DONE", &due, &completedOnTime, nil),
		priTask("t2", "high", "DONE", nil, nil, nil), // tanpa due date -- dikecualikan
	}}
	svc := newTestPerformanceService(repo, "", "admin_workspace")
	result, err := svc.DashboardForWorkspace(context.Background(), nil, "ws-1", "", 0, "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NoDueCount != 1 {
		t.Errorf("NoDueCount = %d, want 1", result.NoDueCount)
	}
	var highStat *OnTimeStat
	for i := range result.OnTime {
		if result.OnTime[i].Priority == "high" {
			highStat = &result.OnTime[i]
		}
	}
	if highStat == nil || highStat.DoneWithDue != 1 || highStat.RatePct == nil || *highStat.RatePct != 100 {
		t.Errorf("OnTime[high] = %+v, want DoneWithDue=1 RatePct=100", highStat)
	}
}

func TestDashboard_OverdueTask_SortedByLatenessAndExcludesDone(t *testing.T) {
	dueRecent := time.Now().Add(-24 * time.Hour)
	dueOld := time.Now().Add(-240 * time.Hour)
	repo := &fakePerformanceRepo{tasks: []repository.PerfTask{
		priTask("t1", "critical", "IN PROGRESS", &dueRecent, nil, nil),
		priTask("t2", "low", "IN PROGRESS", &dueOld, nil, nil),
		priTask("t3", "critical", "DONE", &dueOld, nil, nil), // done -- TIDAK overdue meski due lewat
	}}
	svc := newTestPerformanceService(repo, "", "admin_workspace")
	result, err := svc.DashboardForWorkspace(context.Background(), nil, "ws-1", "", 0, "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OverdueTotal != 2 {
		t.Fatalf("OverdueTotal = %d, want 2", result.OverdueTotal)
	}
	if result.OverdueCritical != 1 {
		t.Errorf("OverdueCritical = %d, want 1", result.OverdueCritical)
	}
	if len(result.Overdue) != 2 || result.Overdue[0].DaysLate < result.Overdue[1].DaysLate {
		t.Errorf("Overdue tidak terurut menurun berdasarkan DaysLate: %+v", result.Overdue)
	}
}

func TestDashboard_BacklogHealth_OnlyIncompleteBacklogTasks(t *testing.T) {
	incomplete := "incomplete"
	complete := "complete"
	repo := &fakePerformanceRepo{tasks: []repository.PerfTask{
		priTask("t1", "high", "BACKLOG", nil, nil, &incomplete),
		priTask("t2", "high", "BACKLOG", nil, nil, &complete),
		priTask("t3", "high", "IN PROGRESS", nil, nil, &incomplete), // bukan BACKLOG -- tidak dihitung
	}}
	svc := newTestPerformanceService(repo, "", "admin_workspace")
	result, err := svc.DashboardForWorkspace(context.Background(), nil, "ws-1", "", 0, "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, b := range result.BacklogHealth {
		if b.Priority == "high" && b.Count != 1 {
			t.Errorf("BacklogHealth[high] = %d, want 1", b.Count)
		}
	}
}

func session(taskID, priority, status string, entered time.Time, workStarted, exited *time.Time, isRegression bool) repository.PerfSession {
	return repository.PerfSession{
		TaskID: taskID, ProjectID: "proj-1", Priority: priority, StatusName: status,
		EnteredAt: entered, WorkStartedAt: workStarted, ExitedAt: exited, IsRegression: isRegression,
	}
}

func TestDashboard_Bottleneck_SplitsQueueAndActiveTime(t *testing.T) {
	entered := time.Now().Add(-10 * time.Hour)
	workStarted := entered.Add(4 * time.Hour) // queue = 4h
	exited := workStarted.Add(6 * time.Hour)  // active = 6h, total = 10h
	repo := &fakePerformanceRepo{sessions: []repository.PerfSession{
		session("t1", "high", "IN PROGRESS", entered, &workStarted, &exited, false),
	}}
	svc := newTestPerformanceService(repo, "", "admin_workspace")
	result, err := svc.DashboardForWorkspace(context.Background(), nil, "ws-1", "", 0, "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Bottleneck) != 1 {
		t.Fatalf("Bottleneck = %+v, want 1 entry", result.Bottleneck)
	}
	b := result.Bottleneck[0]
	if diff := b.AvgQueueHours - 4; diff > 0.01 || diff < -0.01 {
		t.Errorf("AvgQueueHours = %v, want ~4", b.AvgQueueHours)
	}
	if diff := b.AvgActiveHours - 6; diff > 0.01 || diff < -0.01 {
		t.Errorf("AvgActiveHours = %v, want ~6", b.AvgActiveHours)
	}
	if diff := b.AvgTotalHours - 10; diff > 0.01 || diff < -0.01 {
		t.Errorf("AvgTotalHours = %v, want ~10", b.AvgTotalHours)
	}
}

func TestDashboard_Bottleneck_NeverStarted_AllQueueNoActive(t *testing.T) {
	entered := time.Now().Add(-5 * time.Hour)
	exited := entered.Add(5 * time.Hour)
	repo := &fakePerformanceRepo{sessions: []repository.PerfSession{
		session("t1", "high", "BACKLOG", entered, nil, &exited, false), // tidak pernah "Mulai Pengerjaan"
	}}
	svc := newTestPerformanceService(repo, "", "admin_workspace")
	result, err := svc.DashboardForWorkspace(context.Background(), nil, "ws-1", "", 0, "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b := result.Bottleneck[0]
	if b.AvgActiveHours != 0 {
		t.Errorf("AvgActiveHours = %v, want 0 (tidak pernah mulai kerja)", b.AvgActiveHours)
	}
	if diff := b.AvgQueueHours - 5; diff > 0.01 || diff < -0.01 {
		t.Errorf("AvgQueueHours = %v, want ~5 (seluruh durasi adalah antrian)", b.AvgQueueHours)
	}
}

func TestDashboard_RegressionRate_TaskLevelAndPerStatus(t *testing.T) {
	entered := time.Now().Add(-5 * time.Hour)
	exited := entered.Add(1 * time.Hour)
	repo := &fakePerformanceRepo{
		tasks: []repository.PerfTask{
			priTask("t1", "high", "IN PROGRESS", nil, nil, nil),
			priTask("t2", "high", "IN PROGRESS", nil, nil, nil),
		},
		sessions: []repository.PerfSession{
			session("t1", "high", "IN PROGRESS", entered, nil, &exited, true),  // regresi
			session("t1", "high", "IN PROGRESS", entered, nil, &exited, false), // sesi normal task yang sama
			session("t2", "high", "IN PROGRESS", entered, nil, &exited, false),
		},
	}
	svc := newTestPerformanceService(repo, "", "admin_workspace")
	result, err := svc.DashboardForWorkspace(context.Background(), nil, "ws-1", "", 0, "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RegressedTasks != 1 {
		t.Errorf("RegressedTasks = %d, want 1 (t1 saja, dihitung sekali walau py 2 sesi)", result.RegressedTasks)
	}
	wantTaskRate := 1.0 / 2.0 * 100 // 1 dari 2 task
	if diff := result.RegressionRatePct - wantTaskRate; diff > 0.01 || diff < -0.01 {
		t.Errorf("RegressionRatePct = %v, want %v", result.RegressionRatePct, wantTaskRate)
	}
	if len(result.RegressionByStatus) != 1 {
		t.Fatalf("RegressionByStatus = %+v, want 1 status", result.RegressionByStatus)
	}
	rs := result.RegressionByStatus[0]
	if rs.Sessions != 3 || rs.Regressions != 1 {
		t.Errorf("RegressionByStatus[0] = %+v, want Sessions=3 Regressions=1 (rate sesi, bukan task)", rs)
	}
}

func TestDashboard_FlowEfficiency_ActiveOverLead(t *testing.T) {
	created := time.Now().Add(-20 * time.Hour)
	completed := time.Now().Add(-10 * time.Hour) // Lead Time = 10h
	workStarted := created.Add(2 * time.Hour)
	exited := workStarted.Add(5 * time.Hour) // Active Time = 5h
	repo := &fakePerformanceRepo{
		tasks: []repository.PerfTask{
			priTask("t1", "high", "DONE", nil, &completed, nil),
		},
		sessions: []repository.PerfSession{
			session("t1", "high", "IN PROGRESS", created, &workStarted, &exited, false),
		},
	}
	repo.tasks[0].CreatedAt = created
	svc := newTestPerformanceService(repo, "", "admin_workspace")
	result, err := svc.DashboardForWorkspace(context.Background(), nil, "ws-1", "", 0, "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := result.LeadTimeHours - 10; diff > 0.01 || diff < -0.01 {
		t.Errorf("LeadTimeHours = %v, want ~10", result.LeadTimeHours)
	}
	if result.FlowEfficiencyPct == nil {
		t.Fatalf("FlowEfficiencyPct nil, want terisi")
	}
	want := 5.0 / 10.0 * 100
	if diff := *result.FlowEfficiencyPct - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("FlowEfficiencyPct = %v, want %v", *result.FlowEfficiencyPct, want)
	}
}

func TestDashboard_MemberAckRate_ExcludesPendingFromAverage(t *testing.T) {
	activated := time.Now().Add(-30 * time.Hour)
	acked := activated.Add(6 * time.Hour) // 6 jam
	repo := &fakePerformanceRepo{picPhases: []repository.PerfPicPhase{
		{ProjectID: "proj-1", ProjectName: "Project 1", UserID: "u1", UserName: "Budi", ActivatedAt: activated, AcknowledgedAt: &acked},
		{ProjectID: "proj-1", ProjectName: "Project 1", UserID: "u1", UserName: "Budi", ActivatedAt: activated, AcknowledgedAt: nil}, // masih pending
	}}
	svc := newTestPerformanceService(repo, "", "admin_workspace")
	result, err := svc.DashboardForWorkspace(context.Background(), nil, "ws-1", "", 0, "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// User u1 tidak punya baris di ListAssignees, jadi tidak muncul di Members --
	// tes ini fokus ke HandoffDelay (agregat per project, sumbernya sama picPhases).
	if len(result.HandoffDelay) != 1 {
		t.Fatalf("HandoffDelay = %+v, want 1 project", result.HandoffDelay)
	}
	h := result.HandoffDelay[0]
	if h.PendingCount != 1 {
		t.Errorf("PendingCount = %d, want 1", h.PendingCount)
	}
	if h.AvgHours == nil || *h.AvgHours < 5.99 || *h.AvgHours > 6.01 {
		t.Errorf("AvgHours = %v, want ~6 (pending dikecualikan dari rata-rata)", h.AvgHours)
	}
}
