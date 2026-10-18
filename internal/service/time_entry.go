// Package service -- TimeEntryService (Timesheet, dimajukan dari Sprint S8
// asli ke Track S5/IG-97, US-036/037, API_CONTRACT.md §15). Start/stop
// timer (satu timer per user, PK active_timers=user_id), entri manual +
// approval workflow (AW/PM only). Chip header "PM Task Detail.dc.html"
// (LOGGED/ESTIMASI JAM) dan field RINGKASAN "JAM TERCATAT" dihitung dari
// SumLoggedMinutesForTask (HANYA entri approved).
package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// timeEntryRepository -- interface didefinisikan di consumer, §3.9.
type timeEntryRepository interface {
	StartTimer(ctx context.Context, exec db.Executor, taskID, userID string) (*repository.ActiveTimer, error)
	GetActiveTimer(ctx context.Context, exec db.Executor, userID string) (*repository.ActiveTimer, error)
	StopTimer(ctx context.Context, exec db.Executor, taskID, userID string) (*repository.TimeEntry, error)
	CreateManual(ctx context.Context, exec db.Executor, taskID, userID string, startedAt, endedAt time.Time, note *string) (*repository.TimeEntry, error)
	CreateLogged(ctx context.Context, exec db.Executor, taskID, userID string, startedAt, endedAt time.Time, note *string) (*repository.TimeEntry, error)
	DayUsage(ctx context.Context, exec db.Executor, userID string, dayStart, dayEnd time.Time) (minutes int, lastEnd *time.Time, err error)
	SumUserMinutesForTask(ctx context.Context, exec db.Executor, taskID, userID string) (int, error)
	HasOverlap(ctx context.Context, exec db.Executor, userID string, startedAt, endedAt time.Time, excludeID string) (bool, error)
	Get(ctx context.Context, exec db.Executor, id string) (*repository.TimeEntry, error)
	UpdateManual(ctx context.Context, exec db.Executor, id string, startedAt, endedAt time.Time, note *string) (*repository.TimeEntry, error)
	Approve(ctx context.Context, exec db.Executor, id, approvedBy string) error
	Reject(ctx context.Context, exec db.Executor, id, rejectedBy, note string) error
	ListForTask(ctx context.Context, exec db.Executor, taskID, approvalStatus, userID string) ([]repository.TimeEntry, error)
	SumLoggedMinutesForTask(ctx context.Context, exec db.Executor, taskID string) (int, error)
	ListApproverIDs(ctx context.Context, exec db.Executor, taskID string) ([]string, error)
	TaskLabel(ctx context.Context, exec db.Executor, taskID string) (code, title string, err error)
	Notify(ctx context.Context, exec db.Executor, userID, actorID, notifType, entryID, title, body string) error
	LogAudit(ctx context.Context, exec db.Executor, actorID, actorRole, action, taskID, workspaceID string, before, after map[string]any) error
}

// timeEntryTaskResolver -- reuse TaskRepository.GetProjectID + IsOwner (gerbang Editor, S6E-10).
type timeEntryTaskResolver interface {
	GetProjectID(ctx context.Context, exec db.Executor, taskID string) (string, error)
	taskOwnerChecker
}

type TimeEntryService struct {
	repo         timeEntryRepository
	tasks        timeEntryTaskResolver
	projects     taskProjectResolver
	rbac         sprintWorkspaceRoleChecker
	projectRoles sprintProjectRoleChecker
}

func NewTimeEntryService(repo timeEntryRepository, tasks timeEntryTaskResolver, projects taskProjectResolver, rbac sprintWorkspaceRoleChecker, projectRoles sprintProjectRoleChecker) *TimeEntryService {
	return &TimeEntryService{repo: repo, tasks: tasks, projects: projects, rbac: rbac, projectRoles: projectRoles}
}

