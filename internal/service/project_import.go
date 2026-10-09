// Package service -- ProjectImportService (Import CSV PM, "PM Import CSV.dc.html",
// IG-120). Tahap (a): kind "sprint" (kode sprint = kunci penghubung import
// task tahap berikutnya). Level PROJECT, dikelola Project Manager / Admin
// Workspace (GA/PA bypass) -- terpisah dari CSVImportService (level group,
// kind member). Validasi (pratinjau) dan eksekusi sama-sama SINKRON di
// request: sprint jumlahnya kecil, tidak perlu job background.
package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// SprintImportRow -- satu baris CSV import sprint. Status: "valid" |
// "skipped" (lihat Reason). Tanggal disimpan ISO (YYYY-MM-DD) setelah dinormalisasi.
type SprintImportRow struct {
	RowNum       int    `json:"row"`
	Code         string `json:"code"`
	Name         string `json:"name"`
	StartDate    string `json:"start_date,omitempty"`
	EndDate      string `json:"end_date,omitempty"`
	Goal         string `json:"goal,omitempty"`
	SprintStatus string `json:"sprint_status"`
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
}

var sprintCodePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,19}$`)

// SprintImportTemplateCSV -- template kolom desain: kode, nama, tanggal
// (DD/MM/YYYY), tujuan, status.
func SprintImportTemplateCSV() []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"code", "name", "start_date", "end_date", "goal", "status"})
	_ = w.Write([]string{"SPR-01", "Sprint 1", "01/10/2026", "14/10/2026", "Rilis modul login", "done"})
	_ = w.Write([]string{"SPR-02", "Sprint 2", "15/10/2026", "28/10/2026", "", "backlog"})
	w.Flush()
	return buf.Bytes()
}

// parseImportDate -- terima DD/MM/YYYY (desain) dan YYYY-MM-DD; "" = tidak diisi.
func parseImportDate(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	for _, layout := range []string{"02/01/2006", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("2006-01-02"), true
		}
	}
	return "", false
}

// newImportCSVReader -- pemisah kolom dideteksi dari baris header: Excel dengan
// locale Indonesia menyimpan CSV dengan ";" (koma dipakai sebagai desimal).
// Header tidak berkutip, jadi cukup bandingkan jumlah ";" dan "," di baris pertama.
func newImportCSVReader(data []byte) *csv.Reader {
	r := csv.NewReader(bytes.NewReader(data))
	first, _, _ := bytes.Cut(data, []byte("\n"))
	if bytes.Count(first, []byte(";")) > bytes.Count(first, []byte(",")) {
		r.Comma = ';'
	}
	return r
}

func parseSprintCSV(data []byte) ([]SprintImportRow, error) {
	if len(data) > maxCSVBytes {
		return nil, fmt.Errorf("service.parseSprintCSV: %w", domain.ErrCSVTooLarge)
	}
	r := newImportCSVReader(data)
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("service.parseSprintCSV: baca header: %w", domain.ErrInvalidInput)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))] = i
	}
	for _, required := range []string{"code", "name"} {
		if _, ok := col[required]; !ok {
			return nil, fmt.Errorf("service.parseSprintCSV: %w: kolom %q wajib ada", domain.ErrInvalidInput, required)
		}
	}
	get := func(rec []string, key string) string {
		i, ok := col[key]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	rows := make([]SprintImportRow, 0)
	rowNum := 1
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("service.parseSprintCSV: baris %d: %w", rowNum+1, domain.ErrInvalidInput)
		}
		rowNum++
		if rowNum > maxCSVRows+1 {
			return nil, fmt.Errorf("service.parseSprintCSV: %w", domain.ErrCSVTooManyRows)
		}
		rows = append(rows, SprintImportRow{
			RowNum: rowNum, Code: get(rec, "code"), Name: get(rec, "name"), Goal: get(rec, "goal"),
			// Tanggal/status mentah disimpan dulu; dinormalisasi validateSprintRows.
			StartDate: get(rec, "start_date"), EndDate: get(rec, "end_date"), SprintStatus: get(rec, "status"),
		})
	}
	return rows, nil
}

// validateSprintRows -- tandai tiap baris valid/skipped terhadap sprint yang
// sudah ada di project (existing) dan baris lain di berkas yang sama.
// Murni (tanpa I/O) supaya mudah diuji; dipakai ulang saat Execute.
func validateSprintRows(rows []SprintImportRow, existing []repository.Sprint) {
	codes := map[string]bool{}
	names := map[string]bool{}
	hasActive := false
	for i := range existing {
		s := &existing[i]
		codes[strings.ToUpper(s.Code)] = true
		names[strings.ToLower(s.Name)] = true
		if s.Status == "active" {
			hasActive = true
		}
	}
	activeInFile := false

	skip := func(row *SprintImportRow, reason string) {
		row.Status, row.Reason = "skipped", reason
	}
	for i := range rows {
		row := &rows[i]
		row.Status, row.Reason = "", ""
		row.Code = strings.ToUpper(strings.TrimSpace(row.Code))
		row.Name = strings.TrimSpace(row.Name)
		row.SprintStatus = strings.ToLower(strings.TrimSpace(row.SprintStatus))
		if row.SprintStatus == "" {
			row.SprintStatus = "backlog"
		}
		start, okStart := parseImportDate(row.StartDate)
		end, okEnd := parseImportDate(row.EndDate)

		switch {
		case row.Code == "":
			skip(row, "Kolom code wajib diisi.")
		case !sprintCodePattern.MatchString(row.Code):
			skip(row, "Format code tidak valid (huruf/angka/titik/strip/garis bawah, maks 20 karakter).")
		case codes[row.Code]:
			skip(row, fmt.Sprintf("Code %q sudah dipakai (di project ini atau baris lain di berkas).", row.Code))
		case row.Name == "":
			skip(row, "Kolom name wajib diisi.")
		case len(row.Name) > 255:
			skip(row, "Nama sprint maksimal 255 karakter.")
		case names[strings.ToLower(row.Name)]:
			skip(row, fmt.Sprintf("Nama sprint %q sudah dipakai (di project ini atau baris lain di berkas).", row.Name))
		case !okStart:
			skip(row, "Format start_date harus DD/MM/YYYY.")
		case !okEnd:
			skip(row, "Format end_date harus DD/MM/YYYY.")
		case start != "" && end != "" && end < start:
			skip(row, "end_date tidak boleh lebih awal dari start_date.")
		case row.SprintStatus != "backlog" && row.SprintStatus != "active" && row.SprintStatus != "done":
			skip(row, "Status harus backlog, active, atau done.")
		case row.SprintStatus == "active" && hasActive:
			skip(row, "Project ini sudah punya sprint aktif -- hanya satu sprint aktif per project.")
		case row.SprintStatus == "active" && activeInFile:
			skip(row, "Hanya satu baris berstatus active per berkas.")
		default:
			row.Status = "valid"
			row.StartDate, row.EndDate = start, end
			codes[row.Code] = true
			names[strings.ToLower(row.Name)] = true
			if row.SprintStatus == "active" {
				activeInFile = true
			}
			continue
		}
	}
}

type projectImportRepo interface {
	Create(ctx context.Context, exec db.Executor, projectID, kind, importedBy, actorRole, filename string, totalRows int, rowResults []byte) (string, error)
	Get(ctx context.Context, exec db.Executor, importID string) (*repository.ProjectImport, error)
	ListByProject(ctx context.Context, exec db.Executor, projectID string) ([]repository.ProjectImport, error)
	Complete(ctx context.Context, exec db.Executor, imp *repository.ProjectImport, successCount, failedCount int, rowResults []byte, workspaceID, actorID, actorRole string) error
	AuditReportDownload(ctx context.Context, exec db.Executor, imp *repository.ProjectImport, workspaceID, actorID, actorRole string) error
}

type projectImportSprints interface {
	List(ctx context.Context, exec db.Executor, projectID string) ([]repository.Sprint, error)
	CreateImported(ctx context.Context, exec db.Executor, projectID, code, name, status string, startDate, endDate *time.Time, goal *string, workspaceID, actorID, actorRole string) (*repository.Sprint, error)
}

type projectImportProjects interface {
	GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error)
	IsPM(ctx context.Context, exec db.Executor, projectID, userID string) (bool, error)
}

type ProjectImportService struct {
	repo     projectImportRepo
	sprints  projectImportSprints
	projects projectImportProjects
	rbac     sprintWorkspaceRoleChecker
	statuses projectImportStatuses
	members  projectImportMembers
	tasks    projectImportTasks
}

func NewProjectImportService(repo projectImportRepo, sprints projectImportSprints, projects projectImportProjects, rbac sprintWorkspaceRoleChecker,
	statuses projectImportStatuses, members projectImportMembers, tasks projectImportTasks) *ProjectImportService {
	return &ProjectImportService{repo: repo, sprints: sprints, projects: projects, rbac: rbac, statuses: statuses, members: members, tasks: tasks}
}

// authorize -- PM project ini, Admin Workspace, atau GA/PA (bypass). Editor/
// Approver/Viewer ditolak. Mengembalikan role EFEKTIF untuk audit (rute
// project tanpa RequireRole, pola SprintService.authorize).
func (s *ProjectImportService) authorize(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) (auditRole, workspaceID string, err error) {
	workspaceID, err = s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return "", "", fmt.Errorf("service.authorize: %w", err)
	}
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return actorRole, workspaceID, nil
	}
	isPM, err := s.projects.IsPM(ctx, exec, projectID, actorID)
	if err != nil {
		return "", "", fmt.Errorf("service.authorize: %w", err)
	}
	if isPM {
		return "project_manager", workspaceID, nil
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return "", "", fmt.Errorf("service.authorize: %w", err)
	}
	if role == "admin_workspace" {
		return role, workspaceID, nil
	}
	return "", "", fmt.Errorf("service.authorize: %w", domain.ErrForbidden)
}

func validImportKind(kind string) bool { return kind == "sprint" || kind == "task" }

// ProjectImportTemplate -- template CSV per kind.
func ProjectImportTemplate(kind string) ([]byte, bool) {
	switch kind {
	case "sprint":
		return SprintImportTemplateCSV(), true
	case "task":
		return TaskImportTemplateCSV(), true
	}
	return nil, false
}

// ProjectImportValidateResult -- ringkasan pratinjau dry-run. Preview =
// []SprintImportRow atau []TaskImportRow sesuai kind -- SELURUH baris berkas
// (maks maxCSVRows); paginasi dilakukan FE (pola "Grid 1").
type ProjectImportValidateResult struct {
	ImportID string
	Kind     string
	Total    int
	ValidN   int
	SkippedN int
	Preview  any
}

// countValid -- jumlah baris berstatus "valid".
func countValid[T any](rows []T, status func(*T) string) int {
	n := 0
	for i := range rows {
		if status(&rows[i]) == "valid" {
			n++
		}
	}
	return n
}

// Validate -- parse + validasi (dry-run). TIDAK menulis apa pun selain
// catatan import 'pending' berisi hasil pratinjau.
func (s *ProjectImportService) Validate(ctx context.Context, exec db.Executor, projectID, kind, filename string, data []byte, actorID, actorRole string) (*ProjectImportValidateResult, error) {
	if projectID == "" || len(data) == 0 || !validImportKind(kind) {
		return nil, fmt.Errorf("service.Validate: %w", domain.ErrInvalidInput)
	}
	auditRole, _, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return nil, err
	}

	var (
		rowsJSON []byte
		total    int
		validN   int
		preview  any
	)
	switch kind {
	case "sprint":
		rows, err := parseSprintCSV(data)
		if err != nil {
			return nil, fmt.Errorf("service.Validate: %w", err)
		}
		existing, err := s.sprints.List(ctx, exec, projectID)
		if err != nil {
			return nil, fmt.Errorf("service.Validate: %w", err)
		}
		validateSprintRows(rows, existing)
		total, validN, preview = len(rows), countValid(rows, func(r *SprintImportRow) string { return r.Status }), rows
		rowsJSON, err = json.Marshal(rows)
		if err != nil {
			return nil, fmt.Errorf("service.Validate: encode hasil: %w", err)
		}
	case "task":
		rows, err := parseTaskCSV(data)
		if err != nil {
			return nil, fmt.Errorf("service.Validate: %w", err)
		}
		env, err := s.loadTaskImportEnv(ctx, exec, projectID)
		if err != nil {
			return nil, err
		}
		validateTaskRows(rows, env)
		total, validN, preview = len(rows), countValid(rows, func(r *TaskImportRow) string { return r.Status }), rows
		rowsJSON, err = json.Marshal(rows)
		if err != nil {
			return nil, fmt.Errorf("service.Validate: encode hasil: %w", err)
		}
	}

	importID, err := s.repo.Create(ctx, exec, projectID, kind, actorID, auditRole, filename, total, rowsJSON)
	if err != nil {
		return nil, fmt.Errorf("service.Validate: %w", err)
	}
	return &ProjectImportValidateResult{ImportID: importID, Kind: kind, Total: total, ValidN: validN, SkippedN: total - validN, Preview: preview}, nil
}

func (s *ProjectImportService) load(ctx context.Context, exec db.Executor, projectID, importID string) (*repository.ProjectImport, error) {
	imp, err := s.repo.Get(ctx, exec, importID)
	if err != nil {
		return nil, err
	}
	if imp.ProjectID != projectID {
		return nil, fmt.Errorf("service.load: %w", domain.ErrCSVImportNotFound)
	}
	return imp, nil
}

// Execute -- tulis data yang valid. Pratinjau divalidasi ULANG terhadap
// kondisi project saat ini (bisa berubah sejak pratinjau); baris yang
// sebelumnya dilewati tetap dilewati, baris valid yang kini bentrok ikut
// dilewati dengan alasan "Berubah sejak pratinjau".
func (s *ProjectImportService) Execute(ctx context.Context, exec db.Executor, projectID, importID, actorID, actorRole string) (*repository.ProjectImport, error) {
	auditRole, workspaceID, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return nil, err
	}
	imp, err := s.load(ctx, exec, projectID, importID)
	if err != nil {
		return nil, err
	}
	if imp.Status != "pending" {
		return nil, fmt.Errorf("service.Execute: %w", domain.ErrCSVImportAlreadyStarted)
	}

	var rowsJSON []byte
	var success, total int
	switch imp.Kind {
	case "sprint":
		rowsJSON, success, total, err = s.executeSprintImport(ctx, exec, imp, projectID, workspaceID, actorID, auditRole)
	case "task":
		rowsJSON, success, total, err = s.executeTaskImport(ctx, exec, imp, projectID, workspaceID, actorID, auditRole)
	default:
		err = fmt.Errorf("service.Execute: %w", domain.ErrInvalidInput)
	}
	if err != nil {
		return nil, err
	}
	if err := s.repo.Complete(ctx, exec, imp, success, total-success, rowsJSON, workspaceID, actorID, auditRole); err != nil {
		return nil, fmt.Errorf("service.Execute: %w", err)
	}
	return s.load(ctx, exec, projectID, importID)
}

func (s *ProjectImportService) executeSprintImport(ctx context.Context, exec db.Executor, imp *repository.ProjectImport, projectID, workspaceID, actorID, auditRole string) (rowsJSON []byte, success, total int, err error) {
	var rows []SprintImportRow
	if err := json.Unmarshal(imp.RowResults, &rows); err != nil {
		return nil, 0, 0, fmt.Errorf("service.Execute: decode hasil: %w", err)
	}
	existing, err := s.sprints.List(ctx, exec, projectID)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("service.Execute: %w", err)
	}
	// Validasi ulang HANYA baris yang lolos pratinjau (baris yang sudah
	// dilewati tidak boleh "menyita" code/nama dan menggagalkan baris lain).
	var idx []int
	var recheck []SprintImportRow
	for i := range rows {
		if rows[i].Status == "valid" {
			idx = append(idx, i)
			recheck = append(recheck, rows[i])
		}
	}
	validateSprintRows(recheck, existing)
	for k, i := range idx {
		rows[i] = recheck[k]
		if rows[i].Status == "skipped" {
			rows[i].Reason = "Berubah sejak pratinjau: " + rows[i].Reason
		}
	}

	for i := range rows {
		row := &rows[i]
		if row.Status != "valid" {
			continue
		}
		var goal *string
		if row.Goal != "" {
			g := row.Goal
			goal = &g
		}
		if _, err := s.sprints.CreateImported(ctx, exec, projectID, row.Code, row.Name, row.SprintStatus, importDatePtr(row.StartDate), importDatePtr(row.EndDate), goal, workspaceID, actorID, auditRole); err != nil {
			return nil, 0, 0, fmt.Errorf("service.Execute: baris %d: %w", row.RowNum, err)
		}
		success++
	}
	rowsJSON, err = json.Marshal(rows)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("service.Execute: encode hasil: %w", err)
	}
	return rowsJSON, success, len(rows), nil
}

func (s *ProjectImportService) executeTaskImport(ctx context.Context, exec db.Executor, imp *repository.ProjectImport, projectID, workspaceID, actorID, auditRole string) (rowsJSON []byte, success, total int, err error) {
	var rows []TaskImportRow
	if err := json.Unmarshal(imp.RowResults, &rows); err != nil {
		return nil, 0, 0, fmt.Errorf("service.Execute: decode hasil: %w", err)
	}
	env, err := s.loadTaskImportEnv(ctx, exec, projectID)
	if err != nil {
		return nil, 0, 0, err
	}
	var idx []int
	var recheck []TaskImportRow
	for i := range rows {
		if rows[i].Status == "valid" {
			idx = append(idx, i)
			recheck = append(recheck, rows[i])
		}
	}
	validateTaskRows(recheck, env)
	for k, i := range idx {
		rows[i] = recheck[k]
		if rows[i].Status == "skipped" {
			rows[i].Reason = "Berubah sejak pratinjau: " + rows[i].Reason
		}
	}
	success, err = s.executeTaskRows(ctx, exec, projectID, workspaceID, actorID, auditRole, rows, env)
	if err != nil {
		return nil, 0, 0, err
	}
	rowsJSON, err = json.Marshal(rows)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("service.Execute: encode hasil: %w", err)
	}
	return rowsJSON, success, len(rows), nil
}

func (s *ProjectImportService) Get(ctx context.Context, exec db.Executor, projectID, importID, actorID, actorRole string) (*repository.ProjectImport, error) {
	if _, _, err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return nil, err
	}
	return s.load(ctx, exec, projectID, importID)
}

func (s *ProjectImportService) ListHistory(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) ([]repository.ProjectImport, error) {
	if _, _, err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return nil, err
	}
	list, err := s.repo.ListByProject(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.ListHistory: %w", err)
	}
	return list, nil
}

// Report -- CSV hasil (nomor baris, isi, alasan). onlySkipped=true hanya
// baris yang dilewati ("UNDUH LOG BARIS DILEWATI"). Unduhan tercatat di audit.
func (s *ProjectImportService) Report(ctx context.Context, exec db.Executor, projectID, importID string, onlySkipped bool, actorID, actorRole string) ([]byte, error) {
	auditRole, workspaceID, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return nil, err
	}
	imp, err := s.load(ctx, exec, projectID, importID)
	if err != nil {
		return nil, err
	}

	var out []byte
	switch imp.Kind {
	case "task":
		var rows []TaskImportRow
		if err := json.Unmarshal(imp.RowResults, &rows); err != nil {
			return nil, fmt.Errorf("service.Report: decode hasil: %w", err)
		}
		out = taskImportReportCSV(rows, onlySkipped)
	default:
		var rows []SprintImportRow
		if err := json.Unmarshal(imp.RowResults, &rows); err != nil {
			return nil, fmt.Errorf("service.Report: decode hasil: %w", err)
		}
		out = sprintImportReportCSV(rows, onlySkipped)
	}
	if err := s.repo.AuditReportDownload(ctx, exec, imp, workspaceID, actorID, auditRole); err != nil {
		return nil, fmt.Errorf("service.Report: audit: %w", err)
	}
	return out, nil
}

func sprintImportReportCSV(rows []SprintImportRow, onlySkipped bool) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"baris", "code", "name", "start_date", "end_date", "goal", "status", "hasil", "alasan"})
	for i := range rows {
		r := &rows[i]
		if onlySkipped && r.Status != "skipped" {
			continue
		}
		hasil := "BERHASIL"
		if r.Status == "skipped" {
			hasil = "DILEWATI"
		}
		_ = w.Write([]string{fmt.Sprintf("%d", r.RowNum), r.Code, r.Name, r.StartDate, r.EndDate, r.Goal, r.SprintStatus, hasil, r.Reason})
	}
	w.Flush()
	return buf.Bytes()
}
