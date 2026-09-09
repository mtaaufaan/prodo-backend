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

// SprintHandler -- Task Management Core Phase 1, US-013.
type SprintHandler struct {
	sprints *service.SprintService
	logger  *zap.Logger
}

func NewSprintHandler(sprints *service.SprintService, logger *zap.Logger) *SprintHandler {
	return &SprintHandler{sprints: sprints, logger: logger}
}

type sprintRequest struct {
	Name      string  `json:"name"`
	StartDate *string `json:"start_date"`
	EndDate   *string `json:"end_date"`
}

func parseSprintDate(raw *string) (*time.Time, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", *raw)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Create menangani POST /projects/:id/sprints.
func (h *SprintHandler) Create(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	var body sprintRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	startDate, err1 := parseSprintDate(body.StartDate)
	endDate, err2 := parseSprintDate(body.EndDate)
	if err1 != nil || err2 != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Format tanggal harus YYYY-MM-DD", nil))
	}

	sprint, err := h.sprints.Create(c.Context(), exec, projectID, body.Name, startDate, endDate, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal membuat sprint")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(sprintJSON(sprint)))
}

// List menangani GET /projects/:id/sprints.
func (h *SprintHandler) List(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	list, err := h.sprints.List(c.Context(), exec, projectID)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil daftar sprint")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = sprintJSON(&list[i])
	}
	return c.JSON(response.Success(data))
}

// Update menangani PUT /sprints/:id.
func (h *SprintHandler) Update(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	sprintID := c.Params("id")

	var body sprintRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	startDate, err1 := parseSprintDate(body.StartDate)
	endDate, err2 := parseSprintDate(body.EndDate)
	if err1 != nil || err2 != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Format tanggal harus YYYY-MM-DD", nil))
	}

	if err := h.sprints.Update(c.Context(), exec, sprintID, body.Name, startDate, endDate, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal memperbarui sprint")
	}
	return c.JSON(response.Success(fiber.Map{"id": sprintID}))
}

// Start menangani POST /sprints/:id/start.
func (h *SprintHandler) Start(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	if err := h.sprints.StartSprint(c.Context(), exec, c.Params("id"), actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal memulai sprint")
	}
	return c.JSON(response.Success(fiber.Map{"id": c.Params("id"), "is_active": true}))
}

// Complete menangani POST /sprints/:id/complete.
func (h *SprintHandler) Complete(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	if err := h.sprints.CompleteSprint(c.Context(), exec, c.Params("id"), actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal menyelesaikan sprint")
	}
	return c.JSON(response.Success(fiber.Map{"id": c.Params("id"), "is_active": false}))
}

// Delete menangani DELETE /sprints/:id.
func (h *SprintHandler) Delete(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	if err := h.sprints.Delete(c.Context(), exec, c.Params("id"), actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal menghapus sprint")
	}
	return c.JSON(response.Success(fiber.Map{"id": c.Params("id")}))
}

// Summary menangani GET /sprints/:id/summary (Phase 4, US-018a/S4-59).
func (h *SprintHandler) Summary(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	totalSP, unestimated, err := h.sprints.Summary(c.Context(), exec, c.Params("id"))
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil ringkasan sprint")
	}
	return c.JSON(response.Success(fiber.Map{"total_story_points": totalSP, "unestimated_count": unestimated}))
}

func sprintJSON(s *repository.Sprint) fiber.Map {
	return fiber.Map{
		"id": s.ID, "project_id": s.ProjectID, "name": s.Name,
		"start_date": s.StartDate, "end_date": s.EndDate, "is_active": s.IsActive, "created_at": s.CreatedAt,
	}
}

func (h *SprintHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid -- nama sprint wajib diisi", nil))
	case errors.Is(err, domain.ErrSprintNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Sprint tidak ditemukan", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas project ini.", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
