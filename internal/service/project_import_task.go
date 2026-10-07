package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// Import task (IG-120 tahap b dan c, kind=task) -- "PM Import CSV.dc.html". Sprint
// ditunjuk lewat KODE sprint (tahap a); status awal bebas; assignee opsional;
// rule task_created tidak dijalankan. Tahap (c): kolom opsional riwayat tanggal
// status (created_at/in_progress_at/under_review_at/done_at) dan PIC per status
// (pic_backlog/pic_in_progress/pic_under_review/pic_done).

// TaskImportRow -- satu baris CSV import task. Status ("valid"/"skipped",
// lihat Reason) memakai nama yang sama dengan SprintImportRow; status TASK-nya
// sendiri ada di TaskStatus.
type TaskImportRow struct {
	RowNum      int    `json:"row"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	TaskStatus  string `json:"task_status"`
	Priority    string `json:"priority"`
	Assignee    string `json:"assignee,omitempty"` // email, dipisah ";" kalau lebih dari satu
	StartDate   string `json:"start_date,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
	Sprint      string `json:"sprint,omitempty"` // kode sprint
	Estimate    string `json:"estimate,omitempty"`
	StoryPoints string `json:"story_points,omitempty"`
	// Riwayat tanggal status (tahap c, opsional, tanggal ISO setelah validasi).
	CreatedAt     string `json:"created_at,omitempty"`
	InProgressAt  string `json:"in_progress_at,omitempty"`
	UnderReviewAt string `json:"under_review_at,omitempty"`
	DoneAt        string `json:"done_at,omitempty"`
	// PIC per status (tahap c, opsional): email dipisah ";".
	PicBacklog     string `json:"pic_backlog,omitempty"`
	PicInProgress  string `json:"pic_in_progress,omitempty"`
	PicUnderReview string `json:"pic_under_review,omitempty"`
	PicDone        string `json:"pic_done,omitempty"`
	TaskCode       string `json:"task_code,omitempty"` // terisi setelah eksekusi
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
}

const (
	maxTaskTitleLen       = 160
	maxTaskDescriptionLen = 4000 // karakter; row_results menyimpan seluruh baris, jadi dibatasi
)

// TaskImportTemplateCSV -- template kolom desain + start_date (perkiraan mulai).
func TaskImportTemplateCSV() []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"title", "description", "status", "priority", "assignee", "start_date", "due_date", "sprint", "estimate", "story_points",
		"created_at", "in_progress_at", "under_review_at", "done_at", "pic_backlog", "pic_in_progress", "pic_under_review", "pic_done"})
	_ = w.Write([]string{"Perbaiki validasi form pendaftaran", "Validasi email dan password di form daftar; tampilkan pesan error per kolom.", "DONE", "high", "nama@perusahaan.com", "01/10/2026", "08/10/2026", "SPR-01", "6", "3",
		"28/09/2026", "01/10/2026", "06/10/2026", "08/10/2026", "pm@perusahaan.com", "nama@perusahaan.com", "reviewer@perusahaan.com", ""})
	_ = w.Write([]string{"Dokumentasi API publik", "", "", "low", "", "", "", "", "", "?", "", "", "", "", "", "", "", ""})
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
			RowNum: rowNum, Title: get(rec, "title"), Description: get(rec, "description"), TaskStatus: get(rec, "status"), Priority: get(rec, "priority"),
			Assignee: get(rec, "assignee"), StartDate: get(rec, "start_date"), DueDate: get(rec, "due_date"),
			Sprint: get(rec, "sprint"), Estimate: get(rec, "estimate"), StoryPoints: get(rec, "story_points"),
			CreatedAt: get(rec, "created_at"), InProgressAt: get(rec, "in_progress_at"), UnderReviewAt: get(rec, "under_review_at"), DoneAt: get(rec, "done_at"),
			PicBacklog: get(rec, "pic_backlog"), PicInProgress: get(rec, "pic_in_progress"), PicUnderReview: get(rec, "pic_under_review"), PicDone: get(rec, "pic_done"),
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
	today     string // YYYY-MM-DD (UTC) -- tanggal riwayat tidak boleh di masa depan
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
		row.Description = strings.TrimSpace(strings.ReplaceAll(row.Description, "\r\n", "\n"))
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
		dates, pics, histReason := validateTaskHistory(row, env.assignees, env.today)
		emails, badEmail := splitAssigneeEmails(row.Assignee, env.assignees)
		status, okStatus := env.statuses[row.TaskStatus]
		sprint, okSprint := env.sprints[row.Sprint]

		switch {
		case len(row.Title) < 3:
			skip(row, "Kolom title wajib diisi (minimal 3 karakter).")
		case len(row.Title) > maxTaskTitleLen:
			skip(row, fmt.Sprintf("Judul maksimal %d karakter.", maxTaskTitleLen))
		case utf8.RuneCountInString(row.Description) > maxTaskDescriptionLen:
			skip(row, fmt.Sprintf("Deskripsi maksimal %d karakter.", maxTaskDescriptionLen))
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
		case histReason != "":
			skip(row, histReason)
		default:
			row.Status = "valid"
			row.TaskStatus = strings.ToUpper(status.Name)
			row.Priority = priority
			row.Assignee = strings.Join(emails, ";")
			row.StartDate, row.DueDate, row.Estimate, row.StoryPoints = start, due, estimate, sp
			row.CreatedAt, row.InProgressAt, row.UnderReviewAt, row.DoneAt = dates[0], dates[1], dates[2], dates[3]
			row.PicBacklog, row.PicInProgress, row.PicUnderReview, row.PicDone = pics[0], pics[1], pics[2], pics[3]
		}
	}
}

