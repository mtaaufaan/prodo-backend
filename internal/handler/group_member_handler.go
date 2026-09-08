package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/validator"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// groupNamer -- interface didefinisikan di consumer, diimplementasikan
// *GroupRepository (nama grup untuk isi subjek email undangan Eksekutif).
type groupNamer interface {
	GetName(ctx context.Context, exec db.Executor, groupID string) (string, error)
}

// GroupMemberHandler -- Members & Roles (forward-pull US-086, Track S4G).
type GroupMemberHandler struct {
	members  *service.GroupMemberService
	groups   groupNamer
	accounts displayNameGetter
	logger   *zap.Logger
}

func NewGroupMemberHandler(members *service.GroupMemberService, groups groupNamer, accounts displayNameGetter, logger *zap.Logger) *GroupMemberHandler {
	return &GroupMemberHandler{members: members, groups: groups, accounts: accounts, logger: logger}
}

func (h *GroupMemberHandler) mapError(c *fiber.Ctx, err error, fallback string) error {
	switch {
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas grup ini.", nil))
	case errors.Is(err, domain.ErrUserNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Member tidak ditemukan di grup ini", nil))
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrInvitationAlreadyPending):
		return c.Status(fiber.StatusConflict).JSON(response.Error("INVITATION_ALREADY_PENDING", "Sudah ada undangan Eksekutif pending untuk email ini", nil))
	default:
		h.logger.Error(fallback, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallback, nil))
	}
}

func (h *GroupMemberHandler) List(c *fiber.Ctx) error {
	actorID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("GroupMemberHandler.List dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("GroupMemberHandler.List dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}

	dir, err := h.members.ListDirectory(c.Context(), exec, c.Params("groupId"), actorID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil direktori member")
	}

	members := make([]fiber.Map, 0, len(dir.Members))
	for _, m := range dir.Members {
		roles := make([]fiber.Map, 0, len(m.WorkspaceRoles))
		for _, r := range m.WorkspaceRoles {
			roles = append(roles, fiber.Map{
				"workspace_id":   r.WorkspaceID,
				"workspace_name": r.WorkspaceName,
				"org_name":       r.OrgName,
				"role":           r.Role,
			})
		}
		members = append(members, fiber.Map{
			"user_id":         m.UserID,
			"email":           m.Email,
			"display_name":    m.DisplayName,
			"is_active":       m.IsActive,
			"suspended":       m.Suspended,
			"is_group_admin":  m.IsGroupAdmin,
			"is_executive":    m.IsExecutive,
			"executive_title": m.ExecutiveTitle,
			"workspace_roles": roles,
		})
	}

	pending := make([]fiber.Map, 0, len(dir.Pending))
	for i := range dir.Pending {
		p := &dir.Pending[i]
		pending = append(pending, fiber.Map{
			"id":             p.ID,
			"email":          p.Email,
			"role":           p.Role,
			"workspace_id":   p.WorkspaceID,
			"workspace_name": p.WorkspaceName,
			"org_name":       p.OrgName,
			"is_executive":   p.IsExecutive,
			"created_at":     p.CreatedAt,
			"expires_at":     p.ExpiresAt,
		})
	}

	return c.JSON(response.Success(fiber.Map{"members": members, "pending": pending}))
}

type toggleExecutiveRequest struct {
	Assign bool `json:"assign"`
}

func (h *GroupMemberHandler) ToggleExecutive(c *fiber.Ctx) error {
	actorID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("GroupMemberHandler.ToggleExecutive dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("GroupMemberHandler.ToggleExecutive dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}

	var req toggleExecutiveRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}

	groupID, userID := c.Params("groupId"), c.Params("userId")
	var err error
	if req.Assign {
		err = h.members.AssignExecutive(c.Context(), exec, userID, groupID, actorID, actorRole)
	} else {
		err = h.members.RevokeExecutive(c.Context(), exec, userID, groupID, actorID, actorRole)
	}
	if err != nil {
		return h.mapError(c, err, "Gagal mengubah status Eksekutif")
	}
	return c.JSON(response.Success(fiber.Map{"user_id": userID, "is_executive": req.Assign}))
}

