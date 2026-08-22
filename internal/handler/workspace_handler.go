package handler

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

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

// WorkspaceHandler -- S2-04/07, US-002.
type WorkspaceHandler struct {
	rbac   *service.RBACService
	logger *zap.Logger
}

func NewWorkspaceHandler(rbac *service.RBACService, logger *zap.Logger) *WorkspaceHandler {
	return &WorkspaceHandler{rbac: rbac, logger: logger}
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
