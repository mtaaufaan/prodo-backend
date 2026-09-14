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
// workspace_handler.go untuk satu daftar konstanta. division_viewer
// ditambahkan 2026-09-14 (role restructuring, sebelumnya cuma bisa
// diberikan lewat jalur lain -- gap, tidak pernah ada di daftar ini).
var validInvitationRoles = map[string]bool{
	"admin_workspace": true,
	"project_manager": true,
	"editor":          true,
	"approver":        true,
	"division_viewer": true,
	"viewer":          true,
}

// projectScopedInvitationRoles -- role yang berjalan PADA project tertentu
// (dikonfirmasi user 2026-09-14: "role lainnya adalah role berbasis
// project... PM, Editor, Approver, dan Viewer harus mencantumkan sampai
// level project"). admin_workspace/division_viewer SEBALIKNYA workspace-
// scoped murni -- project_id wajib kosong untuk keduanya.
var projectScopedInvitationRoles = map[string]bool{
	"project_manager": true,
	"editor":          true,
	"approver":        true,
	"viewer":          true,
}

type createInvitationsRequest struct {
	Emails    []string `json:"emails"`
	Role      string   `json:"role"`
	ProjectID string   `json:"project_id"`
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
			[]response.FieldError{{Field: "role", Message: "harus salah satu dari admin_workspace, project_manager, editor, approver, division_viewer, viewer"}}))
	}
	// Role restructuring 2026-09-14 (dikonfirmasi user): role project-level
	// (PM/editor/approver/viewer) WAJIB mencantumkan project_id, role
	// workspace-level (admin_workspace/division_viewer) SEBALIKNYA tidak
	// boleh -- tidak ada lagi wacana mengundang member tanpa role/scope.
	if projectScopedInvitationRoles[req.Role] && req.ProjectID == "" {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "project_id wajib diisi untuk role ini",
			[]response.FieldError{{Field: "project_id", Message: "wajib diisi untuk role project_manager/editor/approver/viewer"}}))
	}
	if !projectScopedInvitationRoles[req.Role] && req.ProjectID != "" {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "project_id tidak boleh diisi untuk role ini",
			[]response.FieldError{{Field: "project_id", Message: "hanya berlaku untuk role project_manager/editor/approver/viewer"}}))
	}
	// S4W-01 guard admin_workspace->admin_workspace (WorkspaceHandler.
	// UpdateMemberRole) SENGAJA TIDAK disalin ke sini lagi -- dibuka
	// kembali 2026-09-14 atas konfirmasi eksplisit user ("Ya, buka -- AW
	// boleh undang AW lain"): AW boleh MENGUNDANG AW lain lewat endpoint
	// ini, guard di UpdateMemberRole (ubah role member existing) TETAP ADA
	// tidak berubah -- dua wewenang yang sengaja dipisah.

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

	result, err := h.invitations.CreateBulkInvitations(c.Context(), exec, req.Emails, workspaceID, req.Role, actorUserID, actorRole, workspaceName, inviterName, req.ProjectID)
	if err != nil {
		if errors.Is(err, domain.ErrProjectNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(response.Error("PROJECT_NOT_FOUND", "Project tidak ditemukan di workspace ini", nil))
		}
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
	Title       string `json:"title"`
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

	result, err := h.invitations.AcceptInvitation(c.Context(), tx, req.Token, req.DisplayName, req.Title, req.Password)
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

// PreviewInvitation menangani GET /invitations/preview?token= (`[PUBLIC]`,
// permintaan user 2026-09-10) -- dibaca halaman aktivasi SEBELUM submit,
// supaya tahu apakah ini undangan Eksekutif (copy beda) dan bisa
// pre-fill Nama/Jabatan kalau GA sudah mengisikannya lewat "Kelola".
// Read-only, transaksi selalu di-rollback (sama pola AcceptInvitation
// soal konteks RLS khusus rute publik, tapi tidak pernah commit di sini).
func (h *InvitationHandler) PreviewInvitation(c *fiber.Ctx) error {
	token := c.Query("token")
	if token == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "token wajib diisi", nil))
	}

	tx, err := db.SetRLSContext(c.Context(), h.pool, "", "platform_admin")
	if err != nil {
		h.logger.Error("gagal menyiapkan transaksi preview invitation", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	defer tx.Rollback(c.Context()) //nolint:errcheck // read-only, selalu rollback

	preview, err := h.invitations.PreviewInvitation(c.Context(), tx, token)
	if err != nil {
		if errors.Is(err, domain.ErrInvitationNotFound) {
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_OR_EXPIRED_TOKEN",
				"Link undangan tidak valid, sudah kedaluwarsa, atau sudah dipakai.", nil))
		}
		h.logger.Error("gagal memproses preview invitation", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memproses undangan", nil))
	}

	return c.JSON(response.Success(fiber.Map{
		"is_executive": preview.IsExecutiveInvite,
		"display_name": preview.DisplayName,
		"title":        preview.Title,
	}))
}

// ListPendingInvitations menangani GET /workspaces/:wsId/invitations --
// prasyarat minimal S2-28 (daftar undangan pending di FE), belum pernah
// dijadwalkan sebagai task backend terpisah -- lihat
// implementation_gaps.md IG-09.
func (h *InvitationHandler) ListPendingInvitations(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ListPendingInvitations dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	invitations, err := h.invitations.ListPendingInvitations(c.Context(), exec, workspaceID)
	if err != nil {
		h.logger.Error("gagal ambil daftar undangan pending", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil daftar undangan", nil))
	}

	data := make([]fiber.Map, len(invitations))
	for i := range invitations {
		inv := &invitations[i]
		data[i] = fiber.Map{
			"id":           inv.ID,
			"email":        inv.Email,
			"role":         inv.Role,
			"created_at":   inv.CreatedAt,
			"expires_at":   inv.ExpiresAt,
			"project_name": inv.ProjectName,
		}
	}

	return c.JSON(response.Success(fiber.Map{"pending_invitations": data}))
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