// authorizeLogger -- siapa yang boleh mencatat waktu pada task (S6E-10, IG-141): PA/GA dan AW/PM boleh;
// Editor hanya pada task miliknya (assignee atau PIC aktif; selain itu ErrNotYourTask); role lain
// (Approver, Viewer, Division Viewer, bukan anggota) ErrForbidden. Mengembalikan role efektif untuk audit
// (role rute kosong untuk non-PA/GA, pelajaran IG-92).
func (s *TimeEntryService) authorizeLogger(ctx context.Context, exec db.Executor, taskID, actorID, actorRole string) (string, error) {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return actorRole, nil
	}
	projectID, err := s.tasks.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return "", err
	}
	role, found, err := s.projectRoles.GetRole(ctx, exec, projectID, actorID)
	if err != nil || !found {
		workspaceID, werr := s.projects.GetWorkspaceID(ctx, exec, projectID)
		if werr != nil {
			return "", werr
		}
		if role, err = s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID); err != nil {
			return "", err
		}
	}
	if err := checkTimeLogger(ctx, exec, s.tasks, role, taskID, actorID); err != nil {
		return "", err
	}
	return role, nil
}

// checkTimeLogger -- aturan murni (diuji tabel keputusan); role kosong = bukan anggota -> ditolak.
func checkTimeLogger(ctx context.Context, exec db.Executor, owners taskOwnerChecker, role, taskID, actorID string) error {
	switch role {
	case "admin_workspace", "project_manager":
		return nil
	case "editor":
		return requireOwnTask(ctx, exec, owners, role, taskID, actorID)
	}
	return fmt.Errorf("service.checkTimeLogger: %w", domain.ErrForbidden)
}

const (
	// maxLoggedHours -- batas satu pencatatan jam (desain "TM Catat Waktu": maksimal 12 jam per entri).
	maxLoggedHours = 12.0
	// maxNoteRunes -- panjang catatan entri.
	maxNoteRunes = 500
	// maxDayMinutes -- total jam tercatat satu user per hari (WIB), mencegah data mustahil.
	maxDayMinutes = 24 * 60
)

// wib -- zona waktu pencatatan (Indonesia tidak mengenal DST; sama konvensi estimasi IG-129: 09:00 WIB).
var wib = time.FixedZone("WIB", 7*3600)

// LoggedHours -- hasil POST /tasks/:id/time-log.
type LoggedHours struct {
	Entry            *repository.TimeEntry
	TaskCode         string
	TaskTotalMinutes int
}

