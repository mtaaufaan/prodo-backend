package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
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

type fakePIStatuses struct{ list []repository.CustomStatus }

func (f *fakePIStatuses) ListForScope(_ context.Context, _ db.Executor, _, _ string) ([]repository.CustomStatus, error) {
	return f.list, nil
}

type fakePIMembers struct{ list []repository.ProjectMember }

func (f *fakePIMembers) ListAssignableMembers(_ context.Context, _ db.Executor, _ string) ([]repository.ProjectMember, error) {
	return f.list, nil
}

type fakePITasks struct{ created []repository.TaskImportInput }

func (f *fakePITasks) CreateImported(_ context.Context, _ db.Executor, in *repository.TaskImportInput) (taskID, taskCode string, err error) {
	f.created = append(f.created, *in)
	return "task-" + in.Title, "PRJ-" + in.Title, nil
}

func defaultPIStatuses() *fakePIStatuses {
	return &fakePIStatuses{list: []repository.CustomStatus{
		{ID: "st-backlog", Name: "BACKLOG"}, {ID: "st-prog", Name: "IN PROGRESS"}, {ID: "st-done", Name: "DONE"},
		{ID: "st-canceled", Name: "CANCELED"}, {ID: "st-old", Name: "LAMA", IsUndefined: true},
	}}
}

func defaultPIMembers() *fakePIMembers {
	return &fakePIMembers{list: []repository.ProjectMember{
		{UserID: "u-ed", Email: "Editor@Corp.com", Role: "editor"},
		{UserID: "u-pm", Email: "pm@corp.com", Role: "project_manager", IsPM: true},
		{UserID: "u-vw", Email: "viewer@corp.com", Role: "viewer"},
	}}
}

