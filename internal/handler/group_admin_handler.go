package handler

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/validator"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// GroupAdminHandler -- S1-05, US-073: Platform Admin membuat akun Group Admin.
type GroupAdminHandler struct {
	accounts   *service.AccountService
	email      *service.EmailService
	appBaseURL string
	logger     *zap.Logger
}

func NewGroupAdminHandler(accounts *service.AccountService, email *service.EmailService, appBaseURL string, logger *zap.Logger) *GroupAdminHandler {
	return &GroupAdminHandler{accounts: accounts, email: email, appBaseURL: appBaseURL, logger: logger}
}

type createGroupAdminRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

// Create menangani POST /platform/group-admins -- dipasang di belakang
// middleware.JWTAuth + middleware.RequirePlatformAdmin (lihat cmd/api/main.go).
func (h *GroupAdminHandler) Create(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}

	var req createGroupAdminRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.Email = strings.TrimSpace(req.Email)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.Email == "" || req.DisplayName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "email dan display_name wajib diisi", nil))
	}
	if !validator.IsValidEmail(req.Email) {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "format email tidak valid", nil))
	}

	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users",
			zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	result, err := h.accounts.CreateGroupAdmin(c.Context(), service.CreateGroupAdminRequest{
		Email:           req.Email,
		DisplayName:     req.DisplayName,
		InvitedByUserID: actorUserID,
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrEmailAlreadyExists):
			return c.Status(fiber.StatusConflict).JSON(response.Error("CONFLICT", "Email sudah terdaftar", nil))
		case errors.Is(err, domain.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "email dan display_name wajib diisi", nil))
		default:
			h.logger.Error("gagal membuat akun Group Admin", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal membuat akun Group Admin", nil))
		}
	}

	h.sendActivationEmail(c, result, "akun Group Admin dibuat tapi gagal kirim email aktivasi")

	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{
		"id":           result.UserID,
		"email":        result.Email,
		"display_name": result.DisplayName,
		"expires_at":   result.ExpiresAt.UTC().Format(time.RFC3339),
	}))
}

// List menangani GET /platform/group-admins -- daftar Group Admin untuk
// panel Platform Admin (S1-12). Pagination page/per_page sesuai
// docs/coding-conventions.md §7.1 (default 1/50, max per_page 200).
func (h *GroupAdminHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	if page < 1 {
		page = 1
	}
	perPage := c.QueryInt("per_page", 50)
	if perPage < 1 {
		perPage = 50
	}
	if perPage > 200 {
		perPage = 200
	}

	summaries, total, err := h.accounts.ListGroupAdmins(c.Context(), perPage, (page-1)*perPage)
	if err != nil {
		h.logger.Error("gagal mengambil daftar Group Admin", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil daftar Group Admin", nil))
	}

	data := make([]fiber.Map, len(summaries))
	for i, s := range summaries {
		status := "pending"
		if s.IsActive {
			status = "active"
		}
		data[i] = fiber.Map{
			"id":           s.ID,
			"email":        s.Email,
			"display_name": s.DisplayName,
			"status":       status,
			"created_at":   s.CreatedAt.UTC().Format(time.RFC3339),
		}
	}

	totalPages := (total + perPage - 1) / perPage
	return c.JSON(fiber.Map{
		"data": data,
		"meta": fiber.Map{
			"page":        page,
			"per_page":    perPage,
			"total":       total,
			"total_pages": totalPages,
		},
	})
}

// ResendActivation menangani POST /platform/group-admins/:id/resend-activation
// -- S1-08. Meng-invalidate token lama, menerbitkan yang baru, kirim ulang
// email. Dipasang di belakang middleware yang sama dengan Create.
func (h *GroupAdminHandler) ResendActivation(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}

	targetUserID := c.Params("id")
	if targetUserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "ID user wajib diisi", nil))
	}

	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users",
			zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	result, err := h.accounts.ResendActivation(c.Context(), targetUserID, actorUserID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrUserNotFound), errors.Is(err, domain.ErrInvitationNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND",
				"User tidak ditemukan atau tidak ada invitation pending untuknya (mungkin sudah diaktivasi)", nil))
		default:
			h.logger.Error("gagal menerbitkan ulang token aktivasi", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menerbitkan ulang token aktivasi", nil))
		}
	}

	h.sendActivationEmail(c, result, "token aktivasi diterbitkan ulang tapi gagal kirim email")

	return c.JSON(response.Success(fiber.Map{
		"id":           result.UserID,
		"email":        result.Email,
		"display_name": result.DisplayName,
		"expires_at":   result.ExpiresAt.UTC().Format(time.RFC3339),
	}))
}

// Suspend menangani PUT /platform/group-admins/:id/suspend (S4P-02, US-067).
func (h *GroupAdminHandler) Suspend(c *fiber.Ctx) error {
	return h.setSuspension(c, true)
}

// Reactivate menangani PUT /platform/group-admins/:id/reactivate (S4P-02, US-067).
func (h *GroupAdminHandler) Reactivate(c *fiber.Ctx) error {
	return h.setSuspension(c, false)
}

// setSuspension -- logika bersama Suspend/Reactivate, cuma beda repo call
// dan pesan sukses.
func (h *GroupAdminHandler) setSuspension(c *fiber.Ctx, suspend bool) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}

	targetUserID := c.Params("id")
	if targetUserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "ID user wajib diisi", nil))
	}

	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users",
			zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	status := "suspended"
	if suspend {
		err = h.accounts.SuspendGroupAdmin(c.Context(), targetUserID, actorUserID)
	} else {
		status = "active"
		err = h.accounts.ReactivateGroupAdmin(c.Context(), targetUserID, actorUserID)
	}
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrUserNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Group Admin tidak ditemukan", nil))
		default:
			h.logger.Error("gagal mengubah status suspend Group Admin", zap.Bool("suspend", suspend), zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengubah status akun", nil))
		}
	}

	return c.JSON(response.Success(fiber.Map{
		"id":     targetUserID,
		"status": status,
	}))
}

// sendActivationEmail mengirim email aktivasi -- dipakai Create (S1-05) dan
// ResendActivation (S1-08). Gagal kirim TIDAK membatalkan aksi utama
// (ponytail: akun/token sudah terlanjur dibuat/diterbitkan; kegagalan
// dicatat sebagai error, bukan menggagalkan seluruh request).
func (h *GroupAdminHandler) sendActivationEmail(c *fiber.Ctx, result *service.GroupAdminInvitation, failureLogMsg string) {
	link := fmt.Sprintf("%s/activate?token=%s", h.appBaseURL, result.ActivationToken)
	if err := h.email.SendActivationEmail(c.Context(), result.Email, result.DisplayName, link, result.ExpiresAt); err != nil {
		h.logger.Error(failureLogMsg, zap.String("user_id", result.UserID), zap.Error(err))
	}
}