// LogHours menangani POST /tasks/:id/time-log (S6E-10, IG-141): catat JAM (maks 12) + catatan sebagai entri
// manual untuk HARI INI (WIB) yang langsung disetujui. Tanpa jam mulai/selesai dari pengguna: entri
// ditempatkan berurutan setelah entri terakhir hari itu (paling cepat 09:00 WIB) sehingga tidak tumpang tindih.
func (s *TimeEntryService) LogHours(ctx context.Context, exec db.Executor, taskID, actorID, actorRole string, hours float64, note string, now time.Time) (*LoggedHours, error) {
	minutes := int(math.Round(hours * 60))
	note = strings.TrimSpace(note)
	if taskID == "" || math.IsNaN(hours) || math.IsInf(hours, 0) || hours <= 0 || hours > maxLoggedHours || minutes < 1 || utf8.RuneCountInString(note) > maxNoteRunes {
		return nil, fmt.Errorf("service.LogHours: %w", domain.ErrInvalidInput)
	}
	role, err := s.authorizeLogger(ctx, exec, taskID, actorID, actorRole)
	if err != nil {
		return nil, fmt.Errorf("service.LogHours: %w", err)
	}

	dayStart := time.Date(now.In(wib).Year(), now.In(wib).Month(), now.In(wib).Day(), 0, 0, 0, 0, wib)
	usedMinutes, lastEnd, err := s.repo.DayUsage(ctx, exec, actorID, dayStart, dayStart.Add(24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("service.LogHours: %w", err)
	}
	if usedMinutes+minutes > maxDayMinutes {
		return nil, fmt.Errorf("service.LogHours: %w", domain.ErrDailyTimeLimit)
	}
	start := dayStart.Add(9 * time.Hour)
	if lastEnd != nil && lastEnd.After(start) {
		start = *lastEnd
	}
	end := start.Add(time.Duration(minutes) * time.Minute)
	overlap, err := s.repo.HasOverlap(ctx, exec, actorID, start, end, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		return nil, fmt.Errorf("service.LogHours: %w", err)
	}
	if overlap {
		return nil, fmt.Errorf("service.LogHours: %w", domain.ErrTimeEntryOverlap)
	}

	var notePtr *string
	if note != "" {
		notePtr = &note
	}
	entry, err := s.repo.CreateLogged(ctx, exec, taskID, actorID, start, end, notePtr)
	if err != nil {
		return nil, fmt.Errorf("service.LogHours: %w", err)
	}
	workspaceID, err := s.workspaceOf(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.LogHours: %w", err)
	}
	if err := s.repo.LogAudit(ctx, exec, actorID, role, "task.time_entry_added", taskID, workspaceID, nil, entrySnapshot(entry, "approved")); err != nil {
		return nil, fmt.Errorf("service.LogHours: audit: %w", err)
	}
	code, _, err := s.repo.TaskLabel(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.LogHours: %w", err)
	}
	total, err := s.repo.SumUserMinutesForTask(ctx, exec, taskID, actorID)
	if err != nil {
		return nil, fmt.Errorf("service.LogHours: %w", err)
	}
	return &LoggedHours{Entry: entry, TaskCode: code, TaskTotalMinutes: total}, nil
}

// isApprover -- AW/PM di workspace project ini, atau PA/GA (bypass penuh)
// -- satu-satunya role yang boleh lihat entri user lain/approve/reject
// (API_CONTRACT.md §15).
func (s *TimeEntryService) isApprover(ctx context.Context, exec db.Executor, taskID, actorID, actorRole string) (bool, error) {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return true, nil
	}
	projectID, err := s.tasks.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return false, err
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return false, err
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return false, err
	}
	return role == "admin_workspace" || role == "project_manager", nil
}

// maxManualEntry -- batas satu entri manual (kolom duration_minutes SMALLINT
// juga tidak muat > ~22 hari; 24 jam cukup untuk satu sesi kerja).
const maxManualEntry = 24 * time.Hour

func validEntryRange(startedAt, endedAt time.Time) bool {
	return endedAt.After(startedAt) && endedAt.Sub(startedAt) <= maxManualEntry
}

// workspaceOf -- workspace pemilik task (kolom workspace_id audit_logs).
func (s *TimeEntryService) workspaceOf(ctx context.Context, exec db.Executor, taskID string) (string, error) {
	projectID, err := s.tasks.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return "", err
	}
	return s.projects.GetWorkspaceID(ctx, exec, projectID)
}

// entrySnapshot -- state audit: menit + status, cukup untuk narasi.
func entrySnapshot(e *repository.TimeEntry, status string) map[string]any {
	m := map[string]any{"entry_id": e.ID, "approval_status": status, "entry_type": e.EntryType}
	if e.DurationMinutes != nil {
		m["duration_minutes"] = *e.DurationMinutes
	}
	return m
}

// notifyApprovers -- US-037 AC: PM/Approver menerima notifikasi in-app saat
// ada entri menunggu persetujuan (pembuat entri sendiri dikecualikan).
func (s *TimeEntryService) notifyApprovers(ctx context.Context, exec db.Executor, e *repository.TimeEntry, actorID, verb string) error {
	ids, err := s.repo.ListApproverIDs(ctx, exec, e.TaskID)
	if err != nil {
		return err
	}
	code, title, err := s.repo.TaskLabel(ctx, exec, e.TaskID)
	if err != nil {
		return err
	}
	mins := 0
	if e.DurationMinutes != nil {
		mins = *e.DurationMinutes
	}
	heading := fmt.Sprintf("Entri waktu menunggu persetujuan -- %s", code)
	body := fmt.Sprintf("Entri waktu manual %s: %s (%d menit) pada task %q.", verb, e.StartedAt.Format("02 Jan 2006 15:04"), mins, title)
	for _, id := range ids {
		if id == actorID {
			continue
		}
		if err := s.repo.Notify(ctx, exec, id, actorID, "timesheet_pending", e.ID, heading, body); err != nil {
			return err
		}
	}
	return nil
}

