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

// TaskHandler -- Task Management Core Phase 1, US-014. PIC Handoff/
// dependency hard-block/story-point enforcement penuh adalah Phase 2-4.
type TaskHandler struct {
	tasks  *service.TaskService
	logger *zap.Logger
}

func NewTaskHandler(tasks *service.TaskService, logger *zap.Logger) *TaskHandler {
	return &TaskHandler{tasks: tasks, logger: logger}
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

// Get menangani GET /tasks/:id.
func (h *TaskHandler) Get(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	task, err := h.tasks.Get(c.Context(), exec, c.Params("id"))
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil detail task")
	}
	return c.JSON(response.Success(taskJSON(task)))
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
	StatusID string `json:"status_id"`
}

// SetStatus menangani PUT /tasks/:id/status.
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
	if err := h.tasks.SetStatus(c.Context(), exec, taskID, body.StatusID, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal mengubah status task")
	}
	return c.JSON(response.Success(fiber.Map{"id": taskID, "status_id": body.StatusID}))
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
		"completed_at": t.CompletedAt, "assignees": assignees,
	}
}

func (h *TaskHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
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
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas project ini.", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
