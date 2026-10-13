package service

import (
	"context"
	"testing"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 1, 0, time.UTC) }

func minutesFor(entries []EstimateEntry, user, task string) int {
	n := 0
	for _, e := range entries {
		if e.UserID == user && e.TaskID == task {
			n += e.Minutes
		}
	}
	return n
}

// Contoh PRODO-023: IN PROGRESS 9-12 Feb (Sen-Kam) oleh Joyse = 3 hari kerja,
// UNDER REVIEW selesai di hari yang sama oleh Taufan = 1 hari.
func TestEstimateEntriesPRODO023(t *testing.T) {
	got := EstimateEntries([]EstimatePhase{
		{TaskID: "t", TaskCode: "PRODO-023", Phase: "IN PROGRESS", Start: d(2026, 2, 9), End: d(2026, 2, 12), PICs: []string{"joyse"}},
		{TaskID: "t", TaskCode: "PRODO-023", Phase: "UNDER REVIEW", Start: d(2026, 2, 12), End: d(2026, 2, 12), PICs: []string{"taufan"}},
	}, d(2026, 3, 1))
	if minutesFor(got, "joyse", "t") != 24*60 || minutesFor(got, "taufan", "t") != 8*60 {
		t.Fatalf("joyse=%d taufan=%d, want 1440/480", minutesFor(got, "joyse", "t"), minutesFor(got, "taufan", "t"))
	}
	if len(got) != 4 {
		t.Fatalf("entri = %d, want 4 (satu per hari)", len(got))
	}
}

func TestEstimateSkipsWeekendAndMovesWeekendStart(t *testing.T) {
	// Jumat 6 Feb -> Selasa 10 Feb (eksklusif): Jum 6 dan Sen 9 saja.
	got := EstimateEntries([]EstimatePhase{{TaskID: "a", TaskCode: "A", Phase: "IN PROGRESS", Start: d(2026, 2, 6), End: d(2026, 2, 10), PICs: []string{"u"}}}, d(2026, 3, 1))
	if len(got) != 2 {
		t.Fatalf("entri = %d, want 2 (Sabtu-Minggu dilewati)", len(got))
	}
	for _, e := range got {
		if w := e.StartedAt.In(time.UTC).Weekday(); w == time.Saturday || w == time.Sunday {
			t.Fatalf("entri jatuh di akhir pekan: %v", e.StartedAt)
		}
	}
	// Fase dimulai Sabtu 7 Feb dan selesai hari yang sama -> 1 hari, geser ke Senin 9 Feb.
	one := EstimateEntries([]EstimatePhase{{TaskID: "b", TaskCode: "B", Phase: "UNDER REVIEW", Start: d(2026, 2, 7), End: d(2026, 2, 7), PICs: []string{"u"}}}, d(2026, 3, 1))
	if len(one) != 1 || one[0].StartedAt.In(time.UTC).Day() != 9 {
		t.Fatalf("fase akhir pekan harus 1 entri di Senin 9: %+v", one)
	}
}

func TestEstimateCapsLongPhaseAtTenWorkdays(t *testing.T) {
	got := EstimateEntries([]EstimatePhase{{TaskID: "a", TaskCode: "A", Phase: "IN PROGRESS", Start: d(2026, 1, 5), End: d(2026, 3, 5), PICs: []string{"u"}}}, d(2026, 4, 1))
	if len(got) != 10 || minutesFor(got, "u", "a") != 10*8*60 {
		t.Fatalf("entri=%d menit=%d, want 10 hari / 4800 menit", len(got), minutesFor(got, "u", "a"))
	}
}

func TestEstimateSplitsAcrossPICsAndParallelTasks(t *testing.T) {
	// Dua PIC pada satu fase satu hari: masing-masing 4 jam.
	two := EstimateEntries([]EstimatePhase{{TaskID: "a", TaskCode: "A", Phase: "IN PROGRESS", Start: d(2026, 2, 9), End: d(2026, 2, 9), PICs: []string{"x", "y"}}}, d(2026, 3, 1))
	if minutesFor(two, "x", "a") != 240 || minutesFor(two, "y", "a") != 240 {
		t.Fatalf("dua PIC harus 240/240, got %d/%d", minutesFor(two, "x", "a"), minutesFor(two, "y", "a"))
	}
	// Satu orang memegang dua task paralel pada hari yang sama: 8 jam dibagi dua, tanpa tumpang tindih.
	par := EstimateEntries([]EstimatePhase{
		{TaskID: "a", TaskCode: "A", Phase: "IN PROGRESS", Start: d(2026, 2, 9), End: d(2026, 2, 9), PICs: []string{"u"}},
		{TaskID: "b", TaskCode: "B", Phase: "IN PROGRESS", Start: d(2026, 2, 9), End: d(2026, 2, 9), PICs: []string{"u"}},
	}, d(2026, 3, 1))
	if len(par) != 2 || par[0].Minutes != 240 || par[1].Minutes != 240 {
		t.Fatalf("task paralel harus 240/240: %+v", par)
	}
	if !par[1].StartedAt.Equal(par[0].EndedAt) {
		t.Fatalf("entri harus berurutan tanpa tumpang tindih: %v vs %v", par[0].EndedAt, par[1].StartedAt)
	}
	if par[0].StartedAt.Hour() != 9 {
		t.Fatalf("entri harian mulai 09:00 WIB, got %v", par[0].StartedAt)
	}
}

