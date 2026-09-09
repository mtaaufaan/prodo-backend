package handler

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// TaskHandler -- Task Management Core Phase 1/2/3 (US-014, US-017 PIC
// Handoff, US-017c completeness + US-018 dependencies). Story-point/time
// tracking enforcement penuh adalah Phase 4.
type TaskHandler struct {
	tasks  *service.TaskService
	pics   *service.TaskPicService
	deps   *service.TaskDependencyService
	logger *zap.Logger
}

func NewTaskHandler(tasks *service.TaskService, pics *service.TaskPicService, deps *service.TaskDependencyService, logger *zap.Logger) *TaskHandler {
	return &TaskHandler{tasks: tasks, pics: pics, deps: deps, logger: logger}
}

type taskRequest struct {
	Title          string          `json:"title"`
	Description    json.RawMessage `json:"description"`
	Priority       string          `json:"priority"`
	DueDate        *string         `json:"due_date"`
	EstimatedHours *float64        `json:"estimated_hours"`
	StoryPoints    *int            `json:"story_points"`
	SprintID       *string         `json:"sprint_id"`
	AssigneeIDs    []string        `json:"assignee_ids"`
}

func parseTaskDate(raw *string) (*time.Time, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", *raw)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Create menangani POST /projects/:id/tasks.
func (h *TaskHandler) Create(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	var body taskRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	dueDate, err := parseTaskDate(body.DueDate)
	if err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Format due_date harus YYYY-MM-DD", nil))
	}

	task, err := h.tasks.Create(c.Context(), exec, projectID, body.Title, body.Description, body.Priority, dueDate, body.EstimatedHours, body.StoryPoints, body.SprintID, body.AssigneeIDs, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal membuat task")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(taskJSON(task)))
}

// List menangani GET /projects/:id/tasks?status_id=&priority=&sprint_id=&assignee_id=.
func (h *TaskHandler) List(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	filter := repository.TaskFilter{
		StatusID: c.Query("status_id"), Priority: c.Query("priority"),
		SprintID: c.Query("sprint_id"), AssigneeID: c.Query("assignee_id"),
	}
	list, err := h.tasks.List(c.Context(), exec, projectID, filter)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil daftar task")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = taskJSON(&list[i])
	}
	return c.JSON(response.Success(data))
}

// Get menangani GET /tasks/:id. Menyertakan PIC aktif (Phase 2).
func (h *TaskHandler) Get(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	taskID := c.Params("id")
	task, err := h.tasks.Get(c.Context(), exec, taskID)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil detail task")
	}
	pics, err := h.pics.ListActive(c.Context(), exec, taskID)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil PIC aktif")
	}
	data := taskJSON(task)
	data["active_pics"] = picPhasesJSON(pics)
	return c.JSON(response.Success(data))
}

// Update menangani PUT /tasks/:id.
func (h *TaskHandler) Update(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	taskID := c.Params("id")

	var body taskRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	dueDate, err := parseTaskDate(body.DueDate)
	if err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Format due_date harus YYYY-MM-DD", nil))
	}

	if err := h.tasks.Update(c.Context(), exec, taskID, body.Title, body.Description, body.Priority, dueDate, body.EstimatedHours, body.StoryPoints, body.SprintID, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal memperbarui task")
	}
	return c.JSON(response.Success(fiber.Map{"id": taskID}))
}

type taskStatusRequest struct {
	StatusID string   `json:"status_id"`
	PicIDs   []string `json:"pic_ids"`
}

// SetStatus menangani PUT /tasks/:id/status -- Phase 2: pic_ids WAJIB
// (S4-32 AC), lihat komentar TaskService.SetStatus.
func (h *TaskHandler) SetStatus(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	taskID := c.Params("id")

	var body taskStatusRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	if err := h.tasks.SetStatus(c.Context(), exec, taskID, body.StatusID, body.PicIDs, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal mengubah status task")
	}
	return c.JSON(response.Success(fiber.Map{"id": taskID, "status_id": body.StatusID}))
}

// Acknowledge menangani POST /tasks/:id/pic/acknowledge (S4-33).
func (h *TaskHandler) Acknowledge(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	taskID := c.Params("id")
	if err := h.pics.Acknowledge(c.Context(), exec, taskID, actorUserID); err != nil {
		return h.mapError(c, err, "Gagal mengonfirmasi serah terima PIC")
	}
	return c.JSON(response.Success(fiber.Map{"id": taskID}))
}

// PicHistory menangani GET /tasks/:id/pic-history.
func (h *TaskHandler) PicHistory(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	list, err := h.pics.ListHistory(c.Context(), exec, c.Params("id"))
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil riwayat PIC")
	}
	return c.JSON(response.Success(picPhasesJSON(list)))
}

