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

func TestParseSprintCSV(t *testing.T) {
	data := []byte("\ufeffCode,Name,start_date,End_Date,GOAL,Status\nspr-01,Sprint 1,01/10/2026,14/10/2026,Rilis,done\nSPR-02,Sprint 2\n")
	rows, err := parseSprintCSV(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 || rows[0].Code != "spr-01" || rows[0].EndDate != "14/10/2026" || rows[0].Goal != "Rilis" || rows[1].Name != "Sprint 2" {
		t.Errorf("rows = %+v", rows)
	}
	if rows[0].RowNum != 2 || rows[1].RowNum != 3 {
		t.Errorf("row numbers = %d,%d", rows[0].RowNum, rows[1].RowNum)
	}

	if _, err := parseSprintCSV([]byte("name,goal\nSprint 1,x\n")); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("kolom code hilang: err = %v, want ErrInvalidInput", err)
	}
}

func TestValidateSprintRows(t *testing.T) {
	existing := []repository.Sprint{{Code: "SPR-01", Name: "Sprint Lama", Status: "done"}}
	rows := []SprintImportRow{
		{RowNum: 2, Code: "spr-02", Name: "Sprint 2", StartDate: "01/10/2026", EndDate: "14/10/2026", SprintStatus: "done"}, // valid, tanggal DD/MM/YYYY
		{RowNum: 3, Code: "SPR-01", Name: "Baru"},                                                     // code sudah ada di project
		{RowNum: 4, Code: "SPR-02", Name: "Lain"},                                                     // code kembar dalam berkas
		{RowNum: 5, Code: "SPR-05", Name: "sprint lama"},                                              // nama sudah ada (case-insensitive)
		{RowNum: 6, Code: "SPR-06", Name: "Tanggal Rusak", StartDate: "31-12-2026"},                   // format tanggal
		{RowNum: 7, Code: "SPR-07", Name: "Terbalik", StartDate: "10/10/2026", EndDate: "01/10/2026"}, // end < start
		{RowNum: 8, Code: "SPR-08", Name: "Status Aneh", SprintStatus: "berjalan"},                    // status tidak dikenal
		{RowNum: 9, Code: "SPR-09", Name: "Aktif Satu", SprintStatus: "active"},                       // valid (project belum punya aktif)
		{RowNum: 10, Code: "SPR-10", Name: "Aktif Dua", SprintStatus: "ACTIVE"},                       // active kedua dalam berkas
		{RowNum: 11, Code: "", Name: "Tanpa Kode"},
		{RowNum: 12, Code: "SPR 12", Name: "Spasi"}, // format code
		{RowNum: 13, Code: "SPR-13", Name: "Default Backlog"},
	}
	validateSprintRows(rows, existing)

	want := map[int]string{2: "valid", 3: "skipped", 4: "skipped", 5: "skipped", 6: "skipped", 7: "skipped", 8: "skipped", 9: "valid", 10: "skipped", 11: "skipped", 12: "skipped", 13: "valid"}
	for i := range rows {
		r := &rows[i]
		if r.Status != want[r.RowNum] {
			t.Errorf("baris %d: status = %q (%s), want %q", r.RowNum, r.Status, r.Reason, want[r.RowNum])
		}
		if r.Status == "skipped" && r.Reason == "" {
			t.Errorf("baris %d: dilewati tanpa alasan", r.RowNum)
		}
	}
	if rows[0].Code != "SPR-02" || rows[0].StartDate != "2026-10-01" || rows[0].EndDate != "2026-10-14" {
		t.Errorf("normalisasi baris 2 = %+v", rows[0])
	}
	if rows[11].SprintStatus != "backlog" {
		t.Errorf("status kosong harus jadi backlog, got %q", rows[11].SprintStatus)
	}
}

func TestValidateSprintRows_ProjectAlreadyHasActive(t *testing.T) {
	rows := []SprintImportRow{{RowNum: 2, Code: "SPR-09", Name: "Aktif", SprintStatus: "active"}}
	validateSprintRows(rows, []repository.Sprint{{Code: "SPR-01", Name: "Berjalan", Status: "active"}})
	if rows[0].Status != "skipped" {
		t.Errorf("status = %q, want skipped (project sudah punya sprint aktif)", rows[0].Status)
	}
}

// ---- Execute / otorisasi ----

type fakePIRepo struct {
	imp       *repository.ProjectImport
	completed bool
	success   int
	failed    int
}

func (f *fakePIRepo) Create(_ context.Context, _ db.Executor, projectID, kind, importedBy, actorRole, filename string, totalRows int, rowResults []byte) (string, error) {
	f.imp = &repository.ProjectImport{ID: "imp-1", ProjectID: projectID, Kind: kind, ImportedBy: importedBy, Filename: filename, Status: "pending", TotalRows: totalRows, RowResults: rowResults}
	return "imp-1", nil
}
func (f *fakePIRepo) Get(_ context.Context, _ db.Executor, _ string) (*repository.ProjectImport, error) {
	if f.imp == nil {
		return nil, domain.ErrCSVImportNotFound
	}
	return f.imp, nil
}
func (f *fakePIRepo) ListByProject(_ context.Context, _ db.Executor, _ string) ([]repository.ProjectImport, error) {
	return nil, nil
}
func (f *fakePIRepo) Complete(_ context.Context, _ db.Executor, imp *repository.ProjectImport, success, failed int, rowResults []byte, _, _, _ string) error {
	f.completed, f.success, f.failed = true, success, failed
	imp.Status, imp.RowResults = "completed", rowResults
	return nil
}
func (f *fakePIRepo) AuditReportDownload(_ context.Context, _ db.Executor, _ *repository.ProjectImport, _, _, _ string) error {
	return nil
}

