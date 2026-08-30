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

// ProjectHandler -- S4-02/03, US-012.
type ProjectHandler struct {
	projects *service.ProjectService
	logger   *zap.Logger
}

func NewProjectHandler(projects *service.ProjectService, logger *zap.Logger) *ProjectHandler {
	return &ProjectHandler{projects: projects, logger: logger}
}

func projectToMap(p *repository.Project) fiber.Map {
	return fiber.Map{
		"id":           p.ID,
		"workspace_id": p.WorkspaceID,
		"name":         p.Name,
		"code":         p.Code,
		"pm_user_id":   p.PMUserID,
		"pm_name":      p.PMName,
		"pm_email":     p.PMEmail,
		"is_archived":  p.IsArchived,
		"member_count": p.MemberCount,
		"created_at":   p.CreatedAt,
		"archived_at":  p.ArchivedAt,
	}
}

type createProjectRequest struct {
	Name     string `json:"name"`
	Code     string `json:"code"`
	PMUserID string `json:"pm_user_id"`
}

// Create menangani POST /workspaces/:wsId/projects (S4-02) -- digerbangi
// middleware.RequireRole(admin_workspace, project_manager) di routing.
func (h *ProjectHandler) Create(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.Create dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.Create dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	var req createProjectRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}

	p, err := h.projects.Create(c.Context(), exec, workspaceID, req.Name, req.Code, req.PMUserID, actorUserID, actorRole)
	if err != nil {
		return h.mapProjectError(c, err, "Gagal membuat project")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(projectToMap(p)))
}

// List menangani GET /workspaces/:wsId/projects (S4-04) -- digerbangi
// middleware.RequireRole(seluruh role workspace) di routing.
func (h *ProjectHandler) List(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.List dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	list, err := h.projects.List(c.Context(), exec, workspaceID)
	if err != nil {
		return h.mapProjectError(c, err, "Gagal mengambil daftar project")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = projectToMap(&list[i])
	}
	return c.JSON(response.Success(data))
}

type updateProjectRequest struct {
	Name     string `json:"name"`
	PMUserID string `json:"pm_user_id"`
}

// Update menangani PUT /projects/:id (S4-02) -- TIDAK digerbangi
// middleware role (route ini tidak punya :wsId), otorisasi penuh di
// ProjectService.authorize.
func (h *ProjectHandler) Update(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.Update dipanggil tanpa DBContextMiddleware -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.Update dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	var req updateProjectRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}

	if err := h.projects.Update(c.Context(), exec, projectID, req.Name, req.PMUserID, actorUserID, claims.PlatformRole); err != nil {
		return h.mapProjectError(c, err, "Gagal mengubah project")
	}
	return c.JSON(response.Success(fiber.Map{"id": projectID, "name": req.Name}))
}

// Archive menangani PUT /projects/:id/archive (S4-03).
func (h *ProjectHandler) Archive(c *fiber.Ctx) error {
	return h.setArchived(c, true)
}

// Unarchive menangani PUT /projects/:id/unarchive (S4-03, ditambah atas
// permintaan user 2026-08-30 -- simetris pola tier/GA lifecycle: reversible
// toggle butuh jalan keluar, bukan cuma jalan masuk).
func (h *ProjectHandler) Unarchive(c *fiber.Ctx) error {
	return h.setArchived(c, false)
}

func (h *ProjectHandler) setArchived(c *fiber.Ctx, archive bool) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.setArchived dipanggil tanpa DBContextMiddleware -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.setArchived dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	if err := h.projects.SetArchived(c.Context(), exec, projectID, archive, actorUserID, claims.PlatformRole); err != nil {
		fallback := "Gagal mengarsipkan project"
		if !archive {
			fallback = "Gagal membatalkan arsip project"
		}
		return h.mapProjectError(c, err, fallback)
	}
	return c.JSON(response.Success(fiber.Map{"id": projectID, "is_archived": archive}))
}

// Delete menangani DELETE /projects/:id (S4-02) -- soft-delete, lihat
// komentar ProjectRepository.SoftDelete.
func (h *ProjectHandler) Delete(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.Delete dipanggil tanpa DBContextMiddleware -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.Delete dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	if err := h.projects.Delete(c.Context(), exec, projectID, actorUserID, claims.PlatformRole); err != nil {
		return h.mapProjectError(c, err, "Gagal menghapus project")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// Restore menangani POST /projects/:id/restore -- Group Admin/Platform
// Admin saja (ProjectService.authorizeOrgOnly).
func (h *ProjectHandler) Restore(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.Restore dipanggil tanpa DBContextMiddleware -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.Restore dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	if err := h.projects.Restore(c.Context(), exec, projectID, actorUserID, claims.PlatformRole); err != nil {
		return h.mapProjectError(c, err, "Gagal memulihkan project")
	}
	return c.JSON(response.Success(fiber.Map{"id": projectID}))
}

func (h *ProjectHandler) mapProjectError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid -- nama, kode (2-5 huruf), dan Project Manager wajib diisi dengan benar", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas project ini.", nil))
	case errors.Is(err, domain.ErrProjectNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Project tidak ditemukan", nil))
	case errors.Is(err, domain.ErrProjectCodeTaken):
		return c.Status(fiber.StatusConflict).JSON(response.Error("PROJECT_CODE_TAKEN", "Kode task sudah dipakai project lain di workspace ini", nil))
	case errors.Is(err, domain.ErrProjectNotDeleted):
		return c.Status(fiber.StatusConflict).JSON(response.Error("PROJECT_NOT_DELETED", "Project ini tidak sedang dihapus", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
