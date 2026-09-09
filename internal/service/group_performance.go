// Package service -- GroupPerformanceService (Performance Dashboard Lintas
// Organisasi, Track S4G, desain "GA Kinerja Grup.dc.html", US-079/S4G-25).
// Dibangun forward-pull SETELAH Task Management Core (Phase 1-4) selesai --
// lihat implementation_gaps.md IG-46 untuk kenapa fitur ini awalnya
// dijadwalkan mengembalikan agregat kosong/nol; sekarang tabel
// tasks/task_status_sessions sudah ada, angka di sini REAL, bukan stub.
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

// priorityWeight -- bobot Completion Rate WEIGHTED (desain: CRITICAL lebih
// berat dari LOW, task penting yang belum selesai menurunkan skor lebih
// banyak) -- sama skala dengan mock desain asli (W di GA Kinerja
// Grup.dc.html), bukan angka baru yang dikarang ulang.
var priorityWeight = map[string]float64{"critical": 5, "high": 3, "medium": 2, "low": 1}

// wipStatuses -- status yang dihitung untuk Bottleneck & Flow (desain
// STATUSES) -- DONE dikecualikan, tidak ada "hunian" yang relevan setelah
// task selesai.
var wipStatuses = []string{"BACKLOG", "IN PROGRESS", "UNDER REVIEW", "BLOCKED"}

// groupPerformanceRepository -- interface didefinisikan di consumer, §3.9.
type groupPerformanceRepository interface {
	ListTaskMetrics(ctx context.Context, exec db.Executor, groupID, orgID string, since *time.Time) ([]repository.TaskMetric, error)
	ListStatusDwell(ctx context.Context, exec db.Executor, groupID, orgID string, since *time.Time) ([]repository.StatusDwell, error)
}

// groupPerformanceOrgLister -- reuse OrganizationRepository.List (sudah
// mengembalikan WorkspaceCount per org, tidak perlu query terpisah).
type groupPerformanceOrgLister interface {
	List(ctx context.Context, exec db.Executor, groupID string) ([]repository.Organization, int64, error)
}

// groupPerformanceAuthorizer -- reuse OrganizationRepository, pola sama
// GroupAuditService.
type groupPerformanceAuthorizer interface {
	IsGroupAdminOfGroup(ctx context.Context, exec db.Executor, userID, groupID string) (bool, error)
}

type OrgPerformance struct {
	OrgID                  string
	OrgName                string
	WorkspaceCount         int
	TotalTasks             int
	CompletionRateRaw      float64
	CompletionRateWeighted float64
	OnTimeRateByPriority   map[string]*float64 // nil = belum ada task selesai berdue-date untuk priority itu
	OverdueCount           int
	OverdueCriticalCount   int
	Bottleneck             []StatusBottleneck // urut menurun (paling lama huni duluan)
}

type StatusBottleneck struct {
	StatusName string
	AvgDays    float64
}

type GroupPerformanceResult struct {
	CompletionRateRaw      float64
	CompletionRateWeighted float64
	TotalTasks             int
	OverdueCount           int
	OverdueCriticalCount   int
	BottleneckStatusName   string // "" kalau tidak ada data sama sekali
	BottleneckOrgName      string
	BottleneckAvgDays      float64
	Organizations          []OrgPerformance
}

type GroupPerformanceService struct {
	repo groupPerformanceRepository
	orgs groupPerformanceOrgLister
	auth groupPerformanceAuthorizer
}

func NewGroupPerformanceService(repo groupPerformanceRepository, orgs groupPerformanceOrgLister, auth groupPerformanceAuthorizer) *GroupPerformanceService {
	return &GroupPerformanceService{repo: repo, orgs: orgs, auth: auth}
}

func (s *GroupPerformanceService) authorizeGroup(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) error {
	if actorRole == "platform_admin" {
		return nil
	}
	isGA, err := s.auth.IsGroupAdminOfGroup(ctx, exec, actorID, groupID)
	if err != nil {
		return fmt.Errorf("service.authorizeGroup: %w", err)
	}
	if !isGA {
		return fmt.Errorf("service.authorizeGroup: %w", domain.ErrForbidden)
	}
	return nil
}

// rangeDaysToSince -- 0/negatif = SEMUA (tidak difilter).
func rangeDaysToSince(days int) *time.Time {
	if days <= 0 {
		return nil
	}
	t := time.Now().AddDate(0, 0, -days)
	return &t
}