func newPIService(repo *fakePIRepo, sprints *fakePISprints, isPM bool, wsRole string) *ProjectImportService {
	return NewProjectImportService(repo, sprints, &fakePIProjects{isPM: isPM}, &fakeSprintRBAC{role: wsRole},
		defaultPIStatuses(), defaultPIMembers(), &fakePITasks{})
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

// Pratinjau memuat SELURUH baris berkas (paginasi dilakukan FE), bukan 50 pertama.
func TestProjectImport_Validate_PreviewHasAllRows(t *testing.T) {
	var b strings.Builder
	b.WriteString("code,name\n")
	for i := 1; i <= 120; i++ {
		fmt.Fprintf(&b, "SPR-%03d,Sprint %d\n", i, i)
	}
	svc := newPIService(&fakePIRepo{}, &fakePISprints{}, true, "project_manager")
	res, err := svc.Validate(context.Background(), nil, "p1", "sprint", "banyak.csv", []byte(b.String()), "pm-1", "")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	rows, ok := res.Preview.([]SprintImportRow)
	if !ok || len(rows) != 120 || res.Total != 120 {
		t.Errorf("preview ok=%v len=%d total=%d, want 120 baris", ok, len(rows), res.Total)
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
	if _, err := pm.Validate(context.Background(), nil, "p1", "epic", "x.csv", []byte(piCSV), "pm-1", ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("kind tidak dikenal: err = %v, want ErrInvalidInput", err)
	}

	aw := newPIService(&fakePIRepo{}, &fakePISprints{}, false, "admin_workspace")
	if _, err := aw.Validate(context.Background(), nil, "p1", "sprint", "x.csv", []byte(piCSV), "aw-1", ""); err != nil {
		t.Errorf("admin workspace harus boleh: %v", err)
	}
}

// ---- import task (tahap b) ----

func taskEnv() taskImportEnv {
	env := taskImportEnv{statuses: map[string]repository.CustomStatus{}, sprints: map[string]repository.Sprint{}, assignees: map[string]string{"editor@corp.com": "u-ed", "pm@corp.com": "u-pm"}, today: "2026-12-31"}
	for _, st := range defaultPIStatuses().list {
		if !st.IsUndefined {
			st.RequirePic = st.Name != "DONE" && st.Name != "CANCELED"
			env.statuses[st.Name] = st
		}
	}
	env.sprints["SPR-01"] = repository.Sprint{ID: "sp-1", Code: "SPR-01", Status: "active"}
	env.sprints["SPR-00"] = repository.Sprint{ID: "sp-0", Code: "SPR-00", Status: "done"}
	return env
}

func TestParseTaskCSV(t *testing.T) {
	rows, err := parseTaskCSV([]byte("Title*,Status,PRIORITY,assignee,start_date,due_date,sprint,estimate,story_points\nJudul A,done,HIGH,a@x.com,01/10/2026,08/10/2026,spr-01,\"6,5\",3\nJudul B\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 || rows[0].Title != "Judul A" || rows[0].Sprint != "spr-01" || rows[0].Estimate != "6,5" || rows[1].Title != "Judul B" {
		t.Errorf("rows = %+v", rows)
	}
	if _, err := parseTaskCSV([]byte("name,goal\nx,y\n")); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("kolom title hilang: err = %v, want ErrInvalidInput", err)
	}
}

// Excel locale Indonesia menyimpan CSV dengan ";" -- pemisah dideteksi dari header.
func TestParseCSVSemicolonDelimiter(t *testing.T) {
	rows, err := parseTaskCSV([]byte("title;status;assignee;estimate\nJudul A;DONE;\"a@x.com;b@x.com\";6,5\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 || rows[0].Title != "Judul A" || rows[0].Assignee != "a@x.com;b@x.com" || rows[0].Estimate != "6,5" {
		t.Errorf("rows = %+v", rows)
	}
	sprints, err := parseSprintCSV([]byte("code;name\nSPR-01;Sprint 1\n"))
	if err != nil || len(sprints) != 1 || sprints[0].Code != "SPR-01" || sprints[0].Name != "Sprint 1" {
		t.Errorf("sprints = %+v, err = %v", sprints, err)
	}
}

func TestValidateTaskRows(t *testing.T) {
	rows := []TaskImportRow{
		{RowNum: 2, Title: "Task valid lengkap", TaskStatus: "in progress", Priority: "HIGH", Assignee: "EDITOR@corp.com; pm@corp.com;editor@corp.com", StartDate: "01/10/2026", DueDate: "08/10/2026", Sprint: "spr-01", Estimate: "6,5", StoryPoints: "5"},
		{RowNum: 3, Title: "Task minimal"},                                 // default BACKLOG/medium, tanpa assignee
		{RowNum: 4, Title: "ab"},                                           // judul pendek
		{RowNum: 5, Title: "Status ngawur", TaskStatus: "REVIEW QA"},       // status tak ada
		{RowNum: 6, Title: "Status undefined", TaskStatus: "LAMA"},         // status UNDEFINED ditolak
		{RowNum: 7, Title: "Prioritas salah", Priority: "urgent"},          // priority
		{RowNum: 8, Title: "Assignee asing", Assignee: "orang@luar.com"},   // bukan member
		{RowNum: 9, Title: "Assignee viewer", Assignee: "viewer@corp.com"}, // viewer ditolak
		{RowNum: 10, Title: "Tanggal rusak", DueDate: "31-12-2026"},        // format
		{RowNum: 11, Title: "Due sebelum start", StartDate: "10/10/2026", DueDate: "01/10/2026"},
		{RowNum: 12, Title: "Sprint tak ada", Sprint: "SPR-99"},
		{RowNum: 13, Title: "Sprint selesai kerja", Sprint: "SPR-00", TaskStatus: "IN PROGRESS"}, // sprint done + status kerja
		{RowNum: 14, Title: "Sprint selesai done", Sprint: "SPR-00", TaskStatus: "DONE"},         // boleh
		{RowNum: 15, Title: "Estimate rusak", Estimate: "banyak"},
		{RowNum: 16, Title: "SP rusak", StoryPoints: "4"},
		{RowNum: 17, Title: "SP tanda tanya", StoryPoints: "?"},
	}
	validateTaskRows(rows, taskEnv())

	want := map[int]string{2: "valid", 3: "valid", 4: "skipped", 5: "skipped", 6: "skipped", 7: "skipped", 8: "skipped", 9: "skipped", 10: "skipped", 11: "skipped",
		12: "skipped", 13: "skipped", 14: "valid", 15: "skipped", 16: "skipped", 17: "valid"}
	for i := range rows {
		r := &rows[i]
		if r.Status != want[r.RowNum] {
			t.Errorf("baris %d: status = %q (%s), want %q", r.RowNum, r.Status, r.Reason, want[r.RowNum])
		}
		if r.Status == "skipped" && r.Reason == "" {
			t.Errorf("baris %d: dilewati tanpa alasan", r.RowNum)
		}
	}
	r := rows[0]
	if r.TaskStatus != "IN PROGRESS" || r.Priority != "high" || r.Assignee != "editor@corp.com;pm@corp.com" || r.StartDate != "2026-10-01" ||
		r.DueDate != "2026-10-08" || r.Sprint != "SPR-01" || r.Estimate != "6.5" || r.StoryPoints != "5" {
		t.Errorf("normalisasi baris 2 = %+v", r)
	}
	if rows[1].TaskStatus != "BACKLOG" || rows[1].Priority != "medium" {
		t.Errorf("default baris 3 = %+v", rows[1])
	}
	if rows[15].StoryPoints != "" {
		t.Errorf("SP '?' harus kosong, got %q", rows[15].StoryPoints)
	}
}

func TestProjectImport_Task_ValidateAndExecute(t *testing.T) {
	repo, sprints, tasks := &fakePIRepo{}, &fakePISprints{existing: []repository.Sprint{{ID: "sp-1", Code: "SPR-01", Status: "active"}}}, &fakePITasks{}
	svc := NewProjectImportService(repo, sprints, &fakePIProjects{isPM: true}, &fakeSprintRBAC{role: "project_manager"}, defaultPIStatuses(), defaultPIMembers(), tasks)

	csvData := "title,status,priority,assignee,sprint,story_points\nTask Satu,IN PROGRESS,high,editor@corp.com,SPR-01,5\nTask Dua,,,,,\nTask Rusak,STATUS-X,,,,\n"
	res, err := svc.Validate(context.Background(), nil, "p1", "task", "task.csv", []byte(csvData), "pm-1", "")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Total != 3 || res.ValidN != 2 || res.SkippedN != 1 || len(tasks.created) != 0 {
		t.Fatalf("hasil = %+v created=%d", res, len(tasks.created))
	}

	if _, err := svc.Execute(context.Background(), nil, "p1", res.ImportID, "pm-1", ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(tasks.created) != 2 || repo.success != 2 || repo.failed != 1 {
		t.Fatalf("created=%d success=%d failed=%d", len(tasks.created), repo.success, repo.failed)
	}
	first := tasks.created[0]
	if first.StatusID != "st-prog" || first.Priority != "high" || first.SprintID == nil || *first.SprintID != "sp-1" ||
		len(first.AssigneeUserIDs) != 1 || first.AssigneeUserIDs[0] != "u-ed" || first.StoryPoints == nil || *first.StoryPoints != 5 {
		t.Errorf("task pertama = %+v", first)
	}
	second := tasks.created[1]
	if second.StatusID != "st-backlog" || second.Priority != "medium" || second.SprintID != nil || len(second.AssigneeUserIDs) != 0 {
		t.Errorf("task kedua (assignee opsional) = %+v", second)
	}

	report, err := svc.Report(context.Background(), nil, "p1", res.ImportID, true, "pm-1", "")
	if err != nil || !bytes.Contains(report, []byte("STATUS-X")) || bytes.Contains(report, []byte("Task Satu")) {
		t.Errorf("laporan baris dilewati salah: %v\n%s", err, report)
	}
}

func TestProjectImport_Task_RechecksSprintChange(t *testing.T) {
	repo, sprints, tasks := &fakePIRepo{}, &fakePISprints{existing: []repository.Sprint{{ID: "sp-1", Code: "SPR-01", Status: "active"}}}, &fakePITasks{}
	svc := NewProjectImportService(repo, sprints, &fakePIProjects{isPM: true}, &fakeSprintRBAC{role: "project_manager"}, defaultPIStatuses(), defaultPIMembers(), tasks)
	res, err := svc.Validate(context.Background(), nil, "p1", "task", "task.csv", []byte("title,sprint\nTask Satu,SPR-01\nTask Dua,\n"), "pm-1", "")
	if err != nil || res.ValidN != 2 {
		t.Fatalf("Validate: %v %+v", err, res)
	}
	sprints.existing = nil // sprint dihapus sebelum eksekusi
	if _, err := svc.Execute(context.Background(), nil, "p1", res.ImportID, "pm-1", ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(tasks.created) != 1 || tasks.created[0].Title != "Task Dua" || repo.failed != 1 {
		t.Errorf("created=%v failed=%d, want hanya Task Dua", tasks.created, repo.failed)
	}
}

// ---- riwayat tanggal status + PIC per status (tahap c) ----

func TestValidateTaskRows_History(t *testing.T) {
	base := func(status string, mod func(*TaskImportRow)) TaskImportRow {
		r := TaskImportRow{Title: "Task riwayat", TaskStatus: status}
		mod(&r)
		return r
	}
	rows := []TaskImportRow{
		/*2*/ base("DONE", func(r *TaskImportRow) {
			r.CreatedAt, r.InProgressAt, r.UnderReviewAt, r.DoneAt = "28/09/2026", "01/10/2026", "06/10/2026", "08/10/2026"
			r.PicBacklog, r.PicInProgress, r.PicUnderReview = "pm@corp.com", "editor@corp.com", "pm@corp.com"
		}),
		/*3*/ base("IN PROGRESS", func(r *TaskImportRow) { r.InProgressAt = "01/10/2026" }), // valid, tanpa created_at
		/*4*/ base("DONE", func(r *TaskImportRow) { r.InProgressAt = "01/10/2026" }), // DONE tanpa done_at
		/*5*/ base("UNDER REVIEW", func(r *TaskImportRow) { r.UnderReviewAt, r.DoneAt = "02/10/2026", "03/10/2026" }), // done_at melewati status
		/*6*/ base("DONE", func(r *TaskImportRow) { r.InProgressAt, r.DoneAt = "05/10/2026", "01/10/2026" }), // tidak berurutan
		/*7*/ base("DONE", func(r *TaskImportRow) { r.DoneAt = "01/01/2027" }), // masa depan
		/*8*/ base("CANCELED", func(r *TaskImportRow) { r.InProgressAt = "01/10/2026" }), // CANCELED dengan riwayat wajib canceled_at
		/*9*/ base("DONE", func(r *TaskImportRow) { r.DoneAt = "31-12-2026" }), // format
		/*10*/ base("DONE", func(r *TaskImportRow) {
			r.InProgressAt, r.DoneAt, r.PicUnderReview = "01/10/2026", "02/10/2026", "pm@corp.com"
		}), // PIC status tak dilalui
		/*11*/ base("IN PROGRESS", func(r *TaskImportRow) { r.InProgressAt, r.PicInProgress = "01/10/2026", "orang@luar.com" }), // PIC bukan member
		/*12*/ base("IN PROGRESS", func(r *TaskImportRow) { r.PicInProgress = "editor@corp.com" }), // tanpa riwayat: PIC status saat ini boleh
		/*13*/ base("IN PROGRESS", func(r *TaskImportRow) { r.PicBacklog = "editor@corp.com" }), // tanpa riwayat: PIC status lain tidak
		// CANCELED (status sistem akhir): bisa dicapai dari status manapun.
		/*14*/ base("CANCELED", func(r *TaskImportRow) {
			r.CreatedAt, r.InProgressAt, r.CanceledAt, r.PicInProgress = "28/09/2026", "01/10/2026", "05/10/2026", "editor@corp.com"
		}), // dibatalkan di tengah pengerjaan
		/*15*/ base("CANCELED", func(r *TaskImportRow) { r.CreatedAt, r.CanceledAt = "28/09/2026", "05/10/2026" }), // dibatalkan langsung dari backlog
		/*16*/ base("CANCELED", func(r *TaskImportRow) { r.DoneAt, r.CanceledAt = "01/10/2026", "05/10/2026" }), // DONE lalu dibatalkan
		/*17*/ base("CANCELED", func(r *TaskImportRow) { r.InProgressAt, r.CanceledAt = "05/10/2026", "01/10/2026" }), // canceled_at sebelum in_progress_at
		/*18*/ base("CANCELED", func(r *TaskImportRow) { r.PicCanceled = "pm@corp.com" }), // tanpa riwayat: PIC status saat ini boleh
		/*19*/ base("DONE", func(r *TaskImportRow) { r.DoneAt, r.CanceledAt = "01/10/2026", "05/10/2026" }), // canceled_at untuk task berstatus DONE
		/*20*/ base("CANCELED", func(r *TaskImportRow) { r.CanceledAt = "01/01/2027" }), // masa depan
	}
	for i := range rows {
		rows[i].RowNum = i + 2
	}
	validateTaskRows(rows, taskEnv())

	want := map[int]string{2: "valid", 3: "valid", 4: "skipped", 5: "skipped", 6: "skipped", 7: "skipped", 8: "skipped", 9: "skipped", 10: "skipped", 11: "skipped", 12: "valid", 13: "skipped",
		14: "valid", 15: "valid", 16: "valid", 17: "skipped", 18: "valid", 19: "skipped", 20: "skipped"}
	for i := range rows {
		r := &rows[i]
		if r.Status != want[r.RowNum] {
			t.Errorf("baris %d: status = %q (%s), want %q", r.RowNum, r.Status, r.Reason, want[r.RowNum])
		}
		if r.Status == "skipped" && r.Reason == "" {
			t.Errorf("baris %d: dilewati tanpa alasan", r.RowNum)
		}
	}
	if rows[0].CreatedAt != "2026-09-28" || rows[0].DoneAt != "2026-10-08" || rows[0].PicInProgress != "editor@corp.com" {
		t.Errorf("normalisasi baris 2 = %+v", rows[0])
	}
}

func TestBuildTaskImportHistory(t *testing.T) {
	env := taskEnv()
	row := &TaskImportRow{
		TaskStatus: "DONE", CreatedAt: "2026-09-28", InProgressAt: "2026-10-01", UnderReviewAt: "2026-10-01", DoneAt: "2026-10-08",
		PicBacklog: "pm@corp.com", PicInProgress: "editor@corp.com;pm@corp.com", PicDone: "pm@corp.com",
	}
	created, completed, sessions, phases := buildTaskImportHistory(row, env, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))

	if created == nil || created.Format("2006-01-02") != "2026-09-28" || completed == nil || completed.Format("2006-01-02") != "2026-10-08" {
		t.Fatalf("created=%v completed=%v", created, completed)
	}
	if len(sessions) != 4 {
		t.Fatalf("sessions = %d, want 4", len(sessions))
	}
	// rantai tertutup: exited sesi k = entered sesi k+1; sesi terakhir (DONE) terbuka
	for k := 0; k < 3; k++ {
		if sessions[k].ExitedAt == nil || !sessions[k].ExitedAt.Equal(sessions[k+1].EnteredAt) {
			t.Errorf("sesi %d tidak tersambung ke sesi berikutnya: %+v", k, sessions[k])
		}
	}
	if sessions[3].ExitedAt != nil || sessions[3].WorkStarted {
		t.Errorf("sesi DONE harus terbuka dan tanpa work_started: %+v", sessions[3])
	}
	// hari yang sama (in_progress dan under_review 01/10): urutan tetap naik
	if !sessions[2].EnteredAt.After(sessions[1].EnteredAt) {
		t.Errorf("urutan hari yang sama tidak terjaga: %v vs %v", sessions[1].EnteredAt, sessions[2].EnteredAt)
	}
	if sessions[1].StatusID != "st-prog" || !sessions[1].WorkStarted || !sessions[0].WorkStarted {
		t.Errorf("sesi in progress / backlog (tertutup) = %+v / %+v", sessions[1], sessions[0])
	}
	// PIC: 1 (backlog) + 2 (in progress) + 1 (done) = 4 fase; DONE tidak butuh PIC -> tidak aktif
	if len(phases) != 4 {
		t.Fatalf("phases = %d, want 4", len(phases))
	}
	for i := range phases {
		if phases[i].Active {
			t.Errorf("fase %d seharusnya tidak aktif (task DONE): %+v", i, phases[i])
		}
	}
}

// CANCELED di tengah alur: rantai sesi sampai CANCELED (terbuka), completed_at
// TIDAK diisi (hanya DONE), PIC CANCELED tercatat tapi tidak aktif.
func TestBuildTaskImportHistory_Canceled(t *testing.T) {
	env := taskEnv()
	row := &TaskImportRow{
		TaskStatus: "CANCELED", CreatedAt: "2026-09-28", InProgressAt: "2026-10-01", CanceledAt: "2026-10-05",
		PicInProgress: "editor@corp.com", PicCanceled: "pm@corp.com",
	}
	created, completed, sessions, phases := buildTaskImportHistory(row, env, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))

	if created == nil || created.Format("2006-01-02") != "2026-09-28" || completed != nil {
		t.Fatalf("created=%v completed=%v, want completed nil untuk CANCELED", created, completed)
	}
	if len(sessions) != 3 || sessions[2].StatusID != "st-canceled" || sessions[2].ExitedAt != nil || sessions[2].WorkStarted {
		t.Fatalf("sessions = %+v, want 3 sesi dengan CANCELED terbuka", sessions)
	}
	for k := 0; k < 2; k++ {
		if sessions[k].ExitedAt == nil || !sessions[k].ExitedAt.Equal(sessions[k+1].EnteredAt) {
			t.Errorf("sesi %d tidak tersambung: %+v", k, sessions[k])
		}
	}
	if len(phases) != 2 {
		t.Fatalf("phases = %d, want 2", len(phases))
	}
	for i := range phases {
		if phases[i].Active {
			t.Errorf("fase %d seharusnya tidak aktif (CANCELED/riwayat): %+v", i, phases[i])
		}
	}
}

func TestBuildTaskImportHistory_CurrentStatusPicActive(t *testing.T) {
	env := taskEnv()
	row := &TaskImportRow{TaskStatus: "IN PROGRESS", InProgressAt: "2026-10-01", PicInProgress: "editor@corp.com"}
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	_, completed, sessions, phases := buildTaskImportHistory(row, env, now)
	if completed != nil || len(sessions) != 1 || sessions[0].ExitedAt != nil || !sessions[0].WorkStarted {
		t.Fatalf("completed=%v sessions=%+v", completed, sessions)
	}
	if len(phases) != 1 || !phases[0].Active || phases[0].DeactivatedAt != nil || phases[0].UserID != "u-ed" {
		t.Errorf("PIC status saat ini harus aktif: %+v", phases)
	}

	// tanpa riwayat: tidak ada sesi (repo membuat satu sesi sejak sekarang), PIC status saat ini aktif
	row2 := &TaskImportRow{TaskStatus: "IN PROGRESS", PicInProgress: "pm@corp.com"}
	created, completed2, sessions2, phases2 := buildTaskImportHistory(row2, env, now)
	if created != nil || completed2 != nil || len(sessions2) != 0 || len(phases2) != 1 || !phases2[0].Active {
		t.Errorf("tanpa riwayat: created=%v completed=%v sessions=%v phases=%+v", created, completed2, sessions2, phases2)
	}
}

func TestProjectImport_Task_Description(t *testing.T) {
	// parser: kolom description (sel dengan baris baru dalam tanda kutip) dan trim
	rows, err := parseTaskCSV([]byte("title,description\nTask A,\"  baris satu\r\nbaris dua  \"\nTask B\n"))
	if err != nil || len(rows) != 2 {
		t.Fatalf("parse: %v rows=%d", err, len(rows))
	}
	validateTaskRows(rows, taskEnv())
	if rows[0].Status != "valid" || rows[0].Description != "baris satu\nbaris dua" || rows[1].Description != "" {
		t.Errorf("normalisasi deskripsi = %+v / %+v", rows[0], rows[1])
	}

	// terlalu panjang dilewati
	long := []TaskImportRow{{RowNum: 2, Title: "Task panjang", Description: strings.Repeat("x", maxTaskDescriptionLen+1)}}
	validateTaskRows(long, taskEnv())
	if long[0].Status != "skipped" || long[0].Reason == "" {
		t.Errorf("deskripsi terlalu panjang harus dilewati: %+v", long[0])
	}

	// eksekusi: deskripsi dikirim sebagai JSON string, kosong -> nil
	repo, sprints, tasks := &fakePIRepo{}, &fakePISprints{}, &fakePITasks{}
	svc := NewProjectImportService(repo, sprints, &fakePIProjects{isPM: true}, &fakeSprintRBAC{role: "project_manager"}, defaultPIStatuses(), defaultPIMembers(), tasks)
	res, err := svc.Validate(context.Background(), nil, "p1", "task", "t.csv", []byte("title,description\nTask Satu,\"Ada \"\"kutip\"\" di sini\"\nTask Dua,\n"), "pm-1", "")
	if err != nil || res.ValidN != 2 {
		t.Fatalf("Validate: %v %+v", err, res)
	}
	if _, err := svc.Execute(context.Background(), nil, "p1", res.ImportID, "pm-1", ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if string(tasks.created[0].Description) != `"Ada \"kutip\" di sini"` || tasks.created[1].Description != nil {
		t.Errorf("description input = %q / %q", tasks.created[0].Description, tasks.created[1].Description)
	}
}
