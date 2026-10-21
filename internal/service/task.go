// Package service -- TaskService (Task Management Core Phase 1, US-014;
// PIC Handoff Phase 2, US-017; completeness+dependency hard-block Phase 3,
// US-017c/018; story point permission gate + status time tracking +
// regression Phase 4, US-018a/018b/018c). Lihat komentar migrasi
// 20260924090000_task_core_phase1.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

var validFibonacciSP = map[int]bool{1: true, 2: true, 3: true, 5: true, 8: true, 13: true}
var validTaskPriority = map[string]bool{"critical": true, "high": true, "medium": true, "low": true}

// taskRepository -- interface didefinisikan di consumer, §3.9.
type taskRepository interface {
	Create(ctx context.Context, exec db.Executor, projectID string, sprintID, parentTaskID *string, statusID, title string, description json.RawMessage, priority string, startDate, dueDate *time.Time, estimatedHours *float64, storyPoints *int, createdBy string, assigneeUserIDs []string, actorRole, workspaceID string) (*repository.Task, error)
	Get(ctx context.Context, exec db.Executor, taskID string) (*repository.Task, error)
	List(ctx context.Context, exec db.Executor, projectID string, f repository.TaskFilter) ([]repository.Task, error)
	Update(ctx context.Context, exec db.Executor, taskID, title string, description json.RawMessage, priority string, startDate, dueDate *time.Time, estimatedHours *float64, storyPoints *int, sprintID *string, actorID, actorRole, workspaceID string) error
	SetStatus(ctx context.Context, exec db.Executor, taskID, statusID string, isDone bool, actorID, actorRole, workspaceID, statusBefore, statusAfter string) error
	SetPosition(ctx context.Context, exec db.Executor, taskID string, position float64) error
	SetCompleteness(ctx context.Context, exec db.Executor, taskID, completeness, actorID, actorRole, workspaceID string) error
	SoftDelete(ctx context.Context, exec db.Executor, taskID, actorID, actorRole, workspaceID string) error
	GetProjectID(ctx context.Context, exec db.Executor, taskID string) (string, error)
	IsOwner(ctx context.Context, exec db.Executor, taskID, userID string) (bool, error)
	AssignUser(ctx context.Context, exec db.Executor, taskID, userID, assignedBy string) error
	CreateVersionSnapshot(ctx context.Context, exec db.Executor, taskID, title string, description json.RawMessage, changedBy, trigger string) error
	ListVersionSnapshots(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskVersionSnapshot, error)
	ListAudit(ctx context.Context, exec db.Executor, taskID string, limit, offset int) ([]repository.AuditEntry, int, error)
}

// taskRuleEvaluator -- reuse RuleService.Evaluate (S4W-11). Best-effort,
// TIDAK PERNAH menggagalkan mutasi task yang sudah berhasil -- pola sama
// projectWebhookDispatcher, cuma di-log kalau gagal. Diisi lewat setter
// RuleService.SetTaskActions/main.go karena TaskService<->RuleService
// saling butuh satu sama lain (RuleService butuh TaskService untuk
// menjalankan action change_status/assign/create_subtask) -- dependency
// melingkar dipecah lewat setter, bukan constructor param, keduanya tetap
// di package `service` yang sama jadi tidak ada import cycle.
type taskRuleEvaluator interface {
	Evaluate(ctx context.Context, exec db.Executor, scopeType, scopeID, event string, task *repository.Task, actorID, actorRole string)
}

// taskPicRepository -- reuse TaskPicRepository (Phase 2). Interface
// didefinisikan di consumer -- cuma method yang dipakai TaskService.
type taskPicRepository interface {
	DeactivateActiveForTask(ctx context.Context, exec db.Executor, taskID string) error
	CreatePhase(ctx context.Context, exec db.Executor, taskID, statusID, userID string, assignedBy *string) error
	ListGroupForStatus(ctx context.Context, exec db.Executor, projectID, statusID string) ([]repository.PicGroupMember, error)
	IsActivePic(ctx context.Context, exec db.Executor, taskID, userID string) (bool, error)
}

// taskDependencyChecker -- reuse TaskDependencyRepository (Phase 3).
// Interface didefinisikan di consumer -- cuma method yang dipakai
// TaskService.SetStatus (HARD-BLOCK + notify successor PIC, S4-48/50).
type taskDependencyChecker interface {
	ListIncompletePredecessors(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskDependency, error)
	NotifySuccessorPics(ctx context.Context, exec db.Executor, taskID string, unblocked bool) error
}

// taskProjectResolver -- reuse ProjectRepository.GetWorkspaceID +
// GetAllowEditorStoryPoints (Phase 4, S4-56).
type taskProjectResolver interface {
	GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error)
	GetAllowEditorStoryPoints(ctx context.Context, exec db.Executor, projectID string) (bool, error)
}

// taskCustomStatuses -- reuse CustomStatusRepository. GetBacklogStatus
// (Track S5B) diresolve dari scope project -- setiap project sudah punya
// salinan status sendiri (CloneForProject/migrasi backfill), TIDAK lagi
// scope workspace.
type taskCustomStatuses interface {
	GetBacklogStatus(ctx context.Context, exec db.Executor, scopeType, scopeID string) (*repository.CustomStatus, error)
	Get(ctx context.Context, exec db.Executor, statusID string) (*repository.CustomStatus, error)
}

// taskSprintChecker -- reuse SprintRepository.Get (susulan
// domain.ErrTaskNotInSprint) -- dipanggil setStatusCore untuk tahu status
// sprint yang ditautkan task (kalau ada) sebelum mengizinkan task keluar
// dari BACKLOG.
type taskSprintChecker interface {
	Get(ctx context.Context, exec db.Executor, sprintID string) (*repository.Sprint, error)
}

