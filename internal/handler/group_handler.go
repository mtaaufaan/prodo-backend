package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// GroupHandler -- S3-20, US-009b.
type GroupHandler struct {
	groups *service.GroupService
	logger *zap.Logger
}

func NewGroupHandler(groups *service.GroupService, logger *zap.Logger) *GroupHandler {
	return &GroupHandler{groups: groups, logger: logger}
}

// SearchAccounts menangani GET /groups/:groupId/accounts/search (S3-20).
// TIDAK digerbangi RequirePlatformRole/RequireRole di routing -- target
// scope-nya groupID, bukan :wsId/:orgId yang sudah punya middleware khusus,
// dan aktor sahnya (Project Manager) platform_role-nya "member" biasa,
// jadi otorisasi PENUH ada di GroupService.SearchAccounts. actorUserID
// diresolve middleware.DBContextMiddleware (dbCtx) yang sudah jalan lebih
// dulu; role diambil dari klaim JWT langsung (bukan locals actorRoleLocalsKey
// yang cuma diisi RequireRole/RequirePlatformRole).
func (h *GroupHandler) SearchAccounts(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("GroupHandler.SearchAccounts dipanggil tanpa DBContextMiddleware -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("GroupHandler.SearchAccounts dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")
	query := c.Query("q")

	accounts, err := h.groups.SearchAccounts(c.Context(), exec, groupID, query, actorUserID, claims.PlatformRole)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
		case errors.Is(err, domain.ErrForbidden):
			return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang mencari user di grup ini.", nil))
		default:
			h.logger.Error("gagal mencari akun dalam grup", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mencari akun", nil))
		}
	}

	data := make([]fiber.Map, len(accounts))
	for i := range accounts {
		a := &accounts[i]
		data[i] = fiber.Map{
			"user_id":      a.UserID,
			"email":        a.Email,
			"display_name": a.DisplayName,
			"org_id":       a.OrgID,
			"org_name":     a.OrgName,
		}
	}
	return c.JSON(response.Success(data))
}
