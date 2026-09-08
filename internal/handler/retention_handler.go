package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// RetentionHandler -- Data Retention (Track S4G, desain "GA Data
// Retention.dc.html"). DownloadExport SENGAJA route publik (token-based,
// tanpa jwtAuth/dbCtx) -- perlu pool sendiri (pola sama InvitationHandler.
// AcceptInvitation) untuk buka transaksi RLS-bypass platform_admin.
type RetentionHandler struct {
	retention *service.RetentionService
	pool      *pgxpool.Pool
	logger    *zap.Logger
}

func NewRetentionHandler(retention *service.RetentionService, pool *pgxpool.Pool, logger *zap.Logger) *RetentionHandler {
	return &RetentionHandler{retention: retention, pool: pool, logger: logger}
}

// GetSchedule menangani GET /groups/:groupId/retention-schedule.
func (h *RetentionHandler) GetSchedule(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("RetentionHandler.GetSchedule dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("RetentionHandler.GetSchedule dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")

	items, err := h.retention.GetSchedule(c.Context(), exec, groupID, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil jadwal penghapusan")
	}

	data := make([]fiber.Map, len(items))
	for i := range items {
		it := &items[i]
		data[i] = fiber.Map{
			"kind":       it.Kind,
			"item_id":    it.ItemID,
			"item_name":  it.ItemName,
			"org_name":   it.OrgName,
			"event_at":   it.EventAt,
			"total_days": it.TotalDays,
			"purge_at":   it.PurgeAt,
			"days_left":  it.DaysLeft,
		}
	}
	return c.JSON(response.Success(data))
}

type requestExportRequest struct {
	Kind   string `json:"kind"`
	ItemID string `json:"item_id"`
}

// RequestExport menangani POST /groups/:groupId/retention-exports.
func (h *RetentionHandler) RequestExport(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("RetentionHandler.RequestExport dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("RetentionHandler.RequestExport dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")

	var req requestExportRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}

	if err := h.retention.RequestExport(c.Context(), exec, groupID, req.Kind, req.ItemID, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal membuat ekspor")
	}
	return c.JSON(response.Success(fiber.Map{"sent": true}))
}

// DownloadExport menangani GET /retention-exports/:token -- rute PUBLIK
// (tanpa jwtAuth/dbCtx), otorisasi sepenuhnya lewat kepemilikan token dari
// tautan email (pola sama InvitationHandler.AcceptInvitation).
func (h *RetentionHandler) DownloadExport(c *fiber.Ctx) error {
	token := c.Params("token")

	tx, err := db.SetRLSContext(c.Context(), h.pool, "", "platform_admin")
	if err != nil {
		h.logger.Error("gagal menyiapkan transaksi retention export download", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	defer tx.Rollback(c.Context()) //nolint:errcheck // read-only, rollback best-effort

	export, err := h.retention.DownloadExport(c.Context(), tx, token)
	if err != nil {
		if errors.Is(err, domain.ErrRetentionExportNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Tautan unduhan tidak valid atau sudah kedaluwarsa.", nil))
		}
		h.logger.Error("Gagal mengambil ekspor retensi", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil ekspor", nil))
	}

	c.Set("Content-Type", "application/json")
	return c.Send(export.Payload)
}

func (h *RetentionHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas grup ini.", nil))
	case errors.Is(err, domain.ErrRetentionExportNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Item tidak ditemukan dalam jadwal penghapusan grup ini", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