// taskStatusSessionRepository -- reuse TaskStatusSessionRepository (Phase
// 4, US-018b/018c). Interface didefinisikan di consumer -- cuma method
// yang dipakai TaskService.
type taskStatusSessionRepository interface {
	OpenSession(ctx context.Context, exec db.Executor, taskID, statusID string, isRegression bool, triggeredBy string) error
	CloseActiveSession(ctx context.Context, exec db.Executor, taskID string) error
	StartWork(ctx context.Context, exec db.Executor, taskID string) error
	ListForTask(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskStatusSession, error)
	ListForProject(ctx context.Context, exec db.Executor, projectID string) ([]repository.TaskStatusSession, error)
	NotifyRegression(ctx context.Context, exec db.Executor, taskID, projectID string) error
	NotifyApprovalPending(ctx context.Context, exec db.Executor, taskID, projectID, actorID string) error
}

// statusUnderReview -- nama status sistem tahap review (jalur approval, S7A-04).
const statusUnderReview = "UNDER REVIEW"

type TaskService struct {
	repo         taskRepository
	pics         taskPicRepository
	deps         taskDependencyChecker
	sessions     taskStatusSessionRepository
	projects     taskProjectResolver
	statuses     taskCustomStatuses
	rbac         sprintWorkspaceRoleChecker
	projectRoles sprintProjectRoleChecker
	rules        taskRuleEvaluator
	sprints      taskSprintChecker
	approvals    taskApprovalStore // diisi WithApprovals (S7A-04); nil di tes/worker yang tidak butuh approval
}

func NewTaskService(repo taskRepository, pics taskPicRepository, deps taskDependencyChecker, sessions taskStatusSessionRepository, projects taskProjectResolver, statuses taskCustomStatuses, rbac sprintWorkspaceRoleChecker, projectRoles sprintProjectRoleChecker, rules taskRuleEvaluator, sprints taskSprintChecker) *TaskService {
	return &TaskService{repo: repo, pics: pics, deps: deps, sessions: sessions, projects: projects, statuses: statuses, rbac: rbac, projectRoles: projectRoles, rules: rules, sprints: sprints}
}

// fireRules -- best-effort, nil-safe (rules bisa nil di worker/test yang
// tidak butuh execution engine). Track S5B: rule WORKSPACE dan rule
// PROJECT (task.ProjectID) SAMA-SAMA dievaluasi, aditif -- rule workspace
// tetap berlaku untuk semua project di dalamnya, rule project menambah
// otomasi khusus project itu di atasnya (dikonfirmasi user).
func (s *TaskService) fireRules(ctx context.Context, exec db.Executor, workspaceID, event string, task *repository.Task, actorID, actorRole string) {
	if s.rules == nil {
		return
	}
	s.rules.Evaluate(ctx, exec, "workspace", workspaceID, event, task, actorID, actorRole)
	s.rules.Evaluate(ctx, exec, "project", task.ProjectID, event, task, actorID, actorRole)
}

// authorize -- identik SprintService.authorize (viewer/division_viewer
// ditolak) -- duplikasi kecil disengaja, pola sama ProjectService/
// ProjectMemberService yang masing-masing punya authorize sendiri, bukan
// satu helper lintas-service untuk satu pengecekan sederhana.
//
// MENGEMBALIKAN role hasil resolve (IG-94/IG-97, sama fix IG-92 untuk
// Sprint) -- rute task SENGAJA tanpa middleware RequireRole (route
// berbasis :projectId, bukan :wsId), jadi parameter actorRole mentah dari
// handler SELALU string kosong. Audit trail task HARUS pakai role hasil
// resolve ini, bukan actorRole mentah, supaya tidak mengulang bug
// actor_role kosong yang sama seperti ditemukan di Sprint (IG-92).
func (s *TaskService) authorize(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) (string, error) {
	return s.resolveRole(ctx, exec, projectID, actorID, actorRole)
}

// resolveRole -- role project_scoped_role/workspace_role aktor di project
// ini ("" untuk PA/GA -- Full mode selalu, tidak perlu role spesifik).
// Dipakai authorize() DAN SetStatus (Phase 2: PIC Handoff Bebas vs Terbatas
// -- glossary "Editor/Approver TIDAK bisa pilih PIC bebas").
func (s *TaskService) resolveRole(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) (string, error) {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return "", nil
	}
	if role, found, err := s.projectRoles.GetRole(ctx, exec, projectID, actorID); err == nil && found {
		if role == "viewer" {
			return "", fmt.Errorf("service.resolveRole: %w", domain.ErrForbidden)
		}
		return role, nil
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return "", fmt.Errorf("service.resolveRole: %w", err)
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return "", fmt.Errorf("service.resolveRole: %w", err)
	}
	if role == "viewer" || role == "division_viewer" {
		return "", fmt.Errorf("service.resolveRole: %w", domain.ErrForbidden)
	}
	return role, nil
}

// taskOwnerChecker -- cukup IsOwner; dipakai TaskService dan TaskPicService.
type taskOwnerChecker interface {
	IsOwner(ctx context.Context, exec db.Executor, taskID, userID string) (bool, error)
}

// requireOwnTask -- gerbang role Editor (S6E-06, IG-137; desain "PM Board.dc.html" limitToMine): Editor hanya
// boleh mengubah task miliknya (assignee atau PIC aktif); selain itu ErrNotYourTask. Role lain tidak
// terpengaruh. role = hasil resolveRole/authorizeProject (editor workspace ATAU project-scoped editor).
func requireOwnTask(ctx context.Context, exec db.Executor, owners taskOwnerChecker, role, taskID, userID string) error {
	if role != "editor" {
		return nil
	}
	owner, err := owners.IsOwner(ctx, exec, taskID, userID)
	if err != nil {
		return fmt.Errorf("service.requireOwnTask: %w", err)
	}
	if !owner {
		return fmt.Errorf("service.requireOwnTask: %w", domain.ErrNotYourTask)
	}
	return nil
}

