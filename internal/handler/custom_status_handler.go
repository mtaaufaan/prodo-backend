package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// CustomStatusHandler -- Task Management Core Phase 1; toggle
// require_start_confirmation Phase 4 (US-018b). Selain toggle itu
// baca-saja -- lihat komentar package service.
type CustomStatusHandler struct {
	statuses *service.CustomStatusService
	logger   *zap.Logger
}

func NewCustomStatusHandler(statuses *service.CustomStatusService, logger *zap.Logger) *CustomStatusHandler {
	return &CustomStatusHandler{statuses: statuses, logger: logger}
}

// ListForWorkspace menangani GET /workspaces/:wsId/statuses -- kolom papan
// Kanban (status sistem, di-seed otomatis saat workspace dibuat).
func (h *CustomStatusHandler) ListForWorkspace(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("CustomStatusHandler.ListForWorkspace dipanggil tanpa DBContextMiddleware")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	list, err := h.statuses.ListForWorkspace(c.Context(), exec, workspaceID)
	if err != nil {
		h.logger.Error("gagal mengambil daftar status", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil daftar status", nil))
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = customStatusJSON(&list[i])
	}
	return c.JSON(response.Success(data))
}

type requireStartConfirmationRequest struct {
	RequireStartConfirmation bool `json:"require_start_confirmation"`
}

// UpdateRequireStartConfirmation menangani PUT /statuses/:id (Phase 4,
// US-018b/S4-64) -- hanya PM dan Admin Workspace.
func (h *CustomStatusHandler) UpdateRequireStartConfirmation(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	statusID := c.Params("id")

	var body requireStartConfirmationRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	if err := h.statuses.SetRequireStartConfirmation(c.Context(), exec, statusID, body.RequireStartConfirmation, actorUserID, actorRole); err != nil {
		switch {
		case errors.Is(err, domain.ErrCustomStatusNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Status tidak ditemukan", nil))
		case errors.Is(err, domain.ErrForbidden):
			return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Hanya Admin Workspace dan Project Manager yang bisa mengubah setting ini.", nil))
		default:
			h.logger.Error("gagal mengubah require_start_confirmation", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengubah setting status", nil))
		}
	}
	return c.JSON(response.Success(fiber.Map{"id": statusID, "require_start_confirmation": body.RequireStartConfirmation}))
}

func customStatusJSON(s *repository.CustomStatus) fiber.Map {
	return fiber.Map{
		"id": s.ID, "name": s.Name, "color_token": s.ColorToken, "position": s.Position,
		"is_system": s.IsSystem, "is_undefined": s.IsUndefined, "require_start_confirmation": s.RequireStartConfirmation,
	}
}
