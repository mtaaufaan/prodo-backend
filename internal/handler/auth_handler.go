package handler

import (
	"errors"
	"strings"
	"unicode"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/validator"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// AuthHandler -- S1-06/07, US-073 (aktivasi akun) + S1-18, US-001 (login
// credential lokal). `[PUBLIC]` sesuai docs/API_CONTRACT.md.
type AuthHandler struct {
	activation *service.ActivationService
	auth       *service.AuthService
	logger     *zap.Logger
}

func NewAuthHandler(activation *service.ActivationService, auth *service.AuthService, logger *zap.Logger) *AuthHandler {
	return &AuthHandler{activation: activation, auth: auth, logger: logger}
}

type activateRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// Activate menangani POST /auth/activate.
func (h *AuthHandler) Activate(c *fiber.Ctx) error {
	var req activateRequest
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

	result, err := h.activation.SetPasswordAndInitMFA(c.Context(), req.Token, req.Password)
	if err != nil {
		if errors.Is(err, domain.ErrInvitationNotFound) {
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_OR_EXPIRED_TOKEN",
				"Link aktivasi tidak valid atau sudah kedaluwarsa (72 jam).", nil))
		}
		h.logger.Error("gagal memproses aktivasi akun", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memproses aktivasi", nil))
	}

	return c.JSON(response.Success(fiber.Map{
		"message":     "Password berhasil disetel. Lanjutkan setup MFA.",
		"totp_qr_url": "data:image/png;base64," + result.QRCodePNGBase64,
	}))
}

type verifyMFARequest struct {
	Token   string `json:"token"`
	OTPCode string `json:"otp_code"`
}

// VerifyMFA menangani POST /auth/activate/mfa-verify -- S1-07, langkah
// terakhir onboarding US-073.
func (h *AuthHandler) VerifyMFA(c *fiber.Ctx) error {
	var req verifyMFARequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	if req.Token == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "token wajib diisi", nil))
	}
	if !isSixDigits(req.OTPCode) {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "otp_code harus 6 digit angka", nil))
	}

	err := h.activation.VerifyMFAAndActivate(c.Context(), req.Token, req.OTPCode)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvitationNotFound):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_OR_EXPIRED_TOKEN",
				"Link aktivasi tidak valid, sudah kedaluwarsa, atau MFA sudah aktif.", nil))
		case errors.Is(err, domain.ErrInvalidOTP):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_OTP",
				"Kode OTP tidak valid atau sudah kedaluwarsa.", nil))
		default:
			h.logger.Error("gagal verifikasi MFA", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal verifikasi MFA", nil))
		}
	}

	return c.JSON(response.Success(fiber.Map{
		"message":     "Akun berhasil diaktifkan.",
		"mfa_enabled": true,
	}))
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	MFACode  string `json:"mfa_code"`
}

// Login menangani POST /auth/login -- S1-18, US-001. mfa_code opsional di
// body (dokumentasi API_CONTRACT.md §2): dibutuhkan kalau akun (Group
// Admin) sudah punya MFA aktif, dicek AuthService.VerifyMFA.
func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req loginRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "email dan password wajib diisi", nil))
	}
	if !validator.IsValidEmail(req.Email) {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "format email tidak valid", nil))
	}

	result, err := h.auth.Login(c.Context(), req.Email, req.Password, req.MFACode)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidCredentials):
			return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Email atau password tidak valid.", nil))
		case errors.Is(err, domain.ErrAccountInactive):
			return c.Status(fiber.StatusForbidden).JSON(response.Error("ACCOUNT_INACTIVE",
				"Akun belum aktif. Periksa email undangan Anda atau hubungi administrator.", nil))
		case errors.Is(err, domain.ErrInvalidOTP):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_OTP", "Kode OTP tidak valid.", nil))
		case errors.Is(err, domain.ErrMFARequired):
			// ponytail: seharusnya tidak pernah terjadi lewat onboarding
			// normal (lihat domain.ErrMFARequired) -- 403 generik, bukan
			// alur "mfa_setup_token" di API_CONTRACT.md §2 (itu untuk
			// varian member self-signup yang belum dibangun, lihat
			// Appendix A kontrak). Kalau nanti varian itu dibangun,
			// cabang ini perlu direvisi untuk menerbitkan setup_token.
			return c.Status(fiber.StatusForbidden).JSON(response.Error("MFA_REQUIRED",
				"MFA wajib aktif untuk akun ini. Hubungi administrator.", nil))
		default:
			h.logger.Error("gagal memproses login", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memproses login", nil))
		}
	}

	return c.JSON(response.Success(fiber.Map{
		"access_token":  result.AccessToken,
		"refresh_token": result.RefreshToken,
		"token_type":    result.TokenType,
		"expires_in":    result.ExpiresIn,
		"user": fiber.Map{
			"id":            result.User.ID,
			"email":         result.User.Email,
			"display_name":  result.User.DisplayName,
			"platform_role": result.User.PlatformRole,
			"avatar_url":    result.User.AvatarURL,
		},
	}))
}

func isSixDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