func TestEstimateOpenPhaseRunsUntilNowAndSkipsNoPIC(t *testing.T) {
	now := d(2026, 2, 12) // Kamis
	got := EstimateEntries([]EstimatePhase{
		{TaskID: "a", TaskCode: "A", Phase: "IN PROGRESS", Start: d(2026, 2, 9), PICs: []string{"u"}},            // End kosong -> sampai now: 9,10,11
		{TaskID: "b", TaskCode: "B", Phase: "IN PROGRESS", Start: d(2026, 2, 9), End: d(2026, 2, 11), PICs: nil}, // tanpa PIC
	}, now)
	if len(got) != 3 || minutesFor(got, "u", "a") != 1440 {
		t.Fatalf("fase berjalan harus 3 hari: %+v", got)
	}
}

// ---- Recalculate ----

type fakeEstimateRepo struct {
	sources  []repository.EstimateSourceRow
	replaced []repository.EstimateInsert
	logged   int
}

func (f *fakeEstimateRepo) ListSources(context.Context, db.Executor, string) ([]repository.EstimateSourceRow, error) {
	return f.sources, nil
}
func (f *fakeEstimateRepo) ReplaceEstimates(_ context.Context, _ db.Executor, _ string, e []repository.EstimateInsert) error {
	f.replaced = e
	return nil
}
func (f *fakeEstimateRepo) LogRecalculation(_ context.Context, _ db.Executor, _, _, _, _, _ string, _, _, _ int) error {
	f.logged++
	return nil
}

func TestRecalculateSkipsIncompleteBacklogAndCountsTotals(t *testing.T) {
	end := d(2026, 2, 12)
	repo := &fakeEstimateRepo{sources: []repository.EstimateSourceRow{
		{TaskID: "t1", TaskCode: "P-1", Phase: "BACKLOG", Start: d(2026, 2, 2), End: &[]time.Time{d(2026, 2, 5)}[0], PICs: []string{"taufan"}}, // 2,3,4 = 3 hari
		{TaskID: "t1", TaskCode: "P-1", Phase: "IN PROGRESS", Start: d(2026, 2, 9), End: &end, PICs: []string{"joyse"}},                        // 9,10,11 = 3 hari
		{TaskID: "t2", TaskCode: "P-2", Phase: "BACKLOG", Start: d(2026, 2, 2), End: nil, PICs: []string{"taufan"}},                            // belum lengkap -> dilewati
	}}
	svc := NewTimesheetEstimateService(repo)
	svc.now = func() time.Time { return d(2026, 3, 1) }
	res, err := svc.Recalculate(context.Background(), nil, "p1", "ws", "PRODO", "pm", "project_manager")
	if err != nil {
		t.Fatal(err)
	}
	if res.Entries != 6 || res.Tasks != 1 || res.Minutes != 6*480 {
		t.Fatalf("hasil = %+v, want 6 entri / 1 task / 2880 menit", res)
	}
	if len(repo.replaced) != 6 || repo.logged != 1 {
		t.Fatalf("replaced=%d logged=%d", len(repo.replaced), repo.logged)
	}
	for _, e := range repo.replaced {
		if e.TaskID == "t2" {
			t.Fatal("task backlog belum lengkap tidak boleh diestimasi")
		}
	}
}

// ---- Hook import task -> estimasi jam ----

type fakeImportEstimator struct{ calls int }

func (f *fakeImportEstimator) Recalculate(context.Context, db.Executor, string, string, string, string, string) (*EstimateResult, error) {
	f.calls++
	return &EstimateResult{}, nil
}

func TestProjectImportTaskExecuteTriggersEstimateRecalculation(t *testing.T) {
	repo, sprints, tasks := &fakePIRepo{}, &fakePISprints{existing: []repository.Sprint{{ID: "sp-1", Code: "SPR-01", Status: "active"}}}, &fakePITasks{}
	est := &fakeImportEstimator{}
	svc := NewProjectImportService(repo, sprints, &fakePIProjects{isPM: true}, &fakeSprintRBAC{role: "project_manager"}, defaultPIStatuses(), defaultPIMembers(), tasks)
	svc.SetTimesheetEstimator(est)

	res, err := svc.Validate(context.Background(), nil, "p1", "task", "task.csv", []byte("title,status\nTask Satu,IN PROGRESS\n"), "pm-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(context.Background(), nil, "p1", res.ImportID, "pm-1", ""); err != nil {
		t.Fatal(err)
	}
	if est.calls != 1 {
		t.Fatalf("import task harus memicu hitung ulang estimasi sekali, got %d", est.calls)
	}

	// import sprint tidak menyentuh estimasi
	est2 := &fakeImportEstimator{}
	sprintSvc := NewProjectImportService(&fakePIRepo{}, &fakePISprints{}, &fakePIProjects{isPM: true}, &fakeSprintRBAC{role: "project_manager"}, defaultPIStatuses(), defaultPIMembers(), &fakePITasks{})
	sprintSvc.SetTimesheetEstimator(est2)
	r2, err := sprintSvc.Validate(context.Background(), nil, "p1", "sprint", "s.csv", []byte("code,name,start_date,end_date\nSPR-09,Sprint X,01/10/2026,14/10/2026\n"), "pm-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sprintSvc.Execute(context.Background(), nil, "p1", r2.ImportID, "pm-1", ""); err != nil {
		t.Fatal(err)
	}
	if est2.calls != 0 {
		t.Fatalf("import sprint tidak boleh memicu estimasi, got %d", est2.calls)
	}
}