// requireTaskWriter -- gerbang tulis task (S7A-02, IG-148; matriks RBAC No 8, 9, 10a, 13): Approver
// tidak membuat/mengubah task. Ubah status/PIC oleh Approver diatur terpisah di endpoint approval
// (S7A-04/06). Role lain tidak terpengaruh. role = hasil resolveRole/authorize.
func requireTaskWriter(role string) error {
	if role == "approver" {
		return fmt.Errorf("service.requireTaskWriter: %w", domain.ErrForbidden)
	}
	return nil
}

// canDeleteTask -- matriks RBAC baris 14 "Hapus task": hanya Admin Workspace dan Project Manager
// (role kosong = Platform/Group Admin). Editor dan Approver tidak.
func canDeleteTask(role string) bool {
	return role == "" || role == "admin_workspace" || role == "project_manager"
}

func validatePriority(priority string) (string, error) {
	priority = strings.ToLower(strings.TrimSpace(priority))
	if priority == "" {
		priority = "medium"
	}
	if !validTaskPriority[priority] {
		return "", fmt.Errorf("service.validatePriority: %w", domain.ErrInvalidInput)
	}
	return priority, nil
}

func validateStoryPoints(sp *int) error {
	if sp != nil && !validFibonacciSP[*sp] {
		return fmt.Errorf("service.validateStoryPoints: %w", domain.ErrInvalidInput)
	}
	return nil
}

// canSetStoryPoints -- Phase 4 (US-018a/S4-56): "PM mengisi; Editor bisa
// diizinkan via project setting". AW/GA/PA selalu boleh (super-role di atas
// PM dalam hierarki workspace/org); Editor cuma boleh kalau project
// mengizinkan (allow_editor_story_points); role lain (Approver/viewer)
// tidak pernah boleh.
func canSetStoryPoints(role string, allowEditorSP bool) bool {
	if role == "" || role == "admin_workspace" || role == "project_manager" {
		return true
	}
	return role == "editor" && allowEditorSP
}

func equalIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Create -- desain "PM Add Task.dc.html": judul wajib, minimal satu
// assignee, status default BACKLOG (S4-13). Story point Fibonacci-only
// (opsional, NULL = belum diestimasi). Memicu rule trigger "task_created"
// (S4W-11, US-049) setelah berhasil -- best-effort, tidak menggagalkan
// pembuatan task.
func (s *TaskService) Create(ctx context.Context, exec db.Executor, projectID, title string, description json.RawMessage, priority string, startDate, dueDate *time.Time, estimatedHours *float64, storyPoints *int, sprintID *string, assigneeUserIDs []string, actorID, actorRole string) (*repository.Task, error) {
	title = strings.TrimSpace(title)
	if projectID == "" || len(title) < 3 {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrInvalidInput)
	}
	priority, err := validatePriority(priority)
	if err != nil {
		return nil, err
	}
	if err := validateStoryPoints(storyPoints); err != nil {
		return nil, err
	}
	auditRole, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return nil, err
	}
	if err := requireTaskWriter(auditRole); err != nil {
		return nil, err
	}
	// S6E-08 (IG-139): Editor yang tidak menyebut assignee otomatis menjadi assignee task-nya sendiri
	// (spek "assignee default dirinya"); role lain tetap wajib menyebut minimal satu assignee.
	if len(assigneeUserIDs) == 0 && auditRole == "editor" {
		assigneeUserIDs = []string{actorID}
	}
	if len(assigneeUserIDs) == 0 {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrTaskAssigneeRequired)
	}
	// Story point saat membuat mengikuti aturan yang sama dengan Update (US-018a): Editor hanya bila
	// project mengizinkan.
	if storyPoints != nil {
		allowEditorSP, err := s.projects.GetAllowEditorStoryPoints(ctx, exec, projectID)
		if err != nil {
			return nil, fmt.Errorf("service.Create: %w", err)
		}
		if !canSetStoryPoints(auditRole, allowEditorSP) {
			return nil, fmt.Errorf("service.Create: %w", domain.ErrStoryPointsNotAllowed)
		}
	}

	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	backlog, err := s.statuses.GetBacklogStatus(ctx, exec, "project", projectID)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}

	task, err := s.repo.Create(ctx, exec, projectID, sprintID, nil, backlog.ID, title, description, priority, startDate, dueDate, estimatedHours, storyPoints, actorID, assigneeUserIDs, auditRole, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	s.fireRules(ctx, exec, workspaceID, "task_created", task, actorID, actorRole)
	return task, nil
}

func (s *TaskService) Get(ctx context.Context, exec db.Executor, taskID string) (*repository.Task, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.Get: %w", domain.ErrInvalidInput)
	}
	task, err := s.repo.Get(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.Get: %w", err)
	}
	return task, nil
}

