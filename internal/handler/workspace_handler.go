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

// WorkspaceHandler -- S2-04, US-002.
type WorkspaceHandler struct {
	accounts *service.AccountService
	rbac     *service.RBACService
	logger   *zap.Logger
}

func NewWorkspaceHandler(accounts *service.AccountService, rbac *service.RBACService, logger *zap.Logger) *WorkspaceHandler {
	return &WorkspaceHandler{accounts: accounts, rbac: rbac, logger: logger}
}

type updateMemberRoleRequest struct {
	Role string `json:"role"`
}

// UpdateMemberRole menangani PUT /workspaces/:wsId/members/:userId/role
// (S2-04). ⚠️ Otorisasi "GA atau AW only" per sprint_backlog.md cuma
// SEBAGIAN diimplementasikan: Platform Admin (bypass penuh) dan Admin
// Workspace (role admin_workspace DI WORKSPACE INI) sudah bekerja. Group
// Admin BELUM bisa -- mengecek "apakah actor GA yang mengelola organisasi
// pemilik workspace ini" butuh group_admin_assignments + traversal
// organizations/groups yang belum ada (implementation_gaps.md IG-01, gap
// yang sama dengan S1-30/S1-35). Middleware RequireRole() generik (S2-09)
// juga belum dibangun -- otorisasi di sini masih inline, menyusul
// di-refactor begitu itu ada.
func (h *WorkspaceHandler) UpdateMemberRole(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}

	workspaceID := c.Params("wsId")
	targetUserID := c.Params("userId")

	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("JWT valid tapi user tidak ditemukan di tabel users", zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}

	actorRole := claims.PlatformRole
	if actorRole != "platform_admin" {
		workspaceRole, err := h.rbac.GetMemberRole(c.Context(), workspaceID, actorUserID)
		if err != nil {
			h.logger.Error("gagal cek role actor di workspace", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memproses permintaan", nil))
		}
		if workspaceRole != "admin_workspace" {
			return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Hanya Admin Workspace atau Platform Admin yang dapat mengubah role member.", nil))
		}
		actorRole = workspaceRole
	}

	var req updateMemberRoleRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	if !validWorkspaceRoles[req.Role] {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "role tidak valid",
			[]response.FieldError{{Field: "role", Message: "harus salah satu dari admin_workspace, project_manager, editor, approver, viewer"}}))
	}

	result, err := h.rbac.AssignRole(c.Context(), workspaceID, targetUserID, req.Role, nil, actorUserID, actorRole)
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
