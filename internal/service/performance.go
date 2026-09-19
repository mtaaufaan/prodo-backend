// Package service -- PerformanceService (EPIC 12 Reporting & Analytics,
// US-075/076/077/078, desain "Performance Dashboard.dc.html"). Cakupan
// Project Manager (satu project miliknya) dan Admin Workspace (lintas
// project workspace) -- Group Admin sudah ada terpisah
// (GroupPerformanceService, US-079), tidak dipakai ulang di sini karena
// gerbang otorisasinya beda total (PM-of-project vs GA-of-group).
//
// Toggle RAW/WEIGHTED di desain HANYA benar-benar mengubah Completion Rate
// (kartu atas + per member) -- On-Time Rate per priority TIDAK dihitung
// ulang varian weighted (secara matematis identik dengan raw kalau dihitung
// PER priority: bobot yang sama di pembilang dan penyebut saling meniadakan,
// dikonfirmasi dari logika referensi desain sendiri), jadi hanya satu varian
// dikembalikan.
package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type performanceRepository interface {
	ListTasks(ctx context.Context, exec db.Executor, workspaceID, projectID string, since *time.Time) ([]repository.PerfTask, error)
	ListStatusSessions(ctx context.Context, exec db.Executor, workspaceID, projectID string, since *time.Time) ([]repository.PerfSession, error)
	FirstWorkStarted(ctx context.Context, exec db.Executor, workspaceID, projectID string, since *time.Time) (map[string]time.Time, error)
	ListAssignees(ctx context.Context, exec db.Executor, workspaceID, projectID string, since *time.Time) ([]repository.PerfAssignee, error)
	ListPicPhases(ctx context.Context, exec db.Executor, workspaceID, projectID string, since *time.Time) ([]repository.PerfPicPhase, error)
}

// performanceProjectResolver -- reuse ProjectRepository.GetWorkspaceID.
type performanceProjectResolver interface {
	GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error)
}

// performancePMChecker -- reuse ProjectRepository.GetPMUserID.
type performancePMChecker interface {
	GetPMUserID(ctx context.Context, exec db.Executor, projectID string) (string, error)
}

type PerformanceService struct {
	repo     performanceRepository
	projects performanceProjectResolver
	pm       performancePMChecker
	rbac     sprintWorkspaceRoleChecker
}

func NewPerformanceService(repo performanceRepository, projects performanceProjectResolver, pm performancePMChecker, rbac sprintWorkspaceRoleChecker) *PerformanceService {
	return &PerformanceService{repo: repo, projects: projects, pm: pm, rbac: rbac}
}

func (s *PerformanceService) authorizeWorkspace(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return nil
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("service.authorizeWorkspace: %w", err)
	}
	if role != "admin_workspace" {
		return fmt.Errorf("service.authorizeWorkspace: %w", domain.ErrForbidden)
	}
	return nil
}

// authorizeProject -- PM pemilik project ini, ATAU Admin Workspace/GA/PA
// (Full mode, sama gerbang authorizeWorkspace).
func (s *PerformanceService) authorizeProject(ctx context.Context, exec db.Executor, projectID, workspaceID, actorID, actorRole string) error {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return nil
	}
	pmID, err := s.pm.GetPMUserID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("service.authorizeProject: %w", err)
	}
	if pmID != "" && pmID == actorID {
		return nil
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("service.authorizeProject: %w", err)
	}
	if role != "admin_workspace" {
		return fmt.Errorf("service.authorizeProject: %w", domain.ErrForbidden)
	}
	return nil
}