// StartTimer menangani POST /tasks/:id/time-entries/start.
func (s *TimeEntryService) StartTimer(ctx context.Context, exec db.Executor, taskID, actorID, actorRole string) (*repository.ActiveTimer, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.StartTimer: %w", domain.ErrInvalidInput)
	}
	if _, err := s.authorizeLogger(ctx, exec, taskID, actorID, actorRole); err != nil {
		return nil, fmt.Errorf("service.StartTimer: %w", err)
	}
	timer, err := s.repo.StartTimer(ctx, exec, taskID, actorID)
	if err != nil {
		return nil, fmt.Errorf("service.StartTimer: %w", err)
	}
	return timer, nil
}

// GetActive menangani GET /tasks/:id/time-entries/active -- nil (bukan
// error) kalau user tidak punya timer aktif SAMA SEKALI, atau timer
// aktifnya ternyata untuk task lain (API_CONTRACT.md: "Cek apakah user
// yang sedang login memiliki timer aktif UNTUK TASK INI").
func (s *TimeEntryService) GetActive(ctx context.Context, exec db.Executor, taskID, actorID string) (*repository.ActiveTimer, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.GetActive: %w", domain.ErrInvalidInput)
	}
	timer, err := s.repo.GetActiveTimer(ctx, exec, actorID)
	if err != nil {
		return nil, fmt.Errorf("service.GetActive: %w", err)
	}
	if timer == nil || timer.TaskID != taskID {
		return nil, nil
	}
	return timer, nil
}

// StopTimer menangani POST /tasks/:id/time-entries/stop.
func (s *TimeEntryService) StopTimer(ctx context.Context, exec db.Executor, taskID, actorID string) (*repository.TimeEntry, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.StopTimer: %w", domain.ErrInvalidInput)
	}
	entry, err := s.repo.StopTimer(ctx, exec, taskID, actorID)
	if err != nil {
		return nil, fmt.Errorf("service.StopTimer: %w", err)
	}
	return entry, nil
}

// CreateManual menangani POST /tasks/:id/time-entries.
func (s *TimeEntryService) CreateManual(ctx context.Context, exec db.Executor, taskID, actorID, actorRole string, startedAt, endedAt time.Time, note *string) (*repository.TimeEntry, error) {
	if taskID == "" || !validEntryRange(startedAt, endedAt) {
		return nil, fmt.Errorf("service.CreateManual: %w", domain.ErrInvalidInput)
	}
	auditRole, err := s.authorizeLogger(ctx, exec, taskID, actorID, actorRole)
	if err != nil {
		return nil, fmt.Errorf("service.CreateManual: %w", err)
	}
	overlap, err := s.repo.HasOverlap(ctx, exec, actorID, startedAt, endedAt, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		return nil, fmt.Errorf("service.CreateManual: %w", err)
	}
	if overlap {
		return nil, fmt.Errorf("service.CreateManual: %w", domain.ErrTimeEntryOverlap)
	}
	entry, err := s.repo.CreateManual(ctx, exec, taskID, actorID, startedAt, endedAt, note)
	if err != nil {
		return nil, fmt.Errorf("service.CreateManual: %w", err)
	}
	workspaceID, err := s.workspaceOf(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.CreateManual: %w", err)
	}
	if err := s.repo.LogAudit(ctx, exec, actorID, auditRole, "task.time_entry_added", taskID, workspaceID, nil, entrySnapshot(entry, "pending")); err != nil {
		return nil, fmt.Errorf("service.CreateManual: audit: %w", err)
	}
	if err := s.notifyApprovers(ctx, exec, entry, actorID, "ditambahkan"); err != nil {
		return nil, fmt.Errorf("service.CreateManual: notify: %w", err)
	}
	return entry, nil
}

