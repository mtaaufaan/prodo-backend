package handler

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// validWorkspaceRoles -- 5 nilai enum workspace_role (DATABASE_SCHEMA.md §5.10).
var validWorkspaceRoles = map[string]bool{
	"admin_workspace": true,
	"project_manager": true,
	"editor":          true,
	"approver":        true,
	"viewer":          true,
}

// WorkspaceHandler -- S2-04/07/S3-09, US-002/US-008.
type WorkspaceHandler struct {
	rbac       *service.RBACService
	workspaces *service.WorkspaceService
	logger     *zap.Logger
}

func NewWorkspaceHandler(rbac *service.RBACService, workspaces *service.WorkspaceService, logger *zap.Logger) *WorkspaceHandler {
	return &WorkspaceHandler{rbac: rbac, workspaces: workspaces, logger: logger}
}

type createWorkspaceRequest struct {
	Name                 string `json:"name"`
	AdminWorkspaceUserID string `json:"admin_workspace_user_id"`
}

// CreateWorkspace menangani POST /organizations/:orgId/workspaces (S3-09).
func (h *WorkspaceHandler) CreateWorkspace(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceHandler.CreateWorkspace dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceHandler.CreateWorkspace dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	orgID := c.Params("orgId")

	var req createWorkspaceRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || req.AdminWorkspaceUserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "name dan admin_workspace_user_id wajib diisi", nil))
	}

	ws, err := h.workspaces.CreateWorkspace(c.Context(), exec, orgID, req.Name, req.AdminWorkspaceUserID, actorUserID, actorRole)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
		case errors.Is(err, domain.ErrForbidden):
			return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas organisasi ini.", nil))
		case errors.Is(err, domain.ErrOrganizationNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Organisasi tidak ditemukan", nil))
		default:
			h.logger.Error("gagal membuat workspace", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal membuat workspace", nil))
		}
	}

	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{
		"id":     ws.ID,
		"org_id": ws.OrgID,
		"name":   ws.Name,
	}))
}

type updateMemberRoleRequest struct {
	Role string `json:"role"`
}

// UpdateMemberRole menangani PUT /workspaces/:wsId/members/:userId/role
// (S2-04). Otorisasi ("Platform Admin atau Admin Workspace di workspace
// ini" -- Group Admin belum bisa, implementation_gaps.md IG-01) sudah
// ditegakkan middleware.RequireRole (S2-09) di routing, jadi handler ini
// murni orkestrasi: validasi input + panggil RBACService.AssignRole.
// actorUserID/actorRole diambil dari middleware.ActorFromContext (sudah
// diresolve RequireRole, tidak perlu query ulang).
func (h *WorkspaceHandler) UpdateMemberRole(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("UpdateMemberRole dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("UpdateMemberRole dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}

	workspaceID := c.Params("wsId")
	targetUserID := c.Params("userId")

	var req updateMemberRoleRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	if !validWorkspaceRoles[req.Role] {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "role tidak valid",
			[]response.FieldError{{Field: "role", Message: "harus salah satu dari admin_workspace, project_manager, editor, approver, viewer"}}))
	}

	result, err := h.rbac.AssignRole(c.Context(), exec, workspaceID, targetUserID, req.Role, nil, actorUserID, actorRole)
	if err != nil {
		h.logger.Error("gagal assign role", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengubah role", nil))
	}

	return c.JSON(response.Success(fiber.Map{
		"workspace_id":  workspaceID,
		"user_id":       targetUserID,
		"previous_role": result.PreviousRole,
		"role":          result.NewRole,
	}))
}

type updateWorkspaceRequest struct {
	Name string `json:"name"`
}

// Update menangani PUT /workspaces/:wsId (S3-10). Otorisasi (Admin Workspace
// di workspace ini, atau PA/GA pengelola org) sudah ditegakkan
// middleware.RequireRole di routing -- actorUserID/actorRole diambil dari
// middleware.ActorFromContext, sama pola UpdateMemberRole.
func (h *WorkspaceHandler) Update(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceHandler.Update dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceHandler.Update dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	var req updateWorkspaceRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "name wajib diisi", nil))
	}

	if err := h.workspaces.UpdateWorkspace(c.Context(), exec, workspaceID, req.Name, actorUserID, actorRole); err != nil {
		return h.mapWorkspaceError(c, err, "Gagal mengubah workspace")
	}

	return c.JSON(response.Success(fiber.Map{"id": workspaceID, "name": req.Name}))
}