// StartWork menangani POST /tasks/:id/start-work (Phase 4, US-018b/S4-63).
func (h *TaskHandler) StartWork(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	taskID := c.Params("id")
	if err := h.tasks.StartWork(c.Context(), exec, taskID, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal memulai pengerjaan task")
	}
	return c.JSON(response.Success(fiber.Map{"id": taskID}))
}

// StatusSessions menangani GET /tasks/:id/status-sessions (Phase 4, FE
// StatusTimeline S4-66).
func (h *TaskHandler) StatusSessions(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	list, err := h.tasks.ListStatusSessions(c.Context(), exec, c.Params("id"))
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil riwayat sesi status task")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		s := &list[i]
		data[i] = fiber.Map{
			"id": s.ID, "task_id": s.TaskID, "status_id": s.StatusID, "status_name": s.StatusName,
			"session_no": s.SessionNo, "entered_at": s.EnteredAt, "work_started_at": s.WorkStartedAt,
			"is_auto_start": s.IsAutoStart, "exited_at": s.ExitedAt, "is_regression": s.IsRegression,
			"triggered_by": s.TriggeredBy,
		}
	}
	return c.JSON(response.Success(data))
}

type taskCompletenessRequest struct {
	Completeness string `json:"completeness"`
}

// Completeness menangani PUT /tasks/:id/completeness (Phase 3, S4-44).
func (h *TaskHandler) Completeness(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	taskID := c.Params("id")

	var body taskCompletenessRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	if err := h.tasks.SetCompleteness(c.Context(), exec, taskID, body.Completeness, actorUserID); err != nil {
		return h.mapError(c, err, "Gagal mengubah status kelengkapan task")
	}
	return c.JSON(response.Success(fiber.Map{"id": taskID, "completeness": body.Completeness}))
}

// Dependencies menangani GET /tasks/:id/dependencies (Phase 3, US-018).
func (h *TaskHandler) Dependencies(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	predecessors, successors, err := h.deps.List(c.Context(), exec, c.Params("id"))
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil dependency task")
	}
	return c.JSON(response.Success(fiber.Map{
		"predecessors": dependencyEndpointsJSON(predecessors, true),
		"successors":   dependencyEndpointsJSON(successors, false),
	}))
}

type taskDependencyRequest struct {
	PredecessorTaskID string `json:"predecessor_task_id"`
}

// AddDependency menangani POST /tasks/:id/dependencies (Phase 3, S4-51).
func (h *TaskHandler) AddDependency(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	taskID := c.Params("id")

	var body taskDependencyRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	dep, err := h.deps.Add(c.Context(), exec, taskID, body.PredecessorTaskID, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal menambah dependency task")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{
		"predecessor_id": dep.PredecessorID, "successor_id": dep.SuccessorID, "created_at": dep.CreatedAt,
	}))
}

