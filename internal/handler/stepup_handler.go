package handler

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// StepUpHandler -- POST /auth/step-up (S16-05, forward-pull Track S4G):
// verifikasi OTP ulang, dipanggil FE otomatis saat menerima 403
// STEP_UP_REQUIRED dari middleware.RequireStepUp.
type StepUpHandler struct {
	stepUp   *service.StepUpService
	accounts actorResolver
	logger   *zap.Logger
}

func NewStepUpHandler(stepUp *service.StepUpService, accounts actorResolver, logger *zap.Logger) *StepUpHandler {
	return &StepUpHandler{stepUp: stepUp, accounts: accounts, logger: logger}
}

type verifyStepUpRequest struct {
	Code string `json:"code"`
}

func (h *StepUpHandler) Verify(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	userID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("JWT valid tapi tidak ditemukan di tabel users", zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}

	var req verifyStepUpRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.Code = strings.TrimSpace(req.Code)
	if req.Code == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "code wajib diisi", nil))
	}

	if err := h.stepUp.Verify(c.Context(), userID, claims.ID, req.Code); err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidOTP):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_OTP", "Kode OTP tidak valid.", nil))
		case errors.Is(err, domain.ErrMFARequired):
			return c.Status(fiber.StatusForbidden).JSON(response.Error("MFA_REQUIRED", "Akun ini belum mengaktifkan MFA.", nil))
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memverifikasi step-up", nil))
		}
	}
	return c.JSON(fiber.Map{"data": fiber.Map{"verified": true}})
}