// historyStatusNames -- urutan status baku yang punya kolom tanggal riwayat
// (created_at = masuk BACKLOG) dan kolom PIC.
var historyStatusNames = [4]string{"BACKLOG", "IN PROGRESS", "UNDER REVIEW", "DONE"}

func historyIndex(statusName string) int {
	for i, n := range historyStatusNames {
		if n == statusName {
			return i
		}
	}
	return -1
}

// validateTaskHistory -- aturan riwayat tanggal + PIC per status (tahap c).
//   - Semua kolom tanggal kosong = perilaku tahap (b) (tanpa riwayat).
//   - Ada tanggal: status task harus salah satu status baku; kolom tanggal
//     status SETELAH status saat ini tidak boleh diisi; tanggal berurutan
//     naik; tanggal status saat ini wajib (kecuali BACKLOG); tidak di masa depan.
//   - PIC hanya untuk status yang dilalui (ada tanggalnya; tanpa riwayat:
//     hanya status saat ini); tiap email harus kandidat assignee.
//
// Mengembalikan nilai ternormalisasi (tanggal ISO, email huruf kecil) dan alasan
// kalau baris harus dilewati.
func validateTaskHistory(row *TaskImportRow, assignable map[string]string, today string) (dates, pics [4]string, reason string) {
	raw := [4]string{row.CreatedAt, row.InProgressAt, row.UnderReviewAt, row.DoneAt}
	rawPic := [4]string{row.PicBacklog, row.PicInProgress, row.PicUnderReview, row.PicDone}
	dateCols := [4]string{"created_at", "in_progress_at", "under_review_at", "done_at"}
	picCols := [4]string{"pic_backlog", "pic_in_progress", "pic_under_review", "pic_done"}
	cur := historyIndex(row.TaskStatus)

	has := false
	for i := range raw {
		d, ok := parseImportDate(raw[i])
		if !ok {
			return dates, pics, fmt.Sprintf("Format %s harus DD/MM/YYYY.", dateCols[i])
		}
		if d != "" {
			has = true
			if d > today {
				return dates, pics, fmt.Sprintf("%s tidak boleh di masa depan.", dateCols[i])
			}
		}
		dates[i] = d
	}
	if has {
		if cur < 0 {
			return dates, pics, "Riwayat tanggal hanya untuk task berstatus BACKLOG, IN PROGRESS, UNDER REVIEW, atau DONE."
		}
		prev := ""
		for i := range dates {
			if dates[i] == "" {
				continue
			}
			if i > cur {
				return dates, pics, fmt.Sprintf("%s tidak boleh diisi untuk task berstatus %s.", dateCols[i], row.TaskStatus)
			}
			if prev != "" && dates[i] < prev {
				return dates, pics, "Tanggal riwayat harus berurutan: created_at <= in_progress_at <= under_review_at <= done_at."
			}
			prev = dates[i]
		}
		if cur > 0 && dates[cur] == "" {
			return dates, pics, fmt.Sprintf("%s wajib diisi untuk task berstatus %s kalau riwayat tanggal diisi.", dateCols[cur], row.TaskStatus)
		}
	}

	for i := range rawPic {
		if strings.TrimSpace(rawPic[i]) == "" {
			continue
		}
		passed := (has && dates[i] != "") || (!has && i == cur)
		if !passed {
			return dates, pics, fmt.Sprintf("%s hanya boleh diisi untuk status yang dilalui task (yang punya tanggal riwayat).", picCols[i])
		}
		emails, bad := splitAssigneeEmails(rawPic[i], assignable)
		if bad != "" {
			return dates, pics, fmt.Sprintf("%s: %q bukan member project ini (atau Viewer).", picCols[i], bad)
		}
		pics[i] = strings.Join(emails, ";")
	}
	return dates, pics, ""
}