// Summary -- GET /groups/:groupId/performance. orgID kosong = seluruh
// organisasi aktif grup ini. Organisasi TANPA task sama sekali dalam
// rentang tetap tampil dengan metrik nol (bukan dihilangkan dari tabel) --
// AC S4G-25: "kembalikan agregat kosong/nol yang wajar", digeneralisasi ke
// level per-organisasi.
func (s *GroupPerformanceService) Summary(ctx context.Context, exec db.Executor, groupID, orgID string, rangeDays int, actorID, actorRole string) (*GroupPerformanceResult, error) {
	if groupID == "" {
		return nil, fmt.Errorf("service.Summary: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return nil, err
	}

	allOrgs, _, err := s.orgs.List(ctx, exec, groupID)
	if err != nil {
		return nil, fmt.Errorf("service.Summary: %w", err)
	}
	shownOrgs := make([]repository.Organization, 0, len(allOrgs))
	for i := range allOrgs {
		o := &allOrgs[i]
		if o.DeactivatedAt != nil {
			continue
		}
		if orgID != "" && o.ID != orgID {
			continue
		}
		shownOrgs = append(shownOrgs, *o)
	}

	since := rangeDaysToSince(rangeDays)
	tasks, err := s.repo.ListTaskMetrics(ctx, exec, groupID, orgID, since)
	if err != nil {
		return nil, fmt.Errorf("service.Summary: %w", err)
	}
	dwell, err := s.repo.ListStatusDwell(ctx, exec, groupID, orgID, since)
	if err != nil {
		return nil, fmt.Errorf("service.Summary: %w", err)
	}

	tasksByOrg := make(map[string][]repository.TaskMetric)
	for _, t := range tasks {
		tasksByOrg[t.OrgID] = append(tasksByOrg[t.OrgID], t)
	}
	dwellByOrg := make(map[string][]repository.StatusDwell)
	for _, d := range dwell {
		dwellByOrg[d.OrgID] = append(dwellByOrg[d.OrgID], d)
	}

	result := &GroupPerformanceResult{Organizations: make([]OrgPerformance, 0, len(shownOrgs))}
	now := time.Now()

	for i := range shownOrgs {
		o := &shownOrgs[i]
		perf := aggregateOrg(o, tasksByOrg[o.ID], dwellByOrg[o.ID], now)
		result.Organizations = append(result.Organizations, perf)
		result.TotalTasks += perf.TotalTasks
		result.OverdueCount += perf.OverdueCount
		result.OverdueCriticalCount += perf.OverdueCriticalCount
		if len(perf.Bottleneck) > 0 && perf.Bottleneck[0].AvgDays > result.BottleneckAvgDays {
			result.BottleneckAvgDays = perf.Bottleneck[0].AvgDays
			result.BottleneckStatusName = perf.Bottleneck[0].StatusName
			result.BottleneckOrgName = perf.OrgName
		}
	}

	// Completion rate grup -- POOLED (total done / total task), bukan
	// rata-rata persentase per-org, supaya org besar tidak sama bobotnya
	// dengan org kecil.
	var doneRaw, doneWeighted, sumWeight float64
	for _, t := range tasks {
		if orgID != "" && t.OrgID != orgID {
			continue
		}
		w := priorityWeight[t.Priority]
		sumWeight += w
		if t.StatusName == "DONE" {
			doneRaw++
			doneWeighted += w
		}
	}
	if result.TotalTasks > 0 {
		result.CompletionRateRaw = doneRaw / float64(result.TotalTasks) * 100
	}
	if sumWeight > 0 {
		result.CompletionRateWeighted = doneWeighted / sumWeight * 100
	}

	return result, nil
}

func aggregateOrg(o *repository.Organization, tasks []repository.TaskMetric, dwell []repository.StatusDwell, now time.Time) OrgPerformance {
	perf := OrgPerformance{
		OrgID: o.ID, OrgName: o.Name, WorkspaceCount: o.WorkspaceCount, TotalTasks: len(tasks),
		OnTimeRateByPriority: make(map[string]*float64, len(priorityWeight)),
	}

	var sumWeight, doneWeighted, doneRaw float64
	onTimeCount := make(map[string]int)
	onTimeDoneWithDue := make(map[string]int)
	for _, t := range tasks {
		w := priorityWeight[t.Priority]
		sumWeight += w
		isDone := t.StatusName == "DONE"
		if isDone {
			doneRaw++
			doneWeighted += w
		}
		if !isDone && t.DueDate != nil && t.DueDate.Before(now) {
			perf.OverdueCount++
			if t.Priority == "critical" {
				perf.OverdueCriticalCount++
			}
		}
		if isDone && t.DueDate != nil {
			onTimeDoneWithDue[t.Priority]++
			if t.CompletedAt != nil && !t.CompletedAt.After(*t.DueDate) {
				onTimeCount[t.Priority]++
			}
		}
	}
	if perf.TotalTasks > 0 {
		perf.CompletionRateRaw = doneRaw / float64(perf.TotalTasks) * 100
	}
	if sumWeight > 0 {
		perf.CompletionRateWeighted = doneWeighted / sumWeight * 100
	}
	for priority := range priorityWeight {
		total := onTimeDoneWithDue[priority]
		if total == 0 {
			perf.OnTimeRateByPriority[priority] = nil
			continue
		}
		rate := float64(onTimeCount[priority]) / float64(total) * 100
		perf.OnTimeRateByPriority[priority] = &rate
	}

	dwellSum := make(map[string]float64)
	dwellCount := make(map[string]int)
	for _, d := range dwell {
		dwellSum[d.StatusName] += d.DwellDays
		dwellCount[d.StatusName]++
	}
	perf.Bottleneck = make([]StatusBottleneck, 0, len(wipStatuses))
	for _, statusName := range wipStatuses {
		if dwellCount[statusName] == 0 {
			continue
		}
		perf.Bottleneck = append(perf.Bottleneck, StatusBottleneck{
			StatusName: statusName, AvgDays: dwellSum[statusName] / float64(dwellCount[statusName]),
		})
	}
	sort.Slice(perf.Bottleneck, func(i, j int) bool { return perf.Bottleneck[i].AvgDays > perf.Bottleneck[j].AvgDays })

	return perf
}