type fakePISprints struct {
	existing []repository.Sprint
	created  []string
}

func (f *fakePISprints) List(_ context.Context, _ db.Executor, _ string) ([]repository.Sprint, error) {
	return f.existing, nil
}
func (f *fakePISprints) CreateImported(_ context.Context, _ db.Executor, _, code, _, _ string, _, _ *time.Time, _ *string, _, _, _ string) (*repository.Sprint, error) {
	f.created = append(f.created, code)
	return &repository.Sprint{Code: code}, nil
}

type fakePIProjects struct{ isPM bool }

func (f *fakePIProjects) GetWorkspaceID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return "ws-1", nil
}
func (f *fakePIProjects) IsPM(_ context.Context, _ db.Executor, _, _ string) (bool, error) {
	return f.isPM, nil
}

func newPIService(repo *fakePIRepo, sprints *fakePISprints, isPM bool, wsRole string) *ProjectImportService {
	return NewProjectImportService(repo, sprints, &fakePIProjects{isPM: isPM}, &fakeSprintRBAC{role: wsRole})
}

const piCSV = "code,name,status\nSPR-01,Sprint 1,done\nSPR-02,Sprint 2,backlog\nSPR-03,,backlog\n"

func TestProjectImport_ValidateAndExecute(t *testing.T) {
	repo, sprints := &fakePIRepo{}, &fakePISprints{}
	svc := newPIService(repo, sprints, true, "project_manager")

	res, err := svc.Validate(context.Background(), nil, "p1", "sprint", "sprint.csv", []byte(piCSV), "pm-1", "")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Total != 3 || res.ValidN != 2 || res.SkippedN != 1 {
		t.Fatalf("hasil = %+v", res)
	}
	if len(sprints.created) != 0 {
		t.Fatalf("pratinjau tidak boleh menulis sprint, created = %v", sprints.created)
	}

	if _, err := svc.Execute(context.Background(), nil, "p1", res.ImportID, "pm-1", ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(sprints.created) != 2 || !repo.completed || repo.success != 2 || repo.failed != 1 {
		t.Errorf("created=%v completed=%v success=%d failed=%d", sprints.created, repo.completed, repo.success, repo.failed)
	}

	// eksekusi ulang ditolak
	if _, err := svc.Execute(context.Background(), nil, "p1", res.ImportID, "pm-1", ""); !errors.Is(err, domain.ErrCSVImportAlreadyStarted) {
		t.Errorf("eksekusi kedua: err = %v, want ErrCSVImportAlreadyStarted", err)
	}
}

func TestProjectImport_Execute_RechecksAgainstCurrentState(t *testing.T) {
	repo, sprints := &fakePIRepo{}, &fakePISprints{}
	svc := newPIService(repo, sprints, true, "project_manager")
	res, err := svc.Validate(context.Background(), nil, "p1", "sprint", "sprint.csv", []byte(piCSV), "pm-1", "")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// Antara pratinjau dan eksekusi, seseorang membuat sprint dengan code SPR-01.
	sprints.existing = []repository.Sprint{{Code: "SPR-01", Name: "Dibuat Manual", Status: "backlog"}}
	if _, err := svc.Execute(context.Background(), nil, "p1", res.ImportID, "pm-1", ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(sprints.created) != 1 || sprints.created[0] != "SPR-02" || repo.failed != 2 {
		t.Errorf("created=%v failed=%d, want hanya SPR-02 dibuat dan 2 dilewati", sprints.created, repo.failed)
	}
}

func TestProjectImport_ForbiddenForEditorAndWrongKind(t *testing.T) {
	svc := newPIService(&fakePIRepo{}, &fakePISprints{}, false, "editor")
	if _, err := svc.Validate(context.Background(), nil, "p1", "sprint", "x.csv", []byte(piCSV), "ed-1", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("editor: err = %v, want ErrForbidden", err)
	}

	pm := newPIService(&fakePIRepo{}, &fakePISprints{}, true, "project_manager")
	if _, err := pm.Validate(context.Background(), nil, "p1", "task", "x.csv", []byte(piCSV), "pm-1", ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("kind task belum didukung: err = %v, want ErrInvalidInput", err)
	}

	aw := newPIService(&fakePIRepo{}, &fakePISprints{}, false, "admin_workspace")
	if _, err := aw.Validate(context.Background(), nil, "p1", "sprint", "x.csv", []byte(piCSV), "aw-1", ""); err != nil {
		t.Errorf("admin workspace harus boleh: %v", err)
	}
}