// UpdateManual menangani PATCH /time-entries/:id -- hanya pemilik entri,
// hanya saat masih pending atau ditolak (repo.UpdateManual menggerbangi
// is_approved, di sini cukup cek kepemilikan).
func (s *TimeEntryService) UpdateManual(ctx context.Context, exec db.Executor, entryID, actorID, actorRole string, startedAt, endedAt time.Time, note *string) (*repository.TimeEntry, error) {
	if entryID == "" || !validEntryRange(startedAt, endedAt) {
		return nil, fmt.Errorf("service.UpdateManual: %w", domain.ErrInvalidInput)
	}
	current, err := s.repo.Get(ctx, exec, entryID)
	if err != nil {
		return nil, fmt.Errorf("service.UpdateManual: %w", err)
	}
	if current.UserID != actorID {
		return nil, fmt.Errorf("service.UpdateManual: %w", domain.ErrForbidden)
	}
	overlap, err := s.repo.HasOverlap(ctx, exec, actorID, startedAt, endedAt, entryID)
	if err != nil {
		return nil, fmt.Errorf("service.UpdateManual: %w", err)
	}
	if overlap {
		return nil, fmt.Errorf("service.UpdateManual: %w", domain.ErrTimeEntryOverlap)
	}
	entry, err := s.repo.UpdateManual(ctx, exec, entryID, startedAt, endedAt, note)
	if err != nil {
		return nil, fmt.Errorf("service.UpdateManual: %w", err)
	}
	workspaceID, err := s.workspaceOf(ctx, exec, entry.TaskID)
	if err != nil {
		return nil, fmt.Errorf("service.UpdateManual: %w", err)
	}
	if err := s.repo.LogAudit(ctx, exec, actorID, actorRole, "task.time_entry_updated", entry.TaskID, workspaceID,
		entrySnapshot(current, approvalStatusOf(current)), entrySnapshot(entry, "pending")); err != nil {
		return nil, fmt.Errorf("service.UpdateManual: audit: %w", err)
	}
	// Entri yang diperbaiki setelah ditolak masuk antrian lagi -> approver diberi tahu.
	if err := s.notifyApprovers(ctx, exec, entry, actorID, "diperbarui"); err != nil {
		return nil, fmt.Errorf("service.UpdateManual: notify: %w", err)
	}
	return entry, nil
}

func approvalStatusOf(e *repository.TimeEntry) string {
	switch {
	case e.IsApproved == nil:
		return "pending"
	case *e.IsApproved:
		return "approved"
	default:
		return "rejected"
	}
}

// afterDecision -- audit trail + notifikasi ke pemilik entri setelah approve/
// reject (US-037 AC "seluruh aksi approval dicatat di audit trail", S8-15
// "entry ditolak -> notify user").
func (s *TimeEntryService) afterDecision(ctx context.Context, exec db.Executor, entry *repository.TimeEntry, actorID, actorRole, decision, note string) error {
	workspaceID, err := s.workspaceOf(ctx, exec, entry.TaskID)
	if err != nil {
		return err
	}
	after := entrySnapshot(entry, decision)
	if note != "" {
		after["rejection_note"] = note
	}
	action := "task.time_entry_approved"
	if decision == "rejected" {
		action = "task.time_entry_rejected"
	}
	if err := s.repo.LogAudit(ctx, exec, actorID, actorRole, action, entry.TaskID, workspaceID, entrySnapshot(entry, "pending"), after); err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	if entry.UserID == actorID {
		return nil
	}
	code, title, err := s.repo.TaskLabel(ctx, exec, entry.TaskID)
	if err != nil {
		return err
	}
	heading := fmt.Sprintf("Entri waktu disetujui -- %s", code)
	body := fmt.Sprintf("Entri waktu Anda pada task %q disetujui dan kini terhitung di jam tercatat.", title)
	notifType := "timesheet_approved"
	if decision == "rejected" {
		heading = fmt.Sprintf("Entri waktu ditolak -- %s", code)
		body = fmt.Sprintf("Entri waktu Anda pada task %q ditolak. Catatan: %s. Anda dapat memperbaikinya lalu mengirim ulang.", title, note)
		notifType = "timesheet_rejected"
	}
	return s.repo.Notify(ctx, exec, entry.UserID, actorID, notifType, entry.ID, heading, body)
}

