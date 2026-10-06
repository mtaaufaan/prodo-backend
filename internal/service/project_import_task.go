package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// Import task (IG-120 tahap b, kind=task) -- "PM Import CSV.dc.html". Sprint
// ditunjuk lewat KODE sprint (tahap a); status awal bebas; assignee opsional;
// rule task_created tidak dijalankan. Riwayat tanggal status dan PIC per
// status menyusul di tahap (c).

// TaskImportRow -- satu baris CSV import task. Status ("valid"/"skipped",
// lihat Reason) memakai nama yang sama dengan SprintImportRow; status TASK-nya
// sendiri ada di TaskStatus.
type TaskImportRow struct {
	RowNum      int    `json:"row"`
	Title       string `json:"title"`
	TaskStatus  string `json:"task_status"`
	Priority    string `json:"priority"`
	Assignee    string `json:"assignee,omitempty"` // email, dipisah ";" kalau lebih dari satu
	StartDate   string `json:"start_date,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
	Sprint      string `json:"sprint,omitempty"` // kode sprint
	Estimate    string `json:"estimate,omitempty"`
	StoryPoints string `json:"story_points,omitempty"`
	TaskCode    string `json:"task_code,omitempty"` // terisi setelah eksekusi
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
}

const maxTaskTitleLen = 160

// TaskImportTemplateCSV -- template kolom desain + start_date (perkiraan mulai).
func TaskImportTemplateCSV() []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"title", "status", "priority", "assignee", "start_date", "due_date", "sprint", "estimate", "story_points"})
	_ = w.Write([]string{"Perbaiki validasi form pendaftaran", "BACKLOG", "high", "nama@perusahaan.com", "01/10/2026", "08/10/2026", "SPR-01", "6", "3"})
	_ = w.Write([]string{"Dokumentasi API publik", "", "low", "", "", "", "", "", "?"})
	w.Flush()
	return buf.Bytes()
}

func parseTaskCSV(data []byte) ([]TaskImportRow, error) {
	if len(data) > maxCSVBytes {
		return nil, fmt.Errorf("service.parseTaskCSV: %w", domain.ErrCSVTooLarge)
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("service.parseTaskCSV: baca header: %w", domain.ErrInvalidInput)
	}
	col := map[string]int{}
	for i, h := range header {
		key := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		col[strings.TrimSuffix(key, "*")] = i // "title*" (penanda wajib di desain) = "title"
	}
	if _, ok := col["title"]; !ok {
		return nil, fmt.Errorf("service.parseTaskCSV: %w: kolom \"title\" wajib ada", domain.ErrInvalidInput)
	}
	get := func(rec []string, key string) string {
		i, ok := col[key]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	rows := make([]TaskImportRow, 0)
	rowNum := 1
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("service.parseTaskCSV: baris %d: %w", rowNum+1, domain.ErrInvalidInput)
		}
		rowNum++
		if rowNum > maxCSVRows+1 {
			return nil, fmt.Errorf("service.parseTaskCSV: %w", domain.ErrCSVTooManyRows)
		}
		rows = append(rows, TaskImportRow{
			RowNum: rowNum, Title: get(rec, "title"), TaskStatus: get(rec, "status"), Priority: get(rec, "priority"),
			Assignee: get(rec, "assignee"), StartDate: get(rec, "start_date"), DueDate: get(rec, "due_date"),
			Sprint: get(rec, "sprint"), Estimate: get(rec, "estimate"), StoryPoints: get(rec, "story_points"),
		})
	}
	return rows, nil
}

// taskImportEnv -- kondisi project saat validasi: status (nama huruf besar ->
// status, tanpa UNDEFINED), sprint (kode huruf besar -> sprint), dan kandidat
// assignee (email huruf kecil -> user id; Viewer ditolak, sama dengan picker).
type taskImportEnv struct {
	statuses  map[string]repository.CustomStatus
	sprints   map[string]repository.Sprint
	assignees map[string]string
}

// parseEstimate -- angka desimal >= 0, koma atau titik; "" = tidak diisi.
func parseEstimate(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(raw, ",", "."), 64)
	if err != nil || v < 0 {
		return "", false
	}
	return strconv.FormatFloat(v, 'f', -1, 64), true
}

// parseImportStoryPoints -- "" atau "?" = belum diestimasi; selain itu harus
// salah satu angka Fibonacci kartu (1/2/3/5/8/13).
func parseImportStoryPoints(raw string) (norm string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "?" {
		return "", true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || !validFibonacciSP[n] {
		return "", false
	}
	return strconv.Itoa(n), true
}

// validateTaskRows -- murni (tanpa I/O): tandai tiap baris valid/skipped dan
// normalisasi nilainya (huruf besar/kecil, tanggal ISO, estimasi, SP).
func validateTaskRows(rows []TaskImportRow, env taskImportEnv) {
	skip := func(row *TaskImportRow, reason string) { row.Status, row.Reason = "skipped", reason }
	for i := range rows {
		row := &rows[i]
		row.Status, row.Reason = "", ""
		row.Title = strings.TrimSpace(row.Title)
		row.TaskStatus = strings.ToUpper(strings.TrimSpace(row.TaskStatus))
		if row.TaskStatus == "" {
			row.TaskStatus = "BACKLOG"
		}
		row.Sprint = strings.ToUpper(strings.TrimSpace(row.Sprint))

		priority, perr := validatePriority(row.Priority)
		start, okStart := parseImportDate(row.StartDate)
		due, okDue := parseImportDate(row.DueDate)
		estimate, okEstimate := parseEstimate(row.Estimate)
		sp, okSP := parseImportStoryPoints(row.StoryPoints)
		emails, badEmail := splitAssigneeEmails(row.Assignee, env.assignees)
		status, okStatus := env.statuses[row.TaskStatus]
		sprint, okSprint := env.sprints[row.Sprint]

		switch {
		case len(row.Title) < 3:
			skip(row, "Kolom title wajib diisi (minimal 3 karakter).")
		case len(row.Title) > maxTaskTitleLen:
			skip(row, fmt.Sprintf("Judul maksimal %d karakter.", maxTaskTitleLen))
		case !okStatus:
			skip(row, fmt.Sprintf("Status %q tidak ada di project ini.", row.TaskStatus))
		case perr != nil:
			skip(row, "Priority harus low, medium, high, atau critical.")
		case badEmail != "":
			skip(row, fmt.Sprintf("Assignee %q bukan member project ini (atau Viewer).", badEmail))
		case !okStart:
			skip(row, "Format start_date harus DD/MM/YYYY.")
		case !okDue:
			skip(row, "Format due_date harus DD/MM/YYYY.")
		case start != "" && due != "" && due < start:
			skip(row, "due_date tidak boleh lebih awal dari start_date.")
		case row.Sprint != "" && !okSprint:
			skip(row, fmt.Sprintf("Sprint dengan kode %q tidak ditemukan di project ini.", row.Sprint))
		case row.Sprint != "" && sprint.Status == "done" && row.TaskStatus != "DONE" && row.TaskStatus != "CANCELED":
			skip(row, fmt.Sprintf("Sprint %s sudah selesai -- hanya task berstatus DONE atau CANCELED yang boleh masuk.", row.Sprint))
		case !okEstimate:
			skip(row, "Estimate harus angka desimal >= 0 (jam).")
		case !okSP:
			skip(row, "story_points harus 1/2/3/5/8/13 atau ? (belum diestimasi).")
		default:
			row.Status = "valid"
			row.TaskStatus = strings.ToUpper(status.Name)
			row.Priority = priority
			row.Assignee = strings.Join(emails, ";")
			row.StartDate, row.DueDate, row.Estimate, row.StoryPoints = start, due, estimate, sp
		}
	}
}

// splitAssigneeEmails -- "a@x;b@y" -> email ternormalisasi (huruf kecil,
// tanpa duplikat); bad = email pertama yang bukan kandidat assignee.
func splitAssigneeEmails(raw string, assignable map[string]string) (emails []string, bad string) {
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ";") {
		e := strings.ToLower(strings.TrimSpace(part))
		if e == "" || seen[e] {
			continue
		}
		if _, ok := assignable[e]; !ok {
			return nil, e
		}
		seen[e] = true
		emails = append(emails, e)
	}
	return emails, ""
}

type projectImportStatuses interface {
	ListForScope(ctx context.Context, exec db.Executor, scopeType, scopeID string) ([]repository.CustomStatus, error)
}

type projectImportMembers interface {
	ListAssignableMembers(ctx context.Context, exec db.Executor, projectID string) ([]repository.ProjectMember, error)
}

type projectImportTasks interface {
	CreateImported(ctx context.Context, exec db.Executor, in *repository.TaskImportInput) (taskID, taskCode string, err error)
}

// loadTaskImportEnv -- baca status/sprint/kandidat assignee project SAAT INI.
func (s *ProjectImportService) loadTaskImportEnv(ctx context.Context, exec db.Executor, projectID string) (taskImportEnv, error) {
	env := taskImportEnv{statuses: map[string]repository.CustomStatus{}, sprints: map[string]repository.Sprint{}, assignees: map[string]string{}}
	statuses, err := s.statuses.ListForScope(ctx, exec, "project", projectID)
	if err != nil {
		return env, fmt.Errorf("service.loadTaskImportEnv: %w", err)
	}
	for i := range statuses {
		if !statuses[i].IsUndefined {
			env.statuses[strings.ToUpper(statuses[i].Name)] = statuses[i]
		}
	}
	sprints, err := s.sprints.List(ctx, exec, projectID)
	if err != nil {
		return env, fmt.Errorf("service.loadTaskImportEnv: %w", err)
	}
	for i := range sprints {
		env.sprints[strings.ToUpper(sprints[i].Code)] = sprints[i]
	}
	members, err := s.members.ListAssignableMembers(ctx, exec, projectID)
	if err != nil {
		return env, fmt.Errorf("service.loadTaskImportEnv: %w", err)
	}
	for i := range members {
		if members[i].Role != "viewer" && !members[i].IsPending {
			env.assignees[strings.ToLower(members[i].Email)] = members[i].UserID
		}
	}
	return env, nil
}

// executeTaskRows -- tulis task yang valid. Dipanggil Execute setelah
// validasi ulang; mengembalikan jumlah berhasil.
func (s *ProjectImportService) executeTaskRows(ctx context.Context, exec db.Executor, projectID, workspaceID, actorID, auditRole string, rows []TaskImportRow, env taskImportEnv) (int, error) {
	success := 0
	for i := range rows {
		row := &rows[i]
		if row.Status != "valid" {
			continue
		}
		in := repository.TaskImportInput{
			ProjectID: projectID, WorkspaceID: workspaceID, ActorID: actorID, ActorRole: auditRole,
			StatusID: env.statuses[row.TaskStatus].ID, StatusName: row.TaskStatus,
			Title: row.Title, Priority: row.Priority,
		}
		if row.Sprint != "" {
			id := env.sprints[row.Sprint].ID
			in.SprintID = &id
		}
		in.StartDate, in.DueDate = importDatePtr(row.StartDate), importDatePtr(row.DueDate)
		if row.Estimate != "" {
			v, _ := strconv.ParseFloat(row.Estimate, 64)
			in.EstimatedHours = &v
		}
		if row.StoryPoints != "" {
			v, _ := strconv.Atoi(row.StoryPoints)
			in.StoryPoints = &v
		}
		for _, e := range strings.Split(row.Assignee, ";") {
			if e != "" {
				in.AssigneeUserIDs = append(in.AssigneeUserIDs, env.assignees[e])
			}
		}
		_, code, err := s.tasks.CreateImported(ctx, exec, &in)
		if err != nil {
			return success, fmt.Errorf("service.executeTaskRows: baris %d: %w", row.RowNum, err)
		}
		row.TaskCode = code
		success++
	}
	return success, nil
}

func importDatePtr(iso string) *time.Time {
	if iso == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return nil
	}
	return &t
}

func taskImportReportCSV(rows []TaskImportRow, onlySkipped bool) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"baris", "title", "status", "priority", "assignee", "start_date", "due_date", "sprint", "estimate", "story_points", "task_code", "hasil", "alasan"})
	for i := range rows {
		r := &rows[i]
		if onlySkipped && r.Status != "skipped" {
			continue
		}
		hasil := "BERHASIL"
		if r.Status == "skipped" {
			hasil = "DILEWATI"
		}
		_ = w.Write([]string{fmt.Sprintf("%d", r.RowNum), r.Title, r.TaskStatus, r.Priority, r.Assignee, r.StartDate, r.DueDate, r.Sprint, r.Estimate, r.StoryPoints, r.TaskCode, hasil, r.Reason})
	}
	w.Flush()
	return buf.Bytes()
}
