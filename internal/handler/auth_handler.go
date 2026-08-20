package handler

import (
	"errors"
	"unicode"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/validator"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// AuthHandler -- S1-06/07, US-073: aktivasi akun (set password + init MFA,
// lalu verifikasi OTP pertama). `[PUBLIC]` sesuai docs/API_CONTRACT.md --
// token di body request adalah kredensial one-time-nya, bukan Authorization header.
type AuthHandler struct {
	activation *service.ActivationService
	logger     *zap.Logger
}

func NewAuthHandler(activation *service.ActivationService, logger *zap.Logger) *AuthHandler {
	return &AuthHandler{activation: activation, logger: logger}
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