func (s *TaskService) List(ctx context.Context, exec db.Executor, projectID string, f repository.TaskFilter) ([]repository.Task, error) {
	if projectID == "" {
		return nil, fmt.Errorf("service.List: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.List(ctx, exec, projectID, f)
	if err != nil {
		return nil, fmt.Errorf("service.List: %w", err)
	}
	return list, nil
}

// Update -- Phase 4 (S4-55/56): story_points digerbangi TERPISAH dari field
// lain -- gate cuma aktif kalau NILAI benar-benar berubah dari yang
// tersimpan (FE selalu mengirim story_points current di form edit, bukan
// cuma saat sengaja diubah -- gate literal "field dikirim" akan salah
// menolak edit judul/priority biasa untuk Editor tanpa izin SP).
func (s *TaskService) Update(ctx context.Context, exec db.Executor, taskID, title string, description json.RawMessage, priority string, startDate, dueDate *time.Time, estimatedHours *float64, storyPoints *int, sprintID *string, actorID, actorRole string) error {
	title = strings.TrimSpace(title)
	if taskID == "" || len(title) < 3 {
		return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
	}
	priority, err := validatePriority(priority)
	if err != nil {
		return err
	}
	if err := validateStoryPoints(storyPoints); err != nil {
		return err
	}
	current, err := s.repo.Get(ctx, exec, taskID)
	if err != nil {
		return err
	}
	role, err := s.resolveRole(ctx, exec, current.ProjectID, actorID, actorRole)
	if err != nil {
		return err
	}
	if err := requireTaskWriter(role); err != nil {
		return err
	}
	if err := requireOwnTask(ctx, exec, s.repo, role, taskID, actorID); err != nil {
		return err
	}
	if !equalIntPtr(current.StoryPoints, storyPoints) {
		allowEditorSP, err := s.projects.GetAllowEditorStoryPoints(ctx, exec, current.ProjectID)
		if err != nil {
			return fmt.Errorf("service.Update: %w", err)
		}
		if !canSetStoryPoints(role, allowEditorSP) {
			return fmt.Errorf("service.Update: %w", domain.ErrStoryPointsNotAllowed)
		}
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, current.ProjectID)
	if err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	// descriptionChanged (IG-97, RIWAYAT VERSI): snapshot diambil SEBELUM
	// disimpan (DATABASE_SCHEMA.md §5.19) -- HANYA saat title/deskripsi
	// benar-benar berubah, bukan tiap edit priority/due/SP (desain
	// menyiratkan versi = "setiap perubahan deskripsi", bukan tiap edit
	// field apa pun).
	descriptionChanged := current.Title != title || !bytes.Equal(current.Description, description)
	if descriptionChanged {
		trigger := "Judul diubah"
		if !bytes.Equal(current.Description, description) {
			trigger = "Deskripsi diubah"
		}
		if err := s.repo.CreateVersionSnapshot(ctx, exec, taskID, current.Title, current.Description, actorID, trigger); err != nil {
			return fmt.Errorf("service.Update: %w", err)
		}
	}
	if err := s.repo.Update(ctx, exec, taskID, title, description, priority, startDate, dueDate, estimatedHours, storyPoints, sprintID, actorID, role, workspaceID); err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	return nil
}

// isFullPicMode -- glossary "Phase PIC Handoff": AW/PM/GA/PA = Bebas
// (pilih siapa saja); Editor/Approver = Terbatas KECUALI PIC Group untuk
// status ini kosong (fallback ke Bebas, §5.34).
func isFullPicMode(role string) bool {
	return role == "" || role == "admin_workspace" || role == "project_manager"
}

// SetStatus -- Phase 2 (S4-31/32): WAJIB pilih PIC baru tiap ganti status
// (AC Bruno: "PUT tanpa pic_ids -> 422 pic_required"). PIC lama (kalau ada)
// dinonaktifkan, PIC baru dibuat fase PENDING (belum acknowledge). Editor/
// Approver dibatasi ke PIC Group status tujuan (fallback Bebas kalau
// kosong) -- PM/AW/GA/PA selalu Bebas. Status ber-mode UNDEFINED tidak
// bisa dipilih (sama aturan desain "PM Add Task.dc.html").
//
// Phase 3 (S4-43/48/50): DUA guard tambahan sebelum status benar-benar
// berubah -- (1) completeness: task BACKLOG dengan completeness=incomplete
// tidak boleh pindah KECUALI ke BLOCKED; (2) dependency HARD-BLOCK: task
// dengan predecessor yang belum DONE tidak boleh pindah ke status apa pun
// KECUALI BACKLOG/BLOCKED. Setelah status berubah, successor LANGSUNG
// task ini diberi tahu kalau task ini baru masuk/keluar DONE (S4-50).
func (s *TaskService) SetStatus(ctx context.Context, exec db.Executor, taskID, statusID string, picIDs []string, actorID, actorRole string) error {
	return s.setStatusWith(ctx, exec, taskID, statusID, picIDs, actorID, actorRole, statusOpts{fireRules: true, denyApprover: true})
}

// isTaskInWorkableSprint (susulan domain.ErrTaskNotInSprint) -- true HANYA
// kalau sprintID mengarah ke sprint yang SEDANG 'active' (bukan 'backlog'
// sekalipun, koreksi setelah user menguji live: task Sprint 1 yang belum
// dimulai -- Sprint 0 sedang aktif -- tetap ditolak, "sprint backlog" di
// permintaan awal ternyata berarti task TANPA sprint sama sekali, BUKAN
// sprint yang statusnya 'backlog'). sprintID kosong (task belum pernah
// ditarik dari product backlog sama sekali) SELALU false -- tidak ada
// sprint untuk dicek statusnya.
func (s *TaskService) isTaskInWorkableSprint(ctx context.Context, exec db.Executor, sprintID *string) (bool, error) {
	if sprintID == nil || *sprintID == "" {
		return false, nil
	}
	sprint, err := s.sprints.Get(ctx, exec, *sprintID)
	if err != nil {
		return false, fmt.Errorf("service.isTaskInWorkableSprint: %w", err)
	}
	return sprint.Status == "active", nil
}

// setStatusCore -- isi asli SetStatus, sekarang dipakai BERSAMA
// SetStatusForRule (S4W-11) lewat parameter fireRules -- satu-satunya
// pembeda: rule-triggered TIDAK memicu evaluasi rule lagi (mencegah
// infinite loop), guard/validasi bisnis (dependency, completeness, PIC
// Group) TETAP SAMA PERSIS untuk kedua jalur.
func (s *TaskService) setStatusCore(ctx context.Context, exec db.Executor, taskID, statusID string, picIDs []string, actorID, actorRole string, fireRules bool) error {
	return s.setStatusWith(ctx, exec, taskID, statusID, picIDs, actorID, actorRole, statusOpts{fireRules: fireRules})
}

// statusOpts -- variasi jalur status (S7A-04): keputusan approval memakai guard yang sama tetapi tanpa pilihan PIC
// manual (PIC fase kerja diaktifkan kembali oleh service), tanpa PIC Group, dan audit task.approved/task.rejected.
type statusOpts struct {
	fireRules    bool
	skipPicGroup bool
	skipDeps     bool
	// denyApprover -- jalur status biasa (SetStatus publik): Approver ditolak ErrUseApproval (S7A-06).
	denyApprover bool
	// approval != nil -> status diubah lewat TaskApprovalRepository.SetStatusByApproval dengan audit approval.
	approval *approvalAudit
}

type approvalAudit struct{ action, note string }

func (s *TaskService) setStatusWith(ctx context.Context, exec db.Executor, taskID, statusID string, picIDs []string, actorID, actorRole string, o statusOpts) error {
	if taskID == "" || statusID == "" {
		return fmt.Errorf("service.SetStatus: %w", domain.ErrInvalidInput)
	}
	projectID, err := s.repo.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return err
	}
	role, err := s.resolveRole(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return err
	}
	if o.denyApprover && role == "approver" {
		return fmt.Errorf("service.SetStatus: %w", domain.ErrUseApproval)
	}
	if err := requireOwnTask(ctx, exec, s.repo, role, taskID, actorID); err != nil {
		return err
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("service.SetStatus: %w", err)
	}
	status, err := s.statuses.Get(ctx, exec, statusID)
	if err != nil {
		return err
	}
	if status.IsUndefined {
		return fmt.Errorf("service.SetStatus: %w", domain.ErrTaskStatusUndefined)
	}
	// require_pic (parameter per status): status akhir seperti DONE/BLOCKED
	// tidak butuh PIC -- pilihan PIC yang ikut terkirim diabaikan, PIC fase
	// sebelumnya tetap dinonaktifkan di bawah (riwayat PIC tetap tersimpan).
	if status.RequirePic {
		if len(picIDs) == 0 {
			return fmt.Errorf("service.SetStatus: %w", domain.ErrPicRequired)
		}
	} else {
		picIDs = nil
	}

	current, err := s.repo.Get(ctx, exec, taskID)
	if err != nil {
		return err
	}
	if current.StatusName == "BACKLOG" && current.Completeness != nil && *current.Completeness == "incomplete" && status.Name != "BLOCKED" && status.Name != "CANCELED" {
		return fmt.Errorf("service.SetStatus: %w", domain.ErrTaskIncomplete)
	}
	if current.StatusName == "BACKLOG" && status.Name != "BLOCKED" && status.Name != "CANCELED" {
		workable, err := s.isTaskInWorkableSprint(ctx, exec, current.SprintID)
		if err != nil {
			return fmt.Errorf("service.SetStatus: %w", err)
		}
		if !workable {
			return fmt.Errorf("service.SetStatus: %w", domain.ErrTaskNotInSprint)
		}
	}
	if !o.skipDeps && status.Name != "BACKLOG" && status.Name != "BLOCKED" && status.Name != "CANCELED" {
		blocking, err := s.deps.ListIncompletePredecessors(ctx, exec, taskID)
		if err != nil {
			return fmt.Errorf("service.SetStatus: %w", err)
		}
		if len(blocking) > 0 {
			tasks := make([]domain.BlockingTaskInfo, len(blocking))
			for i := range blocking {
				b := &blocking[i]
				if b.PredecessorCode == nil {
					tasks[i] = domain.BlockingTaskInfo{TaskCode: b.PredecessorTitle, Title: b.PredecessorTitle}
				} else {
					tasks[i] = domain.BlockingTaskInfo{TaskCode: *b.PredecessorCode, Title: b.PredecessorTitle}
				}
			}
			return fmt.Errorf("service.SetStatus: %w", &domain.PredecessorBlockingError{BlockingTasks: tasks})
		}
	}

	if status.RequirePic && !isFullPicMode(role) && !o.skipPicGroup {
		group, err := s.pics.ListGroupForStatus(ctx, exec, projectID, statusID)
		if err != nil {
			return fmt.Errorf("service.SetStatus: %w", err)
		}
		if len(group) > 0 {
			allowed := make(map[string]bool, len(group))
			for _, m := range group {
				allowed[m.UserID] = true
			}
			for _, picID := range picIDs {
				if !allowed[picID] {
					return fmt.Errorf("service.SetStatus: %w", domain.ErrPicNotInGroup)
				}
			}
		}
	}

	currentStatus, err := s.statuses.Get(ctx, exec, current.StatusID)
	if err != nil {
		return err
	}
	isRegression := status.Position < currentStatus.Position

	if o.approval != nil {
		if err := s.approvals.SetStatusByApproval(ctx, exec, taskID, statusID, status.Name == "DONE", actorID, role, workspaceID, o.approval.action, current.StatusName, status.Name, o.approval.note); err != nil {
			return fmt.Errorf("service.SetStatus: %w", err)
		}
	} else if err := s.repo.SetStatus(ctx, exec, taskID, statusID, status.Name == "DONE", actorID, role, workspaceID, current.StatusName, status.Name); err != nil {
		return fmt.Errorf("service.SetStatus: %w", err)
	}
	if err := s.pics.DeactivateActiveForTask(ctx, exec, taskID); err != nil {
		return fmt.Errorf("service.SetStatus: %w", err)
	}
	for _, picID := range picIDs {
		if err := s.pics.CreatePhase(ctx, exec, taskID, statusID, picID, &actorID); err != nil {
			return fmt.Errorf("service.SetStatus: %w", err)
		}
	}

	// Phase 4 (S4-62/68): tutup sesi status lama (auto-fill work_started_at
	// kalau belum diklik, S4-67), buka sesi baru untuk status tujuan, dan
	// notify PM+AW kalau ini regresi (S4-68).
	if err := s.sessions.CloseActiveSession(ctx, exec, taskID); err != nil {
		return fmt.Errorf("service.SetStatus: %w", err)
	}
	if err := s.sessions.OpenSession(ctx, exec, taskID, statusID, isRegression, actorID); err != nil {
		return fmt.Errorf("service.SetStatus: %w", err)
	}
	if isRegression {
		if err := s.sessions.NotifyRegression(ctx, exec, taskID, projectID); err != nil {
			return fmt.Errorf("service.SetStatus: %w", err)
		}
	}
	// S7A-12 (IG-158): task MASUK UNDER REVIEW (dari perpindahan status mana pun: manual, bulk, rule) -> notifikasi
	// `approval_pending` ke Approver project, tidak ke pelaku, sekali per fase. Setelah OpenSession karena dedupe
	// memakai sesi yang baru terbuka.
	if status.Name == statusUnderReview && current.StatusName != statusUnderReview {
		if err := s.sessions.NotifyApprovalPending(ctx, exec, taskID, projectID, actorID); err != nil {
			return fmt.Errorf("service.SetStatus: %w", err)
		}
	}

	// Status akhir (DONE/CANCELED) sama-sama melepas blokir successor (IG-118);
	// DONE<->CANCELED tidak mengubah apa pun sehingga tidak ada notifikasi.
	wasFinal := current.StatusName == "DONE" || current.StatusName == "CANCELED"
	isFinal := status.Name == "DONE" || status.Name == "CANCELED"
	if isFinal != wasFinal {
		if err := s.deps.NotifySuccessorPics(ctx, exec, taskID, isFinal); err != nil {
			return fmt.Errorf("service.SetStatus: %w", err)
		}
	}

	// S4W-11 (US-049): trigger "status_changed" -- current diperbarui di
	// memori (bukan query ulang) supaya rule condition project/priority/
	// assignee dievaluasi terhadap task PASCA-perubahan. Best-effort, tidak
	// menggagalkan SetStatus yang sudah berhasil. fireRules=false untuk
	// panggilan dari SetStatusForRule sendiri (mencegah infinite loop).
	if o.fireRules {
		current.StatusID = statusID
		current.StatusName = status.Name
		s.fireRules(ctx, exec, workspaceID, "status_changed", current, actorID, actorRole)
	}
	return nil
}

// SetStatusForRule (S4W-11, action rule "Ubah status") -- dipanggil HANYA
// dari RuleService.executeAction, TIDAK PERNAH dari HTTP handler. SENGAJA
// tidak lewat SetStatus publik (yang mewajibkan picIDs manual + memicu
// fireRules lagi -- akan infinite loop kalau rule lain mendengarkan status
// yang sama). PIC baru default ke assignee task saat ini (fallback ke
// pembuat rule kalau task tidak punya assignee sama sekali, seharusnya
// tidak pernah terjadi karena assignee wajib saat Create) -- lolos guard
// dependency/completeness/PIC-Group yang SAMA seperti perubahan manual,
// supaya rule tidak bisa memaksa lompatan status yang sebetulnya diblokir
// bisnis (kegagalan tercatat sebagai eksekusi GAGAL, bukan disembunyikan).
func (s *TaskService) SetStatusForRule(ctx context.Context, exec db.Executor, taskID, statusID, actorID, actorRole string) error {
	task, err := s.repo.Get(ctx, exec, taskID)
	if err != nil {
		return fmt.Errorf("service.SetStatusForRule: %w", err)
	}
	picIDs := make([]string, 0, len(task.Assignees))
	for _, a := range task.Assignees {
		picIDs = append(picIDs, a.UserID)
	}
	if len(picIDs) == 0 {
		picIDs = []string{actorID}
	}
	return s.setStatusCore(ctx, exec, taskID, statusID, picIDs, actorID, actorRole, false)
}

// Reorder -- drag-geser kartu Kanban DALAM satu kolom status (Track S5,
// desain "PM Board.dc.html" moveTaskOrder). Pindah KOLOM tetap lewat
// SetStatus (guard PIC/dependency/completeness tidak berlaku untuk sekadar
// urut ulang). Posisi baru = titik tengah dua tetangga (fractional
// indexing, hindari re-index seluruh kolom) -- diambil dari List() yang
// sudah terurut `position` per status.
func (s *TaskService) Reorder(ctx context.Context, exec db.Executor, taskID, targetTaskID string, placeBefore bool, actorID, actorRole string) error {
	if taskID == "" || targetTaskID == "" || taskID == targetTaskID {
		return fmt.Errorf("service.Reorder: %w", domain.ErrInvalidInput)
	}
	src, err := s.repo.Get(ctx, exec, taskID)
	if err != nil {
		return err
	}
	target, err := s.repo.Get(ctx, exec, targetTaskID)
	if err != nil {
		return err
	}
	if src.StatusID != target.StatusID {
		return fmt.Errorf("service.Reorder: %w", domain.ErrInvalidInput)
	}
	role, err := s.authorize(ctx, exec, src.ProjectID, actorID, actorRole)
	if err != nil {
		return err
	}
	if err := requireTaskWriter(role); err != nil {
		return err
	}
	if err := requireOwnTask(ctx, exec, s.repo, role, taskID, actorID); err != nil {
		return err
	}
	siblings, err := s.repo.List(ctx, exec, src.ProjectID, repository.TaskFilter{StatusID: src.StatusID})
	if err != nil {
		return fmt.Errorf("service.Reorder: %w", err)
	}
	var targetIdx = -1
	for i := range siblings {
		if siblings[i].ID == targetTaskID {
			targetIdx = i
			break
		}
	}
	if targetIdx == -1 {
		return fmt.Errorf("service.Reorder: %w", domain.ErrTaskNotFound)
	}
	var neighborIdx int
	if placeBefore {
		neighborIdx = targetIdx - 1
	} else {
		neighborIdx = targetIdx + 1
	}
	newPos := target.Position + boolToSign(placeBefore)
	if neighborIdx >= 0 && neighborIdx < len(siblings) && siblings[neighborIdx].ID != taskID {
		newPos = (target.Position + siblings[neighborIdx].Position) / 2
	}
	if err := s.repo.SetPosition(ctx, exec, taskID, newPos); err != nil {
		return fmt.Errorf("service.Reorder: %w", err)
	}
	return nil
}

func boolToSign(placeBefore bool) float64 {
	if placeBefore {
		return -1
	}
	return 1
}

// BulkStatusResult -- hasil per-task dari BulkSetStatus (kartu terpilih di
// Kanban/Daftar bisa berbeda project? TIDAK -- Board selalu satu project,
// tapi task individual tetap bisa gagal beda-beda alasan: dependency,
// completeness, PIC Group).
type BulkStatusResult struct {
	TaskID string
	Err    error
}

// BulkSetStatus -- bulk action Kanban/Daftar (desain "PINDAHKAN & TETAPKAN
// PIC" saat banyak kartu dipilih sekaligus). REUSE SetStatus APA ADANYA per
// task (guard dependency/completeness/PIC-Group SAMA PERSIS, tidak ada
// jalur pintas) -- satu task gagal tidak menggagalkan task lain dalam
// batch yang sama.
func (s *TaskService) BulkSetStatus(ctx context.Context, exec db.Executor, projectID string, taskIDs []string, statusID string, picIDs []string, actorID, actorRole string) ([]BulkStatusResult, error) {
	// Role Editor dan Approver tidak boleh bulk (S6E-06, IG-137; S7A-02, IG-148): ditolak SELURUHNYA, tidak satu task pun diproses.
	role, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return nil, err
	}
	if role == "editor" || role == "approver" {
		return nil, fmt.Errorf("service.BulkSetStatus: %w", domain.ErrBulkNotAllowed)
	}
	results := make([]BulkStatusResult, 0, len(taskIDs))
	for _, id := range taskIDs {
		err := s.SetStatus(ctx, exec, id, statusID, picIDs, actorID, actorRole)
		results = append(results, BulkStatusResult{TaskID: id, Err: err})
	}
	return results, nil
}

