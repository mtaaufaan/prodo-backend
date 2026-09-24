// Package handler -- TimeEntryHandler (Timesheet, IG-97/US-036/037,
// API_CONTRACT.md §15). 8 endpoint persis draft kontrak yang sudah ada
// sejak awal (start/stop timer, manual CRUD, approve/reject, list, active).
package handler

import (
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

type TimeEntryHandler struct {
	entries *service.TimeEntryService
	logger  *zap.Logger
}

func NewTimeEntryHandler(entries *service.TimeEntryService, logger *zap.Logger) *TimeEntryHandler {
	return &TimeEntryHandler{entries: entries, logger: logger}
}

func timeEntryJSON(e *repository.TimeEntry) fiber.Map {
	status := "pending"
	if e.IsApproved != nil {
		if *e.IsApproved {
			status = "approved"
		} else {
			status = "rejected"
		}
	}
	return fiber.Map{
		"id": e.ID, "task_id": e.TaskID,
		"user":             fiber.Map{"user_id": e.UserID, "display_name": e.UserName, "email": e.UserEmail},
		"entry_type":       e.EntryType,
		"started_at":       e.StartedAt,
		"ended_at":         e.EndedAt,
		"duration_minutes": e.DurationMinutes,
		"note":             e.Note,
		"approval_status":  status,
		"rejection_note":   e.RejectionNote,
		"created_at":       e.CreatedAt,
		"updated_at":       e.UpdatedAt,
	}
}

// StartTimer menangani POST /tasks/:id/time-entries/start.
func (h *TimeEntryHandler) StartTimer(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	taskID := c.Params("id")
	timer, err := h.entries.StartTimer(c.Context(), exec, taskID, actorUserID)
	if err != nil {
		if errors.Is(err, domain.ErrTimerAlreadyRunning) {
			active, _ := h.entries.GetActive(c.Context(), exec, taskID, actorUserID)
			details := fiber.Map{}
			if active != nil {
				details["active_timer"] = fiber.Map{"task_id": active.TaskID, "task_code": active.TaskCode, "started_at": active.StartedAt}
			}
			return c.Status(fiber.StatusConflict).JSON(response.Error("TIMER_ALREADY_RUNNING", "Anda sudah memiliki timer aktif.", details))
		}
		return h.mapError(c, err, "Gagal memulai timer")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{
		"timer_id": timer.UserID, "task_id": timer.TaskID, "started_at": timer.StartedAt,
	}))
}

// StopTimer menangani POST /tasks/:id/time-entries/stop.
func (h *TimeEntryHandler) StopTimer(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	entry, err := h.entries.StopTimer(c.Context(), exec, c.Params("id"), actorUserID)
	if err != nil {
		return h.mapError(c, err, "Gagal menghentikan timer")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(timeEntryJSON(entry)))
}

// GetActive menangani GET /tasks/:id/time-entries/active.
func (h *TimeEntryHandler) GetActive(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	active, err := h.entries.GetActive(c.Context(), exec, c.Params("id"), actorUserID)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil timer aktif")
	}
	if active == nil {
		return c.JSON(response.Success(nil))
	}
	elapsed := int(time.Since(active.StartedAt).Seconds())
	return c.JSON(response.Success(fiber.Map{
		"timer_id": active.UserID, "task_id": active.TaskID, "started_at": active.StartedAt, "elapsed_seconds": elapsed,
	}))
}

type manualTimeEntryRequest struct {
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Note      *string   `json:"note"`
}

// CreateManual menangani POST /tasks/:id/time-entries.
func (h *TimeEntryHandler) CreateManual(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	var body manualTimeEntryRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	entry, err := h.entries.CreateManual(c.Context(), exec, c.Params("id"), actorUserID, body.StartedAt, body.EndedAt, body.Note)
	if err != nil {
		return h.mapError(c, err, "Gagal menambah entri waktu")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(timeEntryJSON(entry)))
}

// UpdateManual menangani PATCH /time-entries/:entryId.
func (h *TimeEntryHandler) UpdateManual(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	var body manualTimeEntryRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	entry, err := h.entries.UpdateManual(c.Context(), exec, c.Params("entryId"), actorUserID, body.StartedAt, body.EndedAt, body.Note)
	if err != nil {
		return h.mapError(c, err, "Gagal mengubah entri waktu")
	}
	return c.JSON(response.Success(timeEntryJSON(entry)))
}

// Approve menangani POST /time-entries/:entryId/approve.
func (h *TimeEntryHandler) Approve(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	entryID := c.Params("entryId")
	if err := h.entries.Approve(c.Context(), exec, entryID, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal menyetujui entri waktu")
	}
	return c.JSON(response.Success(fiber.Map{"id": entryID, "approval_status": "approved", "approved_by": actorUserID}))
}

type rejectTimeEntryRequest struct {
	RejectionNote string `json:"rejection_note"`
}

// Reject menangani POST /time-entries/:entryId/reject.
func (h *TimeEntryHandler) Reject(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	var body rejectTimeEntryRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	entryID := c.Params("entryId")
	if err := h.entries.Reject(c.Context(), exec, entryID, actorUserID, actorRole, body.RejectionNote); err != nil {
		return h.mapError(c, err, "Gagal menolak entri waktu")
	}
	return c.JSON(response.Success(fiber.Map{"id": entryID, "approval_status": "rejected", "rejection_note": body.RejectionNote, "rejected_by": actorUserID}))
}

// List menangani GET /tasks/:id/time-entries.
func (h *TimeEntryHandler) List(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	list, err := h.entries.ListForTask(c.Context(), exec, c.Params("id"), c.Query("approval_status"), c.Query("user_id"), actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil entri waktu")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = timeEntryJSON(&list[i])
	}
	return c.JSON(response.Success(fiber.Map{"items": data, "total": len(data)}))
}

func (h *TimeEntryHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrTimerAlreadyRunning):
		return c.Status(fiber.StatusConflict).JSON(response.Error("TIMER_ALREADY_RUNNING", "Anda sudah memiliki timer aktif.", nil))
	case errors.Is(err, domain.ErrNoActiveTimer):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NO_ACTIVE_TIMER", "Tidak ada timer aktif untuk task ini.", nil))
	case errors.Is(err, domain.ErrTimeEntryOverlap):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("TIME_ENTRY_OVERLAP", "Rentang waktu ini overlap dengan entri yang sudah ada.", nil))
	case errors.Is(err, domain.ErrTimeEntryAlreadyApproved):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("TIME_ENTRY_ALREADY_APPROVED", "Entri waktu yang sudah di-approve tidak dapat diubah.", nil))
	case errors.Is(err, domain.ErrTimeEntryNotPending):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("TIME_ENTRY_NOT_PENDING", "Entri waktu ini sudah diputuskan sebelumnya.", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas entri waktu ini.", nil))
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid.", nil))
	case errors.Is(err, domain.ErrTaskNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Task tidak ditemukan", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
