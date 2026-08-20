package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/validator"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// AuthHandler -- S1-06, US-073: langkah pertama aktivasi akun (set password
// + init MFA). `[PUBLIC]` sesuai docs/API_CONTRACT.md -- token di body request
// adalah kredensial one-time-nya, bukan Authorization header.
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
