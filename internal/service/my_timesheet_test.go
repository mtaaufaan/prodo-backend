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

type fakeMyTimesheetRepo struct {
	tasks       []repository.MyHoursTask
	entries     []repository.MyHoursEntry
	total       int
	gotLimit    int
	gotOffset   int
	entriesCall int
	gotWeek     [2]time.Time
}

func (f *fakeMyTimesheetRepo) ListTasks(_ context.Context, _ db.Executor, _, _ string, ws, we time.Time) ([]repository.MyHoursTask, error) {
	f.gotWeek = [2]time.Time{ws, we}
	return f.tasks, nil
}
func (f *fakeMyTimesheetRepo) ListEntries(_ context.Context, _ db.Executor, _, _ string, limit, offset int) ([]repository.MyHoursEntry, int, error) {
	f.entriesCall++
	f.gotLimit, f.gotOffset = limit, offset
	return f.entries, f.total, nil
}

func est(h float64) *float64 { return &h }

func TestMyTimesheet_StatsAndTaskFilter(t *testing.T) {
	repo := &fakeMyTimesheetRepo{tasks: []repository.MyHoursTask{
		{TaskID: "a", StatusName: "IN PROGRESS", IsMine: true, EstimateHours: est(8), LoggedMinutes: 6 * 60, WeekMinutes: 150},    // aktif, tidak melebihi
		{TaskID: "b", StatusName: "UNDER REVIEW", IsMine: true, EstimateHours: est(8), LoggedMinutes: 11*60 + 30, WeekMinutes: 0}, // aktif, melebihi
		{TaskID: "c", StatusName: "DONE", IsMine: true, EstimateHours: est(5), LoggedMinutes: 5 * 60, WeekMinutes: 0},             // selesai -> tidak aktif, tidak masuk daftar
		{TaskID: "d", StatusName: "CANCELED", IsMine: true, EstimateHours: nil, LoggedMinutes: 0, WeekMinutes: 0},                 // dibatalkan
		{TaskID: "e", StatusName: "IN PROGRESS", IsMine: false, EstimateHours: est(100), LoggedMinutes: 60, WeekMinutes: 60},      // bukan milik, hanya karena jam minggu ini
	}}
	svc := NewMyTimesheetService(repo)
	res, err := svc.Get(context.Background(), nil, "u1", "ws1", "week", 1, 10, logNow)
	if err != nil {
		t.Fatal(err)
	}
	// statistik hanya dari task MILIK: a+b+c (+d), bukan e
	if res.Stats.LoggedHours != 22.5 || res.Stats.EstimateHours != 21 || res.Stats.OverCount != 1 || res.Stats.ActiveCount != 2 {
		t.Fatalf("stats = %+v", res.Stats)
	}
	ids := []string{}
	for _, tk := range res.Tasks {
		ids = append(ids, tk.TaskID)
	}
	if len(ids) != 3 || ids[0] != "a" || ids[1] != "b" || ids[2] != "e" {
		t.Fatalf("tasks = %v, want [a b e] (aktif milik + jam minggu ini)", ids)
	}
	if res.Tasks[0].WeekHours != 2.5 || res.Tasks[0].LoggedHours != 6 || !res.Tasks[0].IsActive {
		t.Fatalf("task a = %+v", res.Tasks[0])
	}
	if res.Tasks[2].IsActive {
		t.Fatal("task bukan milik tidak boleh dianggap aktif")
	}
	if repo.entriesCall != 0 || len(res.Items) != 0 {
		t.Fatal("view=week tidak mengambil riwayat")
	}
	if got := repo.gotWeek[0].In(wib).Format("2006-01-02"); got != "2026-10-12" {
		t.Fatalf("awal minggu = %s", got)
	}
}

func TestMyTimesheet_HistoryPaging(t *testing.T) {
	repo := &fakeMyTimesheetRepo{total: 25, entries: []repository.MyHoursEntry{{ID: "e1", Minutes: 150, EntryDate: "2026-10-14"}}}
	svc := NewMyTimesheetService(repo)
	res, err := svc.Get(context.Background(), nil, "u1", "ws1", "history", 3, 10, logNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 25 || len(res.Items) != 1 || res.Items[0].Hours != 2.5 {
		t.Fatalf("res = %+v", res)
	}
	if repo.gotLimit != 10 || repo.gotOffset != 20 {
		t.Fatalf("limit=%d offset=%d", repo.gotLimit, repo.gotOffset)
	}
	// per_page dijepit, page < 1 dianggap 1
	if _, err := svc.Get(context.Background(), nil, "u1", "ws1", "history", 0, 999, logNow); err != nil {
		t.Fatal(err)
	}
	if repo.gotLimit != 50 || repo.gotOffset != 0 {
		t.Fatalf("clamp: limit=%d offset=%d", repo.gotLimit, repo.gotOffset)
	}
}

func TestMyTimesheet_Validation(t *testing.T) {
	svc := NewMyTimesheetService(&fakeMyTimesheetRepo{})
	for _, tc := range []struct{ ws, view string }{{"", "week"}, {"ws1", "bulan"}, {"ws1", ""}} {
		if _, err := svc.Get(context.Background(), nil, "u1", tc.ws, tc.view, 1, 10, logNow); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("ws=%q view=%q: err = %v", tc.ws, tc.view, err)
		}
	}
}
