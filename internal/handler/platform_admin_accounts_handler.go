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

// PlatformAdminAccountsHandler -- S4P-37/38/39/40, US-084: Platform Admin
// mengelola akun Platform Admin lain.
type PlatformAdminAccountsHandler struct {
	accounts   *service.AccountService
	email      *service.EmailService
	appBaseURL string
	logger     *zap.Logger
}

func NewPlatformAdminAccountsHandler(accounts *service.AccountService, email *service.EmailService, appBaseURL string, logger *zap.Logger) *PlatformAdminAccountsHandler {
	return &PlatformAdminAccountsHandler{accounts: accounts, email: email, appBaseURL: appBaseURL, logger: logger}
}

type createPlatformAdminRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

// Create menangani POST /platform/admins (S4P-37).
func (h *PlatformAdminAccountsHandler) Create(c *fiber.Ctx) error {
	var req createPlatformAdminRequest
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

	actorUserID, err := h.resolveActor(c)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	result, err := h.accounts.CreatePlatformAdmin(c.Context(), req.Email, req.DisplayName, actorUserID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrEmailAlreadyExists):
			return c.Status(fiber.StatusConflict).JSON(response.Error("CONFLICT", "Email sudah terdaftar", nil))
		case errors.Is(err, domain.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "email dan display_name wajib diisi", nil))
		default:
			h.logger.Error("gagal membuat akun Platform Admin", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal membuat akun Platform Admin", nil))
		}
	}

	link := fmt.Sprintf("%s/activate?token=%s", h.appBaseURL, result.ActivationToken)
	if err := h.email.SendActivationEmail(c.Context(), result.Email, result.DisplayName, link, result.ExpiresAt); err != nil {
		h.logger.Error("akun Platform Admin dibuat tapi gagal kirim email aktivasi", zap.String("user_id", result.UserID), zap.Error(err))
	}

	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{
		"id":           result.UserID,
		"email":        result.Email,
		"display_name": result.DisplayName,
		"expires_at":   result.ExpiresAt.UTC().Format(time.RFC3339),
	}))
}

// List menangani GET /platform/admins (S4P-40, endpoint tambahan --
// dikonfirmasi user, dibutuhkan tabel PlatformAdminAccountsPage).
func (h *PlatformAdminAccountsHandler) List(c *fiber.Ctx) error {
	admins, err := h.accounts.ListPlatformAdmins(c.Context())
	if err != nil {
		h.logger.Error("gagal mengambil daftar Platform Admin", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil daftar Platform Admin", nil))
	}

	data := make([]fiber.Map, len(admins))
	for i := range admins {
		a := &admins[i]
		m := fiber.Map{
			"id":            a.ID,
			"email":         a.Email,
			"display_name":  a.DisplayName,
			"is_active":     a.IsActive,
			"suspended_at":  nil,
			"last_login_at": nil,
			"created_at":    a.CreatedAt.UTC().Format(time.RFC3339),
		}
		if a.SuspendedAt != nil {
			m["suspended_at"] = a.SuspendedAt.UTC().Format(time.RFC3339)
		}
		if a.LastLoginAt != nil {
			m["last_login_at"] = a.LastLoginAt.UTC().Format(time.RFC3339)
		}
		data[i] = m
	}
	return c.JSON(response.Success(data))
}

func (h *PlatformAdminAccountsHandler) resolveActor(c *fiber.Ctx) (string, error) {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return "", fmt.Errorf("token tidak ditemukan")
	}
	if actorUserID, _, ok := middleware.ActorFromContext(c); ok {
		return actorUserID, nil
	}
	return h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
}

// Deactivate menangani PUT /platform/admins/:id/deactivate (S4P-38).
func (h *PlatformAdminAccountsHandler) Deactivate(c *fiber.Ctx) error {
	actorUserID, err := h.resolveActor(c)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	if err := h.accounts.DeactivatePlatformAdmin(c.Context(), c.Params("id"), actorUserID); err != nil {
		switch {
		case errors.Is(err, domain.ErrCannotDeactivateSelf):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("CANNOT_DEACTIVATE_SELF", "Tidak dapat menonaktifkan akun sendiri", nil))
		case errors.Is(err, domain.ErrMinimumActiveAdminRequired):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("MINIMUM_ACTIVE_ADMIN_REQUIRED", "Minimal satu Platform Admin aktif harus tersisa", nil))
		case errors.Is(err, domain.ErrPlatformAdminNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Akun Platform Admin tidak ditemukan", nil))
		default:
			h.logger.Error("gagal menonaktifkan Platform Admin", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menonaktifkan akun", nil))
		}
	}
	return c.JSON(response.Success(fiber.Map{"id": c.Params("id"), "suspended": true}))
}

// Reactivate menangani PUT /platform/admins/:id/reactivate (S4P-38
// tambahan, dikonfirmasi user -- mirror suspend/reactivate Group Admin).
func (h *PlatformAdminAccountsHandler) Reactivate(c *fiber.Ctx) error {
	actorUserID, err := h.resolveActor(c)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	if err := h.accounts.ReactivatePlatformAdmin(c.Context(), c.Params("id"), actorUserID); err != nil {
		if errors.Is(err, domain.ErrPlatformAdminNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Akun Platform Admin tidak ditemukan", nil))
		}
		h.logger.Error("gagal mengaktifkan kembali Platform Admin", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengaktifkan kembali akun", nil))
	}
	return c.JSON(response.Success(fiber.Map{"id": c.Params("id"), "suspended": false}))
}

// ResetMFA menangani POST /platform/admins/:id/reset-mfa (S4P-39).
func (h *PlatformAdminAccountsHandler) ResetMFA(c *fiber.Ctx) error {
	actorUserID, err := h.resolveActor(c)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	if err := h.accounts.ResetPlatformAdminMFA(c.Context(), c.Params("id"), actorUserID); err != nil {
		switch {
		case errors.Is(err, domain.ErrCannotResetOwnMFA):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("CANNOT_RESET_OWN_MFA", "Tidak dapat mereset MFA akun sendiri", nil))
		case errors.Is(err, domain.ErrPlatformAdminNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Akun Platform Admin tidak ditemukan", nil))
		default:
			h.logger.Error("gagal mereset MFA Platform Admin", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mereset MFA", nil))
		}
	}
	return c.JSON(response.Success(fiber.Map{"id": c.Params("id"), "mfa_reset": true}))
}