// AssignUserForRule (S4W-11, action rule "Assign ke user") -- idempotent
// (ON CONFLICT DO NOTHING di repo). TIDAK memicu evaluasi rule lagi --
// trigger "assignee_changed" (US-049 AC) karena itu TETAP DORMANT (tidak
// ada jalur lain yang mengubah assignee di codebase ini selain Create),
// sama nasib comment_added -- lihat komentar TaskRepository.AssignUser.
func (s *TaskService) AssignUserForRule(ctx context.Context, exec db.Executor, taskID, userID, actorID, _ string) error {
	if err := s.repo.AssignUser(ctx, exec, taskID, userID, actorID); err != nil {
		return fmt.Errorf("service.AssignUserForRule: %w", err)
	}
	return nil
}

// CreateSubtaskForRule (S4W-11, action rule "Buat sub-task otomatis") --
// minimal viable: judul diberi caller (RuleService, biasanya "Sub-task
// otomatis: <nama rule>"), priority medium, tanpa assignee/due
// date/sprint, status awal BACKLOG workspace (sama seperti Create manusia
// biasa). TIDAK memicu trigger "task_created" lagi -- sub-task yang lahir
// dari rule dianggap bagian dari aksi rule itu sendiri, bukan event baru
// yang independen (konsisten prinsip "rule-triggered tidak pernah memicu
// rule lain").
func (s *TaskService) CreateSubtaskForRule(ctx context.Context, exec db.Executor, projectID, parentTaskID, title, actorID string) error {
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("service.CreateSubtaskForRule: %w", err)
	}
	backlog, err := s.statuses.GetBacklogStatus(ctx, exec, "project", projectID)
	if err != nil {
		return fmt.Errorf("service.CreateSubtaskForRule: %w", err)
	}
	if _, err := s.repo.Create(ctx, exec, projectID, nil, &parentTaskID, backlog.ID, title, nil, "medium", nil, nil, nil, nil, actorID, nil, "", workspaceID); err != nil {
		return fmt.Errorf("service.CreateSubtaskForRule: %w", err)
	}
	return nil
}