// RemoveDependency menangani DELETE /tasks/:id/dependencies/:predecessorId.
func (h *TaskHandler) RemoveDependency(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	if err := h.deps.Remove(c.Context(), exec, c.Params("id"), c.Params("predecessorId"), actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal menghapus dependency task")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func dependencyEndpointsJSON(list []repository.TaskDependency, wantPredecessor bool) []fiber.Map {
	data := make([]fiber.Map, len(list))
	for i := range list {
		d := &list[i]
		if wantPredecessor {
			data[i] = fiber.Map{"task_id": d.PredecessorID, "task_code": d.PredecessorCode, "title": d.PredecessorTitle, "status": d.PredecessorStatusName}
		} else {
			data[i] = fiber.Map{"task_id": d.SuccessorID, "task_code": d.SuccessorCode, "title": d.SuccessorTitle, "status": d.SuccessorStatusName}
		}
	}
	return data
}

// Delete menangani DELETE /tasks/:id.
func (h *TaskHandler) Delete(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	taskID := c.Params("id")
	if err := h.tasks.Delete(c.Context(), exec, taskID, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal menghapus task")
	}
	return c.JSON(response.Success(fiber.Map{"id": taskID}))
}

func taskJSON(t *repository.Task) fiber.Map {
	assignees := make([]fiber.Map, len(t.Assignees))
	for i, a := range t.Assignees {
		assignees[i] = fiber.Map{"user_id": a.UserID, "display_name": a.DisplayName, "email": a.Email, "role": a.Role}
	}
	return fiber.Map{
		"id": t.ID, "project_id": t.ProjectID, "sprint_id": t.SprintID, "sprint_name": t.SprintName,
		"parent_task_id": t.ParentTaskID, "status_id": t.StatusID, "status_name": t.StatusName, "status_color": t.StatusColor,
		"title": t.Title, "description": t.Description, "priority": t.Priority, "completeness": t.Completeness,
		"due_date": t.DueDate, "estimated_hours": t.EstimatedHours, "story_points": t.StoryPoints,
		"task_code": t.TaskCode, "created_by": t.CreatedBy, "created_at": t.CreatedAt, "updated_at": t.UpdatedAt,
		"completed_at": t.CompletedAt, "is_blocked": t.IsBlocked, "regression_count": t.RegressionCount, "assignees": assignees,
	}
}

func picPhasesJSON(list []repository.TaskPicPhase) []fiber.Map {
	data := make([]fiber.Map, len(list))
	for i := range list {
		p := &list[i]
		data[i] = fiber.Map{
			"id": p.ID, "task_id": p.TaskID, "status_id": p.StatusID, "status_name": p.StatusName,
			"user_id": p.UserID, "user_name": p.UserName, "user_email": p.UserEmail,
			"is_active": p.IsActive, "acknowledged_at": p.AcknowledgedAt,
			"activated_at": p.ActivatedAt, "deactivated_at": p.DeactivatedAt, "assigned_by": p.AssignedBy,
		}
	}
	return data
}

func (h *TaskHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	var blockErr *domain.PredecessorBlockingError
	var cycleErr *domain.CircularDependencyError
	switch {
	case errors.As(err, &blockErr):
		blocking := make([]fiber.Map, len(blockErr.BlockingTasks))
		for i, t := range blockErr.BlockingTasks {
			blocking[i] = fiber.Map{"task_code": t.TaskCode, "title": t.Title}
		}
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("DEPENDENCY_HARD_BLOCK", "Task ini memiliki predecessor yang belum selesai.", fiber.Map{"blocking_tasks": blocking}))
	case errors.As(err, &cycleErr):
		return c.Status(fiber.StatusConflict).JSON(response.Error("CIRCULAR_DEPENDENCY", "Menambahkan dependency ini akan membuat circular dependency.", fiber.Map{"cycle_path": cycleErr.CyclePath}))
	case errors.Is(err, domain.ErrTaskIncomplete):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("TASK_INCOMPLETE", "Task ini masih ditandai Belum Lengkap -- selesaikan kelengkapannya dulu sebelum mengubah status.", nil))
	case errors.Is(err, domain.ErrCompletenessInvalid):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "completeness harus 'complete' atau 'incomplete'", nil))
	case errors.Is(err, domain.ErrDependencySelfReference):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("DEPENDENCY_SELF_REFERENCE", "Task tidak bisa menjadi predecessor untuk dirinya sendiri.", nil))
	case errors.Is(err, domain.ErrDependencyCrossProject):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("DEPENDENCY_CROSS_PROJECT", "Dependency hanya bisa dibuat antar task dalam project yang sama.", nil))
	case errors.Is(err, domain.ErrDependencyAlreadyExists):
		return c.Status(fiber.StatusConflict).JSON(response.Error("DEPENDENCY_ALREADY_EXISTS", "Dependency ini sudah ada.", nil))
	case errors.Is(err, domain.ErrDependencyNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Dependency tidak ditemukan", nil))
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid -- judul task minimal 3 karakter, priority harus critical/high/medium/low, dan story point (kalau diisi) harus salah satu dari 1/2/3/5/8/13", nil))
	case errors.Is(err, domain.ErrTaskAssigneeRequired):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("ASSIGNEE_REQUIRED", "Pilih minimal satu assignee -- task tanpa penanggung jawab tidak dapat disimpan.", nil))
	case errors.Is(err, domain.ErrTaskStatusUndefined):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("STATUS_UNDEFINED", "Status ini sudah dihapus dan tidak bisa dipilih lagi.", nil))
	case errors.Is(err, domain.ErrTaskNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Task tidak ditemukan", nil))
	case errors.Is(err, domain.ErrCustomStatusNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Status tidak ditemukan", nil))
	case errors.Is(err, domain.ErrPicRequired):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("PIC_REQUIRED", "Pilih minimal satu PIC untuk fase status baru ini.", nil))
	case errors.Is(err, domain.ErrPicNotInGroup):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("PIC_NOT_IN_GROUP", "PIC Group status ini belum memuat member yang Anda pilih. Minta Project Manager menambah anggota PIC Group.", nil))
	case errors.Is(err, domain.ErrNotActivePic):
		return c.Status(fiber.StatusConflict).JSON(response.Error("NOT_ACTIVE_PIC", "Anda bukan PIC aktif task ini, atau sudah mengonfirmasi sebelumnya.", nil))
	case errors.Is(err, domain.ErrStoryPointsNotAllowed):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("STORY_POINTS_NOT_ALLOWED", "Anda tidak berwenang mengubah story point task ini -- hanya PM/AW atau Editor yang diizinkan project ini.", nil))
	case errors.Is(err, domain.ErrNoActiveStatusSession):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Task tidak memiliki sesi status aktif.", nil))
	case errors.Is(err, domain.ErrWorkAlreadyStarted):
		return c.Status(fiber.StatusConflict).JSON(response.Error("ALREADY_STARTED", "Pengerjaan task ini sudah dimulai sebelumnya.", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas project ini.", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