// Deactivate menangani PUT /workspaces/:wsId/deactivate (S3-11).
func (h *WorkspaceHandler) Deactivate(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceHandler.Deactivate dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceHandler.Deactivate dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	if err := h.workspaces.DeactivateWorkspace(c.Context(), exec, workspaceID, actorUserID, actorRole); err != nil {
		return h.mapWorkspaceError(c, err, "Gagal menonaktifkan workspace")
	}

	return c.JSON(response.Success(fiber.Map{"id": workspaceID, "deactivated": true}))
}

// Reactivate menangani PUT /workspaces/:wsId/reactivate (S3-11, kebalikan Deactivate).
func (h *WorkspaceHandler) Reactivate(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceHandler.Reactivate dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceHandler.Reactivate dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	if err := h.workspaces.ReactivateWorkspace(c.Context(), exec, workspaceID, actorUserID, actorRole); err != nil {
		return h.mapWorkspaceError(c, err, "Gagal mengaktifkan kembali workspace")
	}

	return c.JSON(response.Success(fiber.Map{"id": workspaceID, "deactivated": false}))
}

// Delete menangani DELETE /workspaces/:wsId (S3-12). Otorisasi HANYA
// Platform Admin/Group Admin pemilik org (bukan Admin Workspace) -- lihat
// catatan WorkspaceService.DeleteWorkspace. Guard "semua project dihapus"
// dari wording task asli BELUM diimplementasikan -- tabel `projects` belum
// ada (implementation_gaps.md IG-17).
func (h *WorkspaceHandler) Delete(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceHandler.Delete dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceHandler.Delete dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	if err := h.workspaces.DeleteWorkspace(c.Context(), exec, workspaceID, actorUserID, actorRole); err != nil {
		return h.mapWorkspaceError(c, err, "Gagal menghapus workspace")
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// List menangani GET /organizations/:orgId/workspaces (S3-13 prasyarat,
// implementation_gaps.md IG-17). Scoping sepenuhnya lewat RLS
// (WorkspaceService.ListWorkspaces).
func (h *WorkspaceHandler) List(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceHandler.List dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	orgID := c.Params("orgId")

	list, err := h.workspaces.ListWorkspaces(c.Context(), exec, orgID)
	if err != nil {
		return h.mapWorkspaceError(c, err, "Gagal mengambil daftar workspace")
	}

	data := make([]fiber.Map, len(list))
	for i := range list {
		w := &list[i]
		data[i] = fiber.Map{
			"id":          w.ID,
			"org_id":      w.OrgID,
			"name":        w.Name,
			"archived_at": w.ArchivedAt,
			"created_at":  w.CreatedAt,
		}
	}
	return c.JSON(response.Success(data))
}

func (h *WorkspaceHandler) mapWorkspaceError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas workspace/organisasi ini.", nil))
	case errors.Is(err, domain.ErrWorkspaceNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Workspace tidak ditemukan", nil))
	case errors.Is(err, domain.ErrOrganizationNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Organisasi tidak ditemukan", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}

// ListMembers menangani GET /workspaces/:wsId/members (S2-07/08 prasyarat,
// dimajukan dari S3-14 -- lihat implementation_gaps.md IG-09). Cuma
// mengembalikan `workspace_members`; array `project_scoped_members` yang
// diminta S3-14 asli menyusul S3 (konsepnya butuh tabel yang belum ada).
func (h *WorkspaceHandler) ListMembers(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ListMembers dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	members, err := h.rbac.ListMembers(c.Context(), exec, workspaceID)
	if err != nil {
		h.logger.Error("gagal ambil daftar member", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil daftar member", nil))
	}

	data := make([]fiber.Map, len(members))
	for i := range members {
		m := &members[i]
		data[i] = fiber.Map{
			"user_id":      m.UserID,
			"email":        m.Email,
			"display_name": m.DisplayName,
			"role":         m.Role,
			"joined_at":    m.JoinedAt,
		}
	}

	return c.JSON(response.Success(fiber.Map{"workspace_members": data}))
}