// StartWork menangani POST /tasks/:id/start-work (Phase 4, US-018b/S4-63).
func (s *TaskService) StartWork(ctx context.Context, exec db.Executor, taskID, actorID, actorRole string) error {
	if taskID == "" {
		return fmt.Errorf("service.StartWork: %w", domain.ErrInvalidInput)
	}
	projectID, err := s.repo.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return err
	}
	role, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return err
	}
	// Matriks RBAC 10b: Editor Terbatas (task miliknya), Approver Terbatas (hanya saat PIC aktif) -- S7A-06, IG-152.
	if err := requireOwnTask(ctx, exec, s.repo, role, taskID, actorID); err != nil {
		return err
	}
	if role == "approver" {
		isPic, err := s.pics.IsActivePic(ctx, exec, taskID, actorID)
		if err != nil {
			return fmt.Errorf("service.StartWork: %w", err)
		}
		if !isPic {
			return fmt.Errorf("service.StartWork: %w", domain.ErrForbidden)
		}
	}
	if err := s.sessions.StartWork(ctx, exec, taskID); err != nil {
		return fmt.Errorf("service.StartWork: %w", err)
	}
	return nil
}

// ListVersions menangani GET /tasks/:id/versions (IG-97, tab RIWAYAT VERSI).
func (s *TaskService) ListVersions(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskVersionSnapshot, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.ListVersions: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.ListVersionSnapshots(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.ListVersions: %w", err)
	}
	return list, nil
}

