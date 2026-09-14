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
	accounts displayNameGetter
	logger   *zap.Logger
}

func NewProjectHandler(projects *service.ProjectService, accounts displayNameGetter, logger *zap.Logger) *ProjectHandler {
	return &ProjectHandler{projects: projects, accounts: accounts, logger: logger}
}

func projectToMap(p *repository.Project) fiber.Map {
	return fiber.Map{
		"id":                       p.ID,
		"workspace_id":             p.WorkspaceID,
		"name":                     p.Name,
		"code":                     p.Code,
		"pm_user_id":               p.PMUserID,
		"pm_name":                  p.PMName,
		"pm_email":                 p.PMEmail,
		"is_archived":              p.IsArchived,
		"member_count":             p.MemberCount,
		"sprint_count":             p.SprintCount,
		"task_count":               p.TaskCount,
		"created_by_name":          p.CreatedByName,
		"created_by_email":         p.CreatedByEmail,
		"pm_pending_email":         p.PMPendingEmail,
		"pm_pending_invitation_id": p.PMPendingInvitationID,
		"created_at":               p.CreatedAt,
		"archived_at":              p.ArchivedAt,
	}
}

type createProjectRequest struct {
	Name     string `json:"name"`
	Code     string `json:"code"`
	PMUserID string `json:"pm_user_id"`
	PMEmail  string `json:"pm_email"`
	PMName   string `json:"pm_name"`
}

// Create menangani POST /workspaces/:wsId/projects (S4-02, diperluas S4W
// susulan) -- digerbangi middleware.RequireRole(admin_workspace,
// project_manager) di routing. PM ditunjuk lewat PERSIS SATU dari
// pm_user_id (member existing workspace ini) atau pm_email (+pm_name kalau
// belum terdaftar) -- mutual exclusion ditegakkan di sini, sama pola
// WorkspaceHandler.CreateWorkspace.
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
	if req.PMUserID != "" && req.PMEmail != "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "pm_user_id dan pm_email tidak boleh diisi bersamaan", nil))
	}

	inviterName, err := h.accounts.GetDisplayName(c.Context(), actorUserID)
	if err != nil {
		h.logger.Error("gagal ambil nama actor untuk isi email undangan PM", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal membuat project", nil))
	}

	p, err := h.projects.Create(c.Context(), exec, workspaceID, req.Name, req.Code, req.PMUserID, req.PMEmail, req.PMName, actorUserID, actorRole, inviterName)
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
	Name string `json:"name"`
}

// Update menangani PUT /projects/:id (S4-02) -- ubah NAMA saja sejak S4W
// susulan (PM dipindah ke AssignPM/RemovePM, seksi terpisah panel Kelola).
// TIDAK digerbangi middleware role (route ini tidak punya :wsId),
// otorisasi penuh di ProjectService.authorize.
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

	if err := h.projects.Update(c.Context(), exec, projectID, req.Name, actorUserID, claims.PlatformRole); err != nil {
		return h.mapProjectError(c, err, "Gagal mengubah project")
	}
	return c.JSON(response.Success(fiber.Map{"id": projectID, "name": req.Name}))
}

type assignProjectPMRequest struct {
	PMUserID string `json:"pm_user_id"`
	PMEmail  string `json:"pm_email"`
	PMName   string `json:"pm_name"`
}

// AssignPM menangani POST /projects/:id/pm (S4W susulan) -- tetapkan/ganti
// PM, sama pola Create (existing member ATAU invite email baru). Dipakai
// panel Kelola baik untuk mengisi project yang masih "menunggu PM" maupun
// mengganti PM aktif.
func (h *ProjectHandler) AssignPM(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.AssignPM dipanggil tanpa DBContextMiddleware -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.AssignPM dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	var req assignProjectPMRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	if req.PMUserID != "" && req.PMEmail != "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "pm_user_id dan pm_email tidak boleh diisi bersamaan", nil))
	}

	inviterName, err := h.accounts.GetDisplayName(c.Context(), actorUserID)
	if err != nil {
		h.logger.Error("gagal ambil nama actor untuk isi email undangan PM", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menetapkan PM", nil))
	}

	if err := h.projects.AssignPM(c.Context(), exec, projectID, req.PMUserID, req.PMEmail, req.PMName, actorUserID, claims.PlatformRole, inviterName); err != nil {
		return h.mapProjectError(c, err, "Gagal menetapkan PM")
	}
	return c.JSON(response.Success(fiber.Map{"id": projectID}))
}

// RemovePM menangani DELETE /projects/:id/pm (S4W susulan) -- kosongkan PM
// aktif tanpa pengganti, project masuk status "menunggu PM".
func (h *ProjectHandler) RemovePM(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.RemovePM dipanggil tanpa DBContextMiddleware -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectHandler.RemovePM dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	if err := h.projects.RemovePM(c.Context(), exec, projectID, actorUserID, claims.PlatformRole); err != nil {
		return h.mapProjectError(c, err, "Gagal menghapus PM")
	}
	return c.JSON(response.Success(fiber.Map{"id": projectID}))
}

type updateProjectSettingsRequest struct {
	AllowEditorStoryPoints bool `json:"allow_editor_story_points"`
}

// UpdateSettings menangani PUT /projects/:id/settings (Task Management
// Core Phase 4, US-018a/S4-56) -- gate sama seperti Update (PM/AW/org-access).
func (h *ProjectHandler) UpdateSettings(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	var req updateProjectSettingsRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	if err := h.projects.SetAllowEditorStoryPoints(c.Context(), exec, projectID, req.AllowEditorStoryPoints, actorUserID, actorRole); err != nil {
		return h.mapProjectError(c, err, "Gagal mengubah setting project")
	}
	return c.JSON(response.Success(fiber.Map{"id": projectID, "allow_editor_story_points": req.AllowEditorStoryPoints}))
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
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid -- nama, kode (2-5 huruf), dan Project Manager (member existing atau email+nama untuk undangan baru) wajib diisi dengan benar", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas project ini.", nil))
	case errors.Is(err, domain.ErrProjectNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Project tidak ditemukan", nil))
	case errors.Is(err, domain.ErrProjectCodeTaken):
		return c.Status(fiber.StatusConflict).JSON(response.Error("PROJECT_CODE_TAKEN", "Kode task sudah dipakai project lain di workspace ini", nil))
	case errors.Is(err, domain.ErrProjectNameTaken):
		return c.Status(fiber.StatusConflict).JSON(response.Error("PROJECT_NAME_TAKEN", "Nama project sudah dipakai di workspace ini", nil))
	case errors.Is(err, domain.ErrProjectNotDeleted):
		return c.Status(fiber.StatusConflict).JSON(response.Error("PROJECT_NOT_DELETED", "Project ini tidak sedang dihapus", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