// Approve menangani POST /time-entries/:id/approve -- AW/PM saja.
func (s *TimeEntryService) Approve(ctx context.Context, exec db.Executor, entryID, actorID, actorRole string) error {
	if entryID == "" {
		return fmt.Errorf("service.Approve: %w", domain.ErrInvalidInput)
	}
	entry, err := s.repo.Get(ctx, exec, entryID)
	if err != nil {
		return fmt.Errorf("service.Approve: %w", err)
	}
	ok, err := s.isApprover(ctx, exec, entry.TaskID, actorID, actorRole)
	if err != nil {
		return fmt.Errorf("service.Approve: %w", err)
	}
	if !ok {
		return fmt.Errorf("service.Approve: %w", domain.ErrForbidden)
	}
	if err := s.repo.Approve(ctx, exec, entryID, actorID); err != nil {
		return fmt.Errorf("service.Approve: %w", err)
	}
	if err := s.afterDecision(ctx, exec, entry, actorID, actorRole, "approved", ""); err != nil {
		return fmt.Errorf("service.Approve: %w", err)
	}
	return nil
}

// Reject menangani POST /time-entries/:id/reject -- AW/PM saja, wajib
// rejection_note (US-037 AC, sama CHECK constraint DB ck_rejection_note).
func (s *TimeEntryService) Reject(ctx context.Context, exec db.Executor, entryID, actorID, actorRole, note string) error {
	if entryID == "" || note == "" {
		return fmt.Errorf("service.Reject: %w", domain.ErrInvalidInput)
	}
	entry, err := s.repo.Get(ctx, exec, entryID)
	if err != nil {
		return fmt.Errorf("service.Reject: %w", err)
	}
	ok, err := s.isApprover(ctx, exec, entry.TaskID, actorID, actorRole)
	if err != nil {
		return fmt.Errorf("service.Reject: %w", err)
	}
	if !ok {
		return fmt.Errorf("service.Reject: %w", domain.ErrForbidden)
	}
	if err := s.repo.Reject(ctx, exec, entryID, actorID, note); err != nil {
		return fmt.Errorf("service.Reject: %w", err)
	}
	if err := s.afterDecision(ctx, exec, entry, actorID, actorRole, "rejected", note); err != nil {
		return fmt.Errorf("service.Reject: %w", err)
	}
	return nil
}

// ListForTask menangani GET /tasks/:id/time-entries -- AW/PM lihat semua
// (opsional filter userID), user lain SELALU dipaksa filter ke diri
// sendiri (API_CONTRACT.md: "user lain hanya milik sendiri") apa pun
// filterUserID yang diminta.
func (s *TimeEntryService) ListForTask(ctx context.Context, exec db.Executor, taskID, approvalStatus, filterUserID, actorID, actorRole string) ([]repository.TimeEntry, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.ListForTask: %w", domain.ErrInvalidInput)
	}
	approver, err := s.isApprover(ctx, exec, taskID, actorID, actorRole)
	if err != nil {
		return nil, fmt.Errorf("service.ListForTask: %w", err)
	}
	if !approver {
		filterUserID = actorID
	}
	list, err := s.repo.ListForTask(ctx, exec, taskID, approvalStatus, filterUserID)
	if err != nil {
		return nil, fmt.Errorf("service.ListForTask: %w", err)
	}
	return list, nil
}

// SumLoggedMinutes -- dipakai TaskHandler (chip header + field RINGKASAN
// "JAM TERCATAT"), bukan endpoint HTTP sendiri.
func (s *TimeEntryService) SumLoggedMinutes(ctx context.Context, exec db.Executor, taskID string) (int, error) {
	return s.repo.SumLoggedMinutesForTask(ctx, exec, taskID)
}
