package handler

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// CustomStatusHandler -- Task Management Core Phase 1. Baca-saja -- lihat
// komentar package service.
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

func customStatusJSON(s *repository.CustomStatus) fiber.Map {
	return fiber.Map{
		"id": s.ID, "name": s.Name, "color_token": s.ColorToken, "position": s.Position,
		"is_system": s.IsSystem, "is_undefined": s.IsUndefined,
	}
}
