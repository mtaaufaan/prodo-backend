// Package handler -- GroupLocaleHandler (Bahasa & Format Regional lanjutan,
// Track S4G S4G-27, desain "GA Bahasa Lokal.dc.html").
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

type GroupLocaleHandler struct {
	locale *service.GroupLocaleService
	logger *zap.Logger
}

func NewGroupLocaleHandler(locale *service.GroupLocaleService, logger *zap.Logger) *GroupLocaleHandler {
	return &GroupLocaleHandler{locale: locale, logger: logger}
}

func localeToMap(groupID string, l *repository.GroupLocale) fiber.Map {
	return fiber.Map{
		"group_id":      groupID,
		"date_format":   l.DateFormat,
		"time_format":   l.TimeFormat,
		"timezone":      l.Timezone,
		"number_format": l.NumberFormat,
	}
}

// Get menangani GET /groups/:groupId/locale.
func (h *GroupLocaleHandler) Get(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("GroupLocaleHandler.Get dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("GroupLocaleHandler.Get dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")

	locale, err := h.locale.Get(c.Context(), exec, groupID, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil format lokal grup")
	}
	return c.JSON(response.Success(localeToMap(groupID, locale)))
}

type updateGroupLocaleRequest struct {
	DateFormat   string `json:"date_format"`
	TimeFormat   string `json:"time_format"`
	Timezone     string `json:"timezone"`
	NumberFormat string `json:"number_format"`
}

// Update menangani PUT /groups/:groupId/locale.
func (h *GroupLocaleHandler) Update(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("GroupLocaleHandler.Update dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("GroupLocaleHandler.Update dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")

	var req updateGroupLocaleRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_BODY", "Body request tidak valid", nil))
	}

	locale := repository.GroupLocale{DateFormat: req.DateFormat, TimeFormat: req.TimeFormat, Timezone: req.Timezone, NumberFormat: req.NumberFormat}
	if err := h.locale.Update(c.Context(), exec, groupID, locale, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal menyimpan format lokal grup")
	}
	return c.JSON(response.Success(localeToMap(groupID, &locale)))
}

func (h *GroupLocaleHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas grup ini.", nil))
	case errors.Is(err, domain.ErrGroupNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Grup tidak ditemukan", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