// DashboardForWorkspace -- GET /workspaces/:wsId/performance, AW-only.
// projectID kosong = agregat lintas seluruh project workspace; terisi =
// dipersempit ke satu project (LINGKUP "SEMUA PROJECT" vs per-project di
// desain), tanpa validasi tambahan project itu benar milik workspace ini --
// perfScopeClause sudah menyaring keduanya di WHERE yang sama, project ID
// dari workspace lain otomatis menghasilkan hasil kosong, tidak bocor data.
func (s *PerformanceService) DashboardForWorkspace(ctx context.Context, exec db.Executor, workspaceID, projectID string, rangeDays int, actorID, actorRole string) (*DashboardResult, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.DashboardForWorkspace: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return nil, err
	}
	return s.dashboard(ctx, exec, workspaceID, projectID, rangeDays)
}

// DashboardForProject -- GET /projects/:projectId/performance, PM (pemilik
// project ini) atau Full mode.
func (s *PerformanceService) DashboardForProject(ctx context.Context, exec db.Executor, projectID string, rangeDays int, actorID, actorRole string) (*DashboardResult, error) {
	if projectID == "" {
		return nil, fmt.Errorf("service.DashboardForProject: %w", domain.ErrInvalidInput)
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.DashboardForProject: %w", err)
	}
	if err := s.authorizeProject(ctx, exec, projectID, workspaceID, actorID, actorRole); err != nil {
		return nil, err
	}
	return s.dashboard(ctx, exec, workspaceID, projectID, rangeDays)
}

type DashboardResult struct {
	ScopeTotal             int
	ScopeDone              int
	CompletionRateRaw      float64
	CompletionRateWeighted float64
	OnTime                 []OnTimeStat
	NoDueCount             int
	BacklogHealth          []BacklogStat
	Cycle                  []CycleStat
	Overdue                []OverdueTask
	OverdueTotal           int
	OverdueCritical        int
	Members                []MemberStat
	FlowEfficiencyPct      *float64
	LeadTimeHours          float64
	CycleTimeAvgHours      *float64
	RegressionRatePct      float64
	RegressedTasks         int
	RegressionByStatus     []RegressionStat
	Bottleneck             []BottleneckStat
	HandoffDelay           []HandoffStat
}

type OnTimeStat struct {
	Priority    string
	DoneWithDue int
	OnTime      int
	RatePct     *float64 // nil = belum ada task selesai berdue-date untuk priority ini
}

type BacklogStat struct {
	Priority string
	Count    int
}

type CycleStat struct {
	StatusName    string
	AvgByPriority map[string]float64 // jam, Active Time (work_started_at -> exited_at/NOW)
	AvgHours      float64            // rata-rata seluruh sesi status ini, semua priority
}

type OverdueTask struct {
	TaskCode    *string
	Title       string
	ProjectName string
	Priority    string
	DueDate     time.Time
	DaysLate    int
}

type MemberStat struct {
	UserID                 string
	UserName               string
	ActiveByPriority       map[string]int
	TotalCount             int
	DoneCount              int
	CompletionRateRaw      float64
	CompletionRateWeighted float64
	AvgCompletionHours     *float64
	AckAvgHours            *float64
	AckPendingCount        int
}

type RegressionStat struct {
	StatusName   string
	Regressions  int
	Sessions     int
	TasksThrough int
	RatePct      float64
}

type BottleneckStat struct {
	StatusName     string
	AvgTotalHours  float64
	AvgQueueHours  float64
	AvgActiveHours float64
	SessionCount   int
}

type HandoffStat struct {
	ProjectID    string
	ProjectName  string
	AvgHours     *float64
	PendingCount int
}

