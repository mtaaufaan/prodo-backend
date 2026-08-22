package handler

import (
	"context"
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/validator"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// displayNameGetter -- interface didefinisikan di consumer (§3.9),
// diimplementasikan *service.AccountService.
type displayNameGetter interface {
	GetDisplayName(ctx context.Context, userID string) (string, error)
}

// InvitationHandler -- S2-19/20/21/22, US-006.
type InvitationHandler struct {
	invitations *service.InvitationService
	accounts    displayNameGetter
	pool        *pgxpool.Pool
	logger      *zap.Logger
}

func NewInvitationHandler(invitations *service.InvitationService, accounts displayNameGetter, pool *pgxpool.Pool, logger *zap.Logger) *InvitationHandler {
	return &InvitationHandler{invitations: invitations, accounts: accounts, pool: pool, logger: logger}
}

// validInvitationRoles -- sama dengan validWorkspaceRoles (workspace_handler.go),
// disalin di sini supaya invitation_handler.go tidak bergantung ke
// workspace_handler.go untuk satu daftar konstanta.
var validInvitationRoles = map[string]bool{
	"admin_workspace": true,
	"project_manager": true,
	"editor":          true,
	"approver":        true,
	"viewer":          true,
}

type createInvitationsRequest struct {
	Emails []string `json:"emails"`
	Role   string   `json:"role"`
}

// CreateInvitations menangani POST /workspaces/:wsId/invitations (S2-19) --
// satu email atau massal lewat array yang sama. Email yang sudah terdaftar
// (S2-23) langsung ditambahkan ke workspace, tidak dapat undangan baru.
func (h *InvitationHandler) CreateInvitations(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("CreateInvitations dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("CreateInvitations dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	var req createInvitationsRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	if len(req.Emails) == 0 {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "emails wajib diisi minimal satu",
			[]response.FieldError{{Field: "emails", Message: "wajib diisi"}}))
	}
	if !validInvitationRoles[req.Role] {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "role tidak valid",
			[]response.FieldError{{Field: "role", Message: "harus salah satu dari admin_workspace, project_manager, editor, approver, viewer"}}))
	}

	workspaceName, err := h.invitations.GetWorkspaceName(c.Context(), exec, workspaceID)
	if err != nil {
		h.logger.Error("gagal ambil nama workspace untuk isi email undangan", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memproses undangan", nil))
	}
	inviterName, err := h.accounts.GetDisplayName(c.Context(), actorUserID)
	if err != nil {
		h.logger.Error("gagal ambil nama actor untuk isi email undangan", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memproses undangan", nil))
	}

	result, err := h.invitations.CreateBulkInvitations(c.Context(), exec, req.Emails, workspaceID, req.Role, actorUserID, actorRole, workspaceName, inviterName)
	if err != nil {
		h.logger.Error("gagal membuat undangan", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal membuat undangan", nil))
	}

	invitationIDs := make([]string, len(result.Created))
	for i, inv := range result.Created {
		invitationIDs[i] = inv.ID
	}

	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{
		"invitation_ids": invitationIDs,
		"added_directly": result.AddedDirectly,
		"errors":         result.Errors,
	}))
}

type acceptInvitationRequest struct {
	Token       string `json:"token"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

// AcceptInvitation menangani POST /auth/invitations/accept (S2-20,
// `[PUBLIC]`) -- non-SSO saja untuk sekarang, lihat komentar
// service.InvitationService.AcceptInvitation soal SSO yang belum
// diimplementasikan. Transaksi RLS dibangun di sini (bukan
// DBContextMiddleware) karena rute ini tidak punya sesi/JWT sama sekali --
// lihat komentar service.InvitationService.AcceptInvitation.
func (h *InvitationHandler) AcceptInvitation(c *fiber.Ctx) error {
	var req acceptInvitationRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	if req.Token == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "token wajib diisi", nil))
	}
	if msg := validator.ValidatePasswordComplexity(req.Password); msg != "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", msg,
			[]response.FieldError{{Field: "password", Message: msg}}))
	}

	tx, err := db.SetRLSContext(c.Context(), h.pool, "", "platform_admin")
	if err != nil {
		h.logger.Error("gagal menyiapkan transaksi accept invitation", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}

	result, err := h.invitations.AcceptInvitation(c.Context(), tx, req.Token, req.DisplayName, req.Password)
	if err != nil {
		tx.Rollback(c.Context()) //nolint:errcheck // request sudah gagal, rollback best-effort
		switch {
		case errors.Is(err, domain.ErrInvitationNotFound):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_OR_EXPIRED_TOKEN",
				"Link undangan tidak valid, sudah kedaluwarsa, atau sudah dipakai.", nil))
		case errors.Is(err, domain.ErrInvalidInput):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "display_name minimal 2 karakter",
				[]response.FieldError{{Field: "display_name", Message: "minimal 2 karakter"}}))
		case errors.Is(err, domain.ErrEmailAlreadyExists):
			return c.Status(fiber.StatusConflict).JSON(response.Error("EMAIL_ALREADY_EXISTS", "Email ini sudah terdaftar.", nil))
		default:
			h.logger.Error("gagal memproses accept invitation", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memproses undangan", nil))
		}
	}
	if err := tx.Commit(c.Context()); err != nil {
		h.logger.Error("gagal commit accept invitation", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyimpan perubahan", nil))
	}

	return c.JSON(response.Success(fiber.Map{
		"user_id":      result.UserID,
		"email":        result.Email,
		"workspace_id": result.WorkspaceID,
		"role":         result.Role,
	}))
}

// CancelInvitation menangani DELETE /workspaces/:wsId/invitations/:invId (S2-21).
func (h *InvitationHandler) CancelInvitation(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("CancelInvitation dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("CancelInvitation dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")
	invitationID := c.Params("invId")

	if err := h.invitations.CancelInvitation(c.Context(), exec, workspaceID, invitationID, actorUserID); err != nil {
		if errors.Is(err, domain.ErrInvitationNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(response.Error("INVITATION_NOT_FOUND",
				"Undangan tidak ditemukan atau sudah diterima/dibatalkan.", nil))
		}
		h.logger.Error("gagal membatalkan undangan", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal membatalkan undangan", nil))
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// ResendInvitation menangani POST /workspaces/:wsId/invitations/:invId/resend (S2-22).
func (h *InvitationHandler) ResendInvitation(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ResendInvitation dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ResendInvitation dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")
	invitationID := c.Params("invId")

	workspaceName, err := h.invitations.GetWorkspaceName(c.Context(), exec, workspaceID)
	if err != nil {
		h.logger.Error("gagal ambil nama workspace untuk isi email undangan", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memproses undangan", nil))
	}
	inviterName, err := h.accounts.GetDisplayName(c.Context(), actorUserID)
	if err != nil {
		h.logger.Error("gagal ambil nama actor untuk isi email undangan", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memproses undangan", nil))
	}

	if err := h.invitations.ResendInvitation(c.Context(), exec, workspaceID, invitationID, workspaceName, inviterName); err != nil {
		if errors.Is(err, domain.ErrInvitationNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(response.Error("INVITATION_NOT_FOUND",
				"Undangan tidak ditemukan atau sudah diterima/dibatalkan.", nil))
		}
		h.logger.Error("gagal mengirim ulang undangan", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengirim ulang undangan", nil))
	}

	return c.JSON(response.Success(fiber.Map{"message": "Email undangan berhasil dikirim ulang."}))
}