type updateIdentityRequest struct {
	DisplayName string `json:"display_name"`
	Title       string `json:"title"`
}

func (h *GroupMemberHandler) UpdateIdentity(c *fiber.Ctx) error {
	actorID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("GroupMemberHandler.UpdateIdentity dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("GroupMemberHandler.UpdateIdentity dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}

	var req updateIdentityRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.Title = strings.TrimSpace(req.Title)

	groupID, userID := c.Params("groupId"), c.Params("userId")
	if err := h.members.UpdateIdentity(c.Context(), exec, userID, groupID, actorID, actorRole, req.DisplayName, req.Title); err != nil {
		return h.mapError(c, err, "Gagal mengubah identitas")
	}
	return c.JSON(response.Success(fiber.Map{"user_id": userID, "display_name": req.DisplayName, "title": req.Title}))
}

// DeactivateAccess dan ReactivateAccess dipisah jadi 2 endpoint (bukan satu
// PUT .../access dengan body {active}) supaya RequireStepUp bisa dipasang
// HANYA di jalur nonaktifkan lewat routing biasa -- middleware tidak perlu
// mengintip body request untuk memutuskan apakah step-up berlaku (desain
// "GA Members Roles.dc.html": nonaktifkan butuh step-up, aktifkan tidak,
// pola sama Organizations Deactivate).

func (h *GroupMemberHandler) DeactivateAccess(c *fiber.Ctx) error {
	return h.setAccess(c, false, "Gagal menonaktifkan akses akun")
}

func (h *GroupMemberHandler) ReactivateAccess(c *fiber.Ctx) error {
	return h.setAccess(c, true, "Gagal mengaktifkan akses akun")
}

func (h *GroupMemberHandler) setAccess(c *fiber.Ctx, active bool, fallback string) error {
	actorID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("GroupMemberHandler.setAccess dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("GroupMemberHandler.setAccess dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}

	groupID, userID := c.Params("groupId"), c.Params("userId")
	if err := h.members.SetAccess(c.Context(), exec, userID, groupID, actorID, actorRole, active); err != nil {
		return h.mapError(c, err, fallback)
	}
	return c.JSON(response.Success(fiber.Map{"user_id": userID, "active": active}))
}

type inviteExecutiveRequest struct {
	Email string `json:"email"`
}

// InviteExecutive menangani POST /groups/:groupId/executive-invitations --
// undangan Eksekutif murni (email baru, tanpa workspace). groupName/
// inviterName disuplai dari resolve di sini (pola sama InvitationHandler.
// Create), bukan dilempar ke FE untuk diisi manual.
func (h *GroupMemberHandler) InviteExecutive(c *fiber.Ctx) error {
	actorID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("GroupMemberHandler.InviteExecutive dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("GroupMemberHandler.InviteExecutive dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	inviterName, err := h.accounts.GetDisplayName(c.Context(), actorID)
	if err != nil {
		h.logger.Error("GroupMemberHandler.InviteExecutive gagal resolve nama pengundang", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}

	var req inviteExecutiveRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.Email = strings.TrimSpace(req.Email)
	if !validator.IsValidEmail(req.Email) {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Format email tidak valid", nil))
	}

	groupID := c.Params("groupId")
	groupName, err := h.groups.GetName(c.Context(), exec, groupID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Grup tidak ditemukan", nil))
	}
	inv, err := h.members.InviteExecutive(c.Context(), exec, groupID, actorID, actorRole, req.Email, groupName, inviterName)
	if err != nil {
		return h.mapError(c, err, "Gagal membuat undangan Eksekutif")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{"id": inv.ID, "email": inv.Email, "expires_at": inv.ExpiresAt}))
}