// buildTaskImportHistory -- ubah baris tervalidasi menjadi sesi status + PIC
// fase. Sesi hanya dibuat kalau ada riwayat tanggal; tanggal hanya berupa hari,
// jadi urutan status pada hari yang sama dijaga dengan selisih 1 detik per
// langkah (ponytail: supaya urutan timeline tetap deterministik).
func buildTaskImportHistory(row *TaskImportRow, env taskImportEnv, now time.Time) (created, completed *time.Time, sessions []repository.TaskImportSession, phases []repository.TaskImportPhase) {
	dates := [4]string{row.CreatedAt, row.InProgressAt, row.UnderReviewAt, row.DoneAt}
	pics := [4]string{row.PicBacklog, row.PicInProgress, row.PicUnderReview, row.PicDone}
	cur := historyIndex(row.TaskStatus)

	type link struct {
		idx     int
		entered time.Time
	}
	var chain []link
	has := false
	for i := range dates {
		if dates[i] == "" {
			continue
		}
		has = true
		t := importDatePtr(dates[i])
		chain = append(chain, link{idx: i, entered: t.Add(time.Duration(len(chain)) * time.Second)})
	}
	if !has && cur >= 0 {
		chain = []link{{idx: cur, entered: now}}
	}

	for k := range chain {
		l := chain[k]
		var exited *time.Time
		if k+1 < len(chain) {
			e := chain[k+1].entered
			exited = &e
		}
		statusID := env.statuses[historyStatusNames[l.idx]].ID
		if has {
			sessions = append(sessions, repository.TaskImportSession{
				StatusID: statusID, EnteredAt: l.entered, ExitedAt: exited,
				WorkStarted: l.idx == 1 || l.idx == 2 || exited != nil,
			})
		}
		if pics[l.idx] == "" {
			continue
		}
		isCurrent := k == len(chain)-1 && l.idx == cur
		active := isCurrent && env.statuses[row.TaskStatus].RequirePic
		deactivated := exited
		if isCurrent && !active {
			// status tanpa kewajiban PIC (mis. DONE): PIC tercatat tapi tidak aktif.
			e := l.entered
			deactivated = &e
		}
		for _, email := range strings.Split(pics[l.idx], ";") {
			if email == "" {
				continue
			}
			phases = append(phases, repository.TaskImportPhase{
				StatusID: statusID, UserID: env.assignees[email], ActivatedAt: l.entered, DeactivatedAt: deactivated, Active: active,
			})
		}
	}
	if has {
		c := chain[0].entered
		created = &c
		if cur == 3 {
			d := chain[len(chain)-1].entered
			completed = &d
		}
	}
	return created, completed, sessions, phases
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
	env := taskImportEnv{statuses: map[string]repository.CustomStatus{}, sprints: map[string]repository.Sprint{}, assignees: map[string]string{}, today: time.Now().UTC().Format("2006-01-02")}
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
		if row.Description != "" {
			in.Description, _ = json.Marshal(row.Description)
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
		in.CreatedAt, in.CompletedAt, in.Sessions, in.Phases = buildTaskImportHistory(row, env, time.Now())
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
	_ = w.Write([]string{"baris", "title", "description", "status", "priority", "assignee", "start_date", "due_date", "sprint", "estimate", "story_points",
		"created_at", "in_progress_at", "under_review_at", "done_at", "pic_backlog", "pic_in_progress", "pic_under_review", "pic_done", "task_code", "hasil", "alasan"})
	for i := range rows {
		r := &rows[i]
		if onlySkipped && r.Status != "skipped" {
			continue
		}
		hasil := "BERHASIL"
		if r.Status == "skipped" {
			hasil = "DILEWATI"
		}
		_ = w.Write([]string{fmt.Sprintf("%d", r.RowNum), r.Title, r.Description, r.TaskStatus, r.Priority, r.Assignee, r.StartDate, r.DueDate, r.Sprint, r.Estimate, r.StoryPoints,
			r.CreatedAt, r.InProgressAt, r.UnderReviewAt, r.DoneAt, r.PicBacklog, r.PicInProgress, r.PicUnderReview, r.PicDone, r.TaskCode, hasil, r.Reason})
	}
	w.Flush()
	return buf.Bytes()
}
