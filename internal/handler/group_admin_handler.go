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
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("UNAUTHORIZED", "Token tidak ditemukan", nil))
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

	link := fmt.Sprintf("%s/activate?token=%s", h.appBaseURL, result.ActivationToken)
	if err := h.email.SendActivationEmail(c.Context(), result.Email, result.DisplayName, link, result.ExpiresAt); err != nil {
		// ponytail: akun sudah terlanjur dibuat -- gagal kirim email tidak
		// membatalkan pembuatan akun. Platform Admin bisa resend lewat
		// S1-08 (belum diimplementasikan) begitu tersedia.
		h.logger.Error("akun Group Admin dibuat tapi gagal kirim email aktivasi",
			zap.String("user_id", result.UserID), zap.Error(err))
	}

	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{
		"id":           result.UserID,
		"email":        result.Email,
		"display_name": result.DisplayName,
		"expires_at":   result.ExpiresAt.UTC().Format(time.RFC3339),
	}))
}