// ListActivity menangani GET /tasks/:id/activity (IG-94/IG-97, tab
// AKTIVITAS) -- paginasi offset sederhana, default 20/halaman.
func (s *TaskService) ListActivity(ctx context.Context, exec db.Executor, taskID string, page, perPage int) ([]repository.AuditEntry, int, error) {
	if taskID == "" {
		return nil, 0, fmt.Errorf("service.ListActivity: %w", domain.ErrInvalidInput)
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	list, total, err := s.repo.ListAudit(ctx, exec, taskID, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, fmt.Errorf("service.ListActivity: %w", err)
	}
	return list, total, nil
}

// ListStatusSessions menangani GET /tasks/:id/status-sessions (Phase 4,
// FE StatusTimeline S4-66 -- Queue/Active/Lead Time dihitung di klien dari
// raw session rows).
func (s *TaskService) ListStatusSessions(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskStatusSession, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.ListStatusSessions: %w", domain.ErrInvalidInput)
	}
	list, err := s.sessions.ListForTask(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.ListStatusSessions: %w", err)
	}
	return list, nil
}

// ListStatusSessionsForProject menangani GET /projects/:id/status-sessions
// (menu Board tab Gantt, bar ACTUAL -- US-039/H22-24): SEMUA sesi status
// SELURUH task project ini sekaligus, pola sama List (tasks) -- TIDAK ada
// pengecekan otorisasi tambahan, scoping lewat RLS tasks/task_status_sessions.
func (s *TaskService) ListStatusSessionsForProject(ctx context.Context, exec db.Executor, projectID string) ([]repository.TaskStatusSession, error) {
	if projectID == "" {
		return nil, fmt.Errorf("service.ListStatusSessionsForProject: %w", domain.ErrInvalidInput)
	}
	list, err := s.sessions.ListForProject(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.ListStatusSessionsForProject: %w", err)
	}
	return list, nil
}

