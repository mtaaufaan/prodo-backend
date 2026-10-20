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
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// ContextHandler -- GET/PATCH /me/context (S16-01/02/03, forward-pull Track
// S4G): switcher context dual-role GA.
type ContextHandler struct {
	contexts *service.ContextService
	audit    contextAuditLogger
	followed followedProjectLister
	logger   *zap.Logger
}

// followedProjectLister -- interface didefinisikan di consumer, diimplementasikan
// *repository.FollowedProjectAccessRepository (S6E-02).
type followedProjectLister interface {
	ListFollowedProjects(ctx context.Context, exec db.Executor, workspaceID, userID string) ([]repository.FollowedProject, error)
}

// contextAuditLogger -- interface didefinisikan di consumer, diimplementasikan
// *repository.ContextRepository.
type contextAuditLogger interface {
	LogSwitch(ctx context.Context, exec db.Executor, userID, fromContext, toContext string) error
}

func NewContextHandler(contexts *service.ContextService, audit contextAuditLogger, followed followedProjectLister, logger *zap.Logger) *ContextHandler {
	return &ContextHandler{contexts: contexts, audit: audit, followed: followed, logger: logger}
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
		joined := w.JoinedAt.UTC()
		workspaces = append(workspaces, fiber.Map{
			"workspace_id": w.WorkspaceID,
			"name":         w.Name,
			"org_name":     w.OrgName,
			"role":         w.Role,
			"capabilities": service.CapabilitiesForRole(w.Role),
			"joined_at":    dateOnly(&joined),
		})
	}

	// project_scoped_projects (susulan, lihat komentar repository.
	// ProjectScopedMembership) -- SENGAJA daftar terpisah dari
	// workspace_memberships, tidak menyalakan switcher multi-workspace.
	projectScoped := make([]fiber.Map, 0, len(uc.ProjectScopedProjects))
	for _, p := range uc.ProjectScopedProjects {
		projectScoped = append(projectScoped, fiber.Map{
			"project_id":     p.ProjectID,
			"project_name":   p.ProjectName,
			"workspace_id":   p.WorkspaceID,
			"workspace_name": p.WorkspaceName,
			"org_name":       p.OrgName,
			"role":           p.Role,
			"capabilities":   service.CapabilitiesForRole(p.Role),
		})
	}

	return c.JSON(response.Success(fiber.Map{
		"platform_role":           uc.PlatformRole,
		"ga_console_enabled":      uc.GAConsoleEnabled,
		"active_context":          uc.ActiveContext,
		"workspace_memberships":   workspaces,
		"project_scoped_projects": projectScoped,
	}))
}

// Projects menangani GET /me/projects?workspace_id= (S6E-02, IG-133): project non-arsip di
// workspace itu yang diikuti user (anggota project, assignee, atau PIC aktif). Data milik
// user sendiri, jadi tanpa gerbang role: non-anggota mendapat daftar kosong.
func (h *ContextHandler) Projects(c *fiber.Ctx) error {
	workspaceID := c.Query("workspace_id")
	if workspaceID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "workspace_id wajib diisi", nil))
	}
	userID, _, ok := middleware.ActorFromContext(c)
	exec, txOK := middleware.DBTxFromContext(c)
	if !ok || !txOK {
		h.logger.Error("ContextHandler.Projects dipanggil tanpa DBContextMiddleware")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	list, err := h.followed.ListFollowedProjects(c.Context(), exec, workspaceID, userID)
	if err != nil {
		h.logger.Error("ContextHandler.Projects gagal", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memuat project", nil))
	}
	data := make([]fiber.Map, 0, len(list))
	for _, p := range list {
		data = append(data, fiber.Map{"id": p.ID, "workspace_id": p.WorkspaceID, "name": p.Name, "code": p.Code})
	}
	return c.JSON(response.Success(data))
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