// dashboard -- inti perhitungan, dipanggil kedua entry point di atas
// setelah otorisasi lolos. Semua query difilter rentang waktu yang sama
// (rangeDaysToSince, reuse dari group_performance.go -- package sama).
func (s *PerformanceService) dashboard(ctx context.Context, exec db.Executor, workspaceID, projectID string, rangeDays int) (*DashboardResult, error) {
	since := rangeDaysToSince(rangeDays)

	tasks, err := s.repo.ListTasks(ctx, exec, workspaceID, projectID, since)
	if err != nil {
		return nil, fmt.Errorf("service.dashboard: %w", err)
	}
	sessions, err := s.repo.ListStatusSessions(ctx, exec, workspaceID, projectID, since)
	if err != nil {
		return nil, fmt.Errorf("service.dashboard: %w", err)
	}
	firstWork, err := s.repo.FirstWorkStarted(ctx, exec, workspaceID, projectID, since)
	if err != nil {
		return nil, fmt.Errorf("service.dashboard: %w", err)
	}
	assignees, err := s.repo.ListAssignees(ctx, exec, workspaceID, projectID, since)
	if err != nil {
		return nil, fmt.Errorf("service.dashboard: %w", err)
	}
	picPhases, err := s.repo.ListPicPhases(ctx, exec, workspaceID, projectID, since)
	if err != nil {
		return nil, fmt.Errorf("service.dashboard: %w", err)
	}

	now := time.Now()
	result := &DashboardResult{ScopeTotal: len(tasks)}

	// --- Completion Rate + On-Time Rate + Backlog Health + Overdue ---
	var sumWeight, doneWeighted float64
	onTimePool := map[string]int{}
	onTimeOK := map[string]int{}
	backlogCount := map[string]int{}
	overdueAll := make([]OverdueTask, 0)
	overdueCritical := 0
	for i := range tasks {
		t := &tasks[i]
		w := priorityWeight[t.Priority]
		sumWeight += w
		isDone := t.StatusName == "DONE"
		if isDone {
			result.ScopeDone++
			doneWeighted += w
		}
		if t.DueDate == nil {
			result.NoDueCount++
		} else {
			if isDone {
				onTimePool[t.Priority]++
				if t.CompletedAt != nil && !t.CompletedAt.After(*t.DueDate) {
					onTimeOK[t.Priority]++
				}
			}
			if !isDone && t.DueDate.Before(now) {
				daysLate := int(now.Sub(*t.DueDate).Hours()/24) + 1
				if t.Priority == "critical" {
					overdueCritical++
				}
				overdueAll = append(overdueAll, OverdueTask{
					TaskCode: t.TaskCode, Title: t.Title, ProjectName: t.ProjectName,
					Priority: t.Priority, DueDate: *t.DueDate, DaysLate: daysLate,
				})
			}
		}
		if t.StatusName == "BACKLOG" && t.Completeness != nil && *t.Completeness == "incomplete" {
			backlogCount[t.Priority]++
		}
	}
	if result.ScopeTotal > 0 {
		result.CompletionRateRaw = float64(result.ScopeDone) / float64(result.ScopeTotal) * 100
	}
	if sumWeight > 0 {
		result.CompletionRateWeighted = doneWeighted / sumWeight * 100
	}
	for _, p := range priorityOrder {
		pool := onTimePool[p]
		var rate *float64
		if pool > 0 {
			v := float64(onTimeOK[p]) / float64(pool) * 100
			rate = &v
		}
		result.OnTime = append(result.OnTime, OnTimeStat{Priority: p, DoneWithDue: pool, OnTime: onTimeOK[p], RatePct: rate})
		result.BacklogHealth = append(result.BacklogHealth, BacklogStat{Priority: p, Count: backlogCount[p]})
	}
	sort.Slice(overdueAll, func(i, j int) bool { return overdueAll[i].DaysLate > overdueAll[j].DaysLate })
	result.OverdueTotal = len(overdueAll)
	result.OverdueCritical = overdueCritical
	if len(overdueAll) > 8 {
		overdueAll = overdueAll[:8]
	}
	result.Overdue = overdueAll

	// --- Cycle Time (per-status Active Time) + Bottleneck + Regression per status ---
	type statusAgg struct {
		activeByPriority map[string][]float64
		totalHours       []float64
		queueHours       []float64
		activeHours      []float64
		sessions         int
		regressions      int
		tasksThrough     map[string]bool
	}
	byStatus := map[string]*statusAgg{}
	regressedTaskIDs := map[string]bool{}
	var totalActiveHoursAllStatuses float64
	for i := range sessions {
		sess := &sessions[i]
		end := now
		if sess.ExitedAt != nil {
			end = *sess.ExitedAt
		}
		totalHours := end.Sub(sess.EnteredAt).Hours()
		var activeHours, queueHours float64
		if sess.WorkStartedAt != nil {
			queueHours = sess.WorkStartedAt.Sub(sess.EnteredAt).Hours()
			activeHours = end.Sub(*sess.WorkStartedAt).Hours()
		} else {
			queueHours = totalHours
		}
		totalActiveHoursAllStatuses += activeHours

		agg := byStatus[sess.StatusName]
		if agg == nil {
			agg = &statusAgg{activeByPriority: map[string][]float64{}, tasksThrough: map[string]bool{}}
			byStatus[sess.StatusName] = agg
		}
		agg.activeByPriority[sess.Priority] = append(agg.activeByPriority[sess.Priority], activeHours)
		agg.totalHours = append(agg.totalHours, totalHours)
		agg.queueHours = append(agg.queueHours, queueHours)
		agg.activeHours = append(agg.activeHours, activeHours)
		agg.sessions++
		agg.tasksThrough[sess.TaskID] = true
		if sess.IsRegression {
			agg.regressions++
			regressedTaskIDs[sess.TaskID] = true
		}
	}
	for status, agg := range byStatus {
		cs := CycleStat{StatusName: status, AvgByPriority: map[string]float64{}}
		for p, vals := range agg.activeByPriority {
			cs.AvgByPriority[p] = mean(vals)
		}
		cs.AvgHours = mean(agg.activeHours)
		result.Cycle = append(result.Cycle, cs)

		result.Bottleneck = append(result.Bottleneck, BottleneckStat{
			StatusName: status, AvgTotalHours: mean(agg.totalHours), AvgQueueHours: mean(agg.queueHours),
			AvgActiveHours: mean(agg.activeHours), SessionCount: agg.sessions,
		})

		rate := 0.0
		if agg.sessions > 0 {
			rate = float64(agg.regressions) / float64(agg.sessions) * 100
		}
		result.RegressionByStatus = append(result.RegressionByStatus, RegressionStat{
			StatusName: status, Regressions: agg.regressions, Sessions: agg.sessions,
			TasksThrough: len(agg.tasksThrough), RatePct: rate,
		})
	}
	sort.Slice(result.Cycle, func(i, j int) bool { return result.Cycle[i].AvgHours > result.Cycle[j].AvgHours })
	sort.Slice(result.Bottleneck, func(i, j int) bool { return result.Bottleneck[i].AvgTotalHours > result.Bottleneck[j].AvgTotalHours })
	sort.Slice(result.RegressionByStatus, func(i, j int) bool {
		return result.RegressionByStatus[i].RatePct > result.RegressionByStatus[j].RatePct
	})
	result.RegressedTasks = len(regressedTaskIDs)
	if result.ScopeTotal > 0 {
		result.RegressionRatePct = float64(result.RegressedTasks) / float64(result.ScopeTotal) * 100
	}

	// --- Flow Efficiency: Lead Time (task selesai) + Active Time (semua sesi) ---
	var leadHours, cycleSum float64
	var cycleN int
	for i := range tasks {
		t := &tasks[i]
		if t.StatusName != "DONE" || t.CompletedAt == nil {
			continue
		}
		leadHours += t.CompletedAt.Sub(t.CreatedAt).Hours()
		if fw, ok := firstWork[t.TaskID]; ok {
			cycleSum += t.CompletedAt.Sub(fw).Hours()
			cycleN++
		}
	}
	result.LeadTimeHours = leadHours
	if leadHours > 0 {
		v := totalActiveHoursAllStatuses / leadHours * 100
		result.FlowEfficiencyPct = &v
	}
	if cycleN > 0 {
		v := cycleSum / float64(cycleN)
		result.CycleTimeAvgHours = &v
	}

	// --- Member Performance ---
	type memberAgg struct {
		name                    string
		activeByPriority        map[string]int
		total, done             int
		sumWeight, doneWeighted float64
		completionHours         []float64
	}
	members := map[string]*memberAgg{}
	for i := range assignees {
		a := &assignees[i]
		m := members[a.UserID]
		if m == nil {
			m = &memberAgg{name: a.UserName, activeByPriority: map[string]int{}}
			members[a.UserID] = m
		}
		m.total++
		w := priorityWeight[a.Priority]
		m.sumWeight += w
		isDone := a.StatusName == "DONE"
		if isDone {
			m.done++
			m.doneWeighted += w
			if a.CompletedAt != nil {
				if fw, ok := firstWork[a.TaskID]; ok {
					m.completionHours = append(m.completionHours, a.CompletedAt.Sub(fw).Hours())
				}
			}
		} else {
			m.activeByPriority[a.Priority]++
		}
	}
	ackByUser := map[string][]float64{}
	ackPendingByUser := map[string]int{}
	for i := range picPhases {
		h := &picPhases[i]
		if h.AcknowledgedAt != nil {
			ackByUser[h.UserID] = append(ackByUser[h.UserID], h.AcknowledgedAt.Sub(h.ActivatedAt).Hours())
		} else {
			ackPendingByUser[h.UserID]++
		}
	}
	for userID, m := range members {
		stat := MemberStat{
			UserID: userID, UserName: m.name, ActiveByPriority: m.activeByPriority,
			TotalCount: m.total, DoneCount: m.done, AckPendingCount: ackPendingByUser[userID],
		}
		if m.total > 0 {
			stat.CompletionRateRaw = float64(m.done) / float64(m.total) * 100
		}
		if m.sumWeight > 0 {
			stat.CompletionRateWeighted = m.doneWeighted / m.sumWeight * 100
		}
		if len(m.completionHours) > 0 {
			v := mean(m.completionHours)
			stat.AvgCompletionHours = &v
		}
		if acks := ackByUser[userID]; len(acks) > 0 {
			v := mean(acks)
			stat.AckAvgHours = &v
		}
		result.Members = append(result.Members, stat)
	}
	sort.Slice(result.Members, func(i, j int) bool {
		return activeTotal(&result.Members[i]) > activeTotal(&result.Members[j])
	})

	// --- Handoff Delay per project ---
	type handoffAgg struct {
		name    string
		hours   []float64
		pending int
	}
	byProject := map[string]*handoffAgg{}
	for i := range picPhases {
		h := &picPhases[i]
		agg := byProject[h.ProjectID]
		if agg == nil {
			agg = &handoffAgg{name: h.ProjectName}
			byProject[h.ProjectID] = agg
		}
		if h.AcknowledgedAt != nil {
			agg.hours = append(agg.hours, h.AcknowledgedAt.Sub(h.ActivatedAt).Hours())
		} else {
			agg.pending++
		}
	}
	for projID, agg := range byProject {
		stat := HandoffStat{ProjectID: projID, ProjectName: agg.name, PendingCount: agg.pending}
		if len(agg.hours) > 0 {
			v := mean(agg.hours)
			stat.AvgHours = &v
		}
		result.HandoffDelay = append(result.HandoffDelay, stat)
	}
	sort.Slice(result.HandoffDelay, func(i, j int) bool { return result.HandoffDelay[i].ProjectName < result.HandoffDelay[j].ProjectName })

	return result, nil
}

var priorityOrder = []string{"critical", "high", "medium", "low"}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func activeTotal(m *MemberStat) int {
	total := 0
	for _, v := range m.ActiveByPriority {
		total += v
	}
	return total
}
