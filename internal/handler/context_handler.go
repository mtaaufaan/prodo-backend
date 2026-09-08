package handler

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// ContextHandler -- GET/PATCH /me/context (S16-01/02/03, forward-pull Track
// S4G): switcher context dual-role GA.
type ContextHandler struct {
	contexts *service.ContextService
	audit    contextAuditLogger
	logger   *zap.Logger
}

// contextAuditLogger -- interface didefinisikan di consumer, diimplementasikan
// *repository.ContextRepository.
type contextAuditLogger interface {
	LogSwitch(ctx context.Context, exec db.Executor, userID, fromContext, toContext string) error
}

func NewContextHandler(contexts *service.ContextService, audit contextAuditLogger, logger *zap.Logger) *ContextHandler {
	return &ContextHandler{contexts: contexts, audit: audit, logger: logger}
}

func (h *ContextHandler) Get(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	userID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ContextHandler.Get dipanggil tanpa DBContextMiddleware -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ContextHandler.Get dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}

	uc, err := h.contexts.Get(c.Context(), exec, userID, claims.ID, claims.PlatformRole)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memuat context", nil))
	}

	workspaces := make([]fiber.Map, 0, len(uc.Workspaces))
	for _, w := range uc.Workspaces {
		workspaces = append(workspaces, fiber.Map{
			"workspace_id": w.WorkspaceID,
			"name":         w.Name,
			"org_name":     w.OrgName,
			"role":         w.Role,
		})
	}

	return c.JSON(response.Success(fiber.Map{
		"platform_role":         uc.PlatformRole,
		"ga_console_enabled":    uc.GAConsoleEnabled,
		"active_context":        uc.ActiveContext,
		"workspace_memberships": workspaces,
	}))
}

type switchContextRequest struct {
	Context string `json:"context"`
}

func (h *ContextHandler) Switch(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	userID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ContextHandler.Switch dipanggil tanpa DBContextMiddleware -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ContextHandler.Switch dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}

	var req switchContextRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}

	var tokenTTL time.Duration
	if claims.ExpiresAt != nil {
		tokenTTL = time.Until(claims.ExpiresAt.Time)
	}

	if err := h.contexts.Switch(c.Context(), exec, h.audit, userID, claims.ID, claims.PlatformRole, req.Context, tokenTTL); err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "context harus 'ga_console' atau 'workspace'", nil))
		case errors.Is(err, domain.ErrForbidden):
			return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda bukan Group Admin -- tidak bisa pindah ke Konsol Group Admin", nil))
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal berpindah context", nil))
		}
	}

	return c.JSON(response.Success(fiber.Map{"active_context": req.Context}))
}