// SetCompleteness menangani PUT /tasks/:id/completeness (Phase 3, S4-44) --
// hanya pembuat task ATAU PIC aktif yang boleh mengubah flag ini.
func (s *TaskService) SetCompleteness(ctx context.Context, exec db.Executor, taskID, completeness, actorID, actorRole string) error {
	if taskID == "" || (completeness != "complete" && completeness != "incomplete") {
		return fmt.Errorf("service.SetCompleteness: %w", domain.ErrCompletenessInvalid)
	}
	task, err := s.repo.Get(ctx, exec, taskID)
	if err != nil {
		return err
	}
	// IG-126: hanya PIC aktif fase BACKLOG (task harus berstatus BACKLOG, jadi
	// PIC aktifnya adalah PIC backlog). Pembuat task / PM yang bukan PIC tidak
	// lagi boleh -- PM dapat menambahkan diri sebagai PIC paralel dulu.
	role, err := s.resolveRole(ctx, exec, task.ProjectID, actorID, actorRole)
	if err != nil {
		return err
	}
	if err := requireTaskWriter(role); err != nil {
		return err
	}
	if err := requireOwnTask(ctx, exec, s.repo, role, taskID, actorID); err != nil {
		return err
	}
	if task.StatusName != "BACKLOG" {
		return fmt.Errorf("service.SetCompleteness: %w", domain.ErrNotBacklogPic)
	}
	isPic, err := s.pics.IsActivePic(ctx, exec, taskID, actorID)
	if err != nil {
		return fmt.Errorf("service.SetCompleteness: %w", err)
	}
	if !isPic {
		return fmt.Errorf("service.SetCompleteness: %w", domain.ErrNotBacklogPic)
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, task.ProjectID)
	if err != nil {
		return fmt.Errorf("service.SetCompleteness: %w", err)
	}
	if err := s.repo.SetCompleteness(ctx, exec, taskID, completeness, actorID, role, workspaceID); err != nil {
		return fmt.Errorf("service.SetCompleteness: %w", err)
	}
	return nil
}

func (s *TaskService) Delete(ctx context.Context, exec db.Executor, taskID, actorID, actorRole string) error {
	if taskID == "" {
		return fmt.Errorf("service.Delete: %w", domain.ErrInvalidInput)
	}
	projectID, err := s.repo.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return err
	}
	auditRole, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return err
	}
	if !canDeleteTask(auditRole) {
		return fmt.Errorf("service.Delete: %w", domain.ErrForbidden)
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	if err := s.repo.SoftDelete(ctx, exec, taskID, actorID, auditRole, workspaceID); err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	return nil
}
