package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// myTimesheetRepository -- interface didefinisikan di consumer, diimplementasikan repository.MyTimesheetRepository.
type myTimesheetRepository interface {
	ListTasks(ctx context.Context, exec db.Executor, userID, workspaceID string, weekStart, weekEnd time.Time) ([]repository.MyHoursTask, error)
	ListEntries(ctx context.Context, exec db.Executor, userID, workspaceID string, limit, offset int) ([]repository.MyHoursEntry, int, error)
}

// MyTimesheetService -- Timesheet Editor (S6E-10, IG-141): statistik 4 kartu, task milik saya, dan riwayat.
type MyTimesheetService struct{ repo myTimesheetRepository }

func NewMyTimesheetService(repo myTimesheetRepository) *MyTimesheetService {
	return &MyTimesheetService{repo: repo}
}

type MyHoursStats struct {
	LoggedHours   float64
	EstimateHours float64
	OverCount     int
	ActiveCount   int
}

type MyHoursTaskView struct {
	repository.MyHoursTask
	LoggedHours float64
	WeekHours   float64
	IsActive    bool
}

type MyHoursEntryView struct {
	repository.MyHoursEntry
	Hours float64
}

type MyHours struct {
	Stats MyHoursStats
	Tasks []MyHoursTaskView
	Items []MyHoursEntryView
	Total int
}

const (
	defaultMyHoursPerPage = 10
	maxMyHoursPerPage     = 50
)

func hoursOf(minutes int) float64 { return math.Round(float64(minutes)/60*100) / 100 }

// weekBounds -- Senin 00:00 WIB minggu berjalan dan Senin berikutnya.
func weekBounds(now time.Time) (start, end time.Time) {
	n := now.In(wib)
	day := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, wib)
	start = day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	return start, start.AddDate(0, 0, 7)
}

// isActiveStatus -- task masih bisa dicatat jamnya: bukan DONE/CANCELED.
func isActiveStatus(name string) bool { return name != "DONE" && name != "CANCELED" }

// Get menangani GET /me/timesheets?workspace_id=&view=week|history&page=&per_page=. Statistik dihitung dari
// task MILIK user (assignee atau PIC aktif); `tasks` hanya memuat task aktif milik user atau yang punya jam minggu
// ini (cukup untuk panel dan modal Catat Waktu). view=week tidak mengirim riwayat.
func (s *MyTimesheetService) Get(ctx context.Context, exec db.Executor, userID, workspaceID, view string, page, perPage int, now time.Time) (*MyHours, error) {
	if userID == "" || workspaceID == "" || (view != "week" && view != "history") {
		return nil, fmt.Errorf("service.MyTimesheetGet: %w", domain.ErrInvalidInput)
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = defaultMyHoursPerPage
	}
	if perPage > maxMyHoursPerPage {
		perPage = maxMyHoursPerPage
	}
	ws, we := weekBounds(now)
	tasks, err := s.repo.ListTasks(ctx, exec, userID, workspaceID, ws, we)
	if err != nil {
		return nil, fmt.Errorf("service.MyTimesheetGet: %w", err)
	}
	out := &MyHours{Tasks: make([]MyHoursTaskView, 0), Items: make([]MyHoursEntryView, 0)}
	var loggedMin int
	for i := range tasks {
		t := &tasks[i]
		active := t.IsMine && isActiveStatus(t.StatusName)
		if t.IsMine {
			loggedMin += t.LoggedMinutes
			if t.EstimateHours != nil {
				out.Stats.EstimateHours += *t.EstimateHours
				if *t.EstimateHours > 0 && float64(t.LoggedMinutes)/60 > *t.EstimateHours {
					out.Stats.OverCount++
				}
			}
			if active {
				out.Stats.ActiveCount++
			}
		}
		if active || t.WeekMinutes > 0 {
			out.Tasks = append(out.Tasks, MyHoursTaskView{MyHoursTask: *t, LoggedHours: hoursOf(t.LoggedMinutes), WeekHours: hoursOf(t.WeekMinutes), IsActive: active})
		}
	}
	out.Stats.LoggedHours = hoursOf(loggedMin)
	out.Stats.EstimateHours = math.Round(out.Stats.EstimateHours*100) / 100
	if view == "history" {
		entries, total, err := s.repo.ListEntries(ctx, exec, userID, workspaceID, perPage, (page-1)*perPage)
		if err != nil {
			return nil, fmt.Errorf("service.MyTimesheetGet: %w", err)
		}
		out.Total = total
		for i := range entries {
			out.Items = append(out.Items, MyHoursEntryView{MyHoursEntry: entries[i], Hours: hoursOf(entries[i].Minutes)})
		}
	}
	return out, nil
}
