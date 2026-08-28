package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// actorResolver -- subset AccountService dibutuhkan handler ini untuk
// memetakan Keycloak sub (claims.Subject) ke users.id PRODO.
type actorResolver interface {
	ResolveActorUserID(ctx context.Context, keycloakSub string) (string, error)
}

// ErasureHandler -- S4P-29/30/31, US-060: Right to Erasure.
type ErasureHandler struct {
	erasure  *service.ErasureService
	accounts actorResolver
	logger   *zap.Logger
}

func NewErasureHandler(erasure *service.ErasureService, accounts actorResolver, logger *zap.Logger) *ErasureHandler {
	return &ErasureHandler{erasure: erasure, accounts: accounts, logger: logger}
}

type createErasureRequestBody struct {
	UserID string `json:"user_id"`
	OrgID  string `json:"org_id"`
	Reason string `json:"reason"`
}

// Create menangani POST /platform/erasure-requests (S4P-29) -- SENGAJA
// TIDAK digerbangi RequirePlatformAdmin() di main.go: user/AW/PM mengajukan
// permintaan (docs/security-compliance.md §6.2), Platform Admin cuma
// mengeksekusi/menolak (Execute/Reject di bawah). Otorisasi per-request
// (diri sendiri / AW-PM workspace bersama / GA-PA) ada di service.
func (h *ErasureHandler) Create(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	requesterID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("JWT valid tapi tidak ditemukan di tabel users", zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}

	var req createErasureRequestBody
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.OrgID = strings.TrimSpace(req.OrgID)
	if req.OrgID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "org_id wajib diisi", nil))
	}

	id, err := h.erasure.CreateRequest(c.Context(), requesterID, claims.PlatformRole, strings.TrimSpace(req.UserID), req.OrgID, strings.TrimSpace(req.Reason))
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrForbidden):
			return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang mengajukan permintaan erasure untuk user ini.", nil))
		case errors.Is(err, domain.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
		default:
			h.logger.Error("gagal membuat erasure request", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal membuat permintaan erasure", nil))
		}
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{"id": id}))
}

// List menangani GET /platform/erasure-requests (S4P-30) -- gerbang
// RequirePlatformAdmin() di main.go.
func (h *ErasureHandler) List(c *fiber.Ctx) error {
	rows, err := h.erasure.List(c.Context())
	if err != nil {
		h.logger.Error("gagal mengambil daftar erasure request", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil antrian erasure", nil))
	}
	data := make([]fiber.Map, len(rows))
	for i, r := range rows {
		m := fiber.Map{
			"id":           r.ID,
			"subject":      r.Subject,
			"org":          r.OrgName,
			"requested_by": r.RequestedByName,
			"status":       r.Status,
			"requested_at": r.RequestedAt,
			"processed_at": nil,
		}
		if r.ProcessedAt != nil {
			m["processed_at"] = *r.ProcessedAt
		}
		data[i] = m
	}
	return c.JSON(response.Success(data))
}

type executeErasureRequestBody struct {
	Confirmation string `json:"confirmation"`
}

// Execute menangani POST /platform/erasure-requests/:id/execute (S4P-31) --
// gerbang RequirePlatformAdmin() di main.go. Konfirmasi dua langkah: body
// confirmation harus persis "KONFIRMASI" (lihat service.ErasureService.Execute).
func (h *ErasureHandler) Execute(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	processedBy, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("JWT valid tapi tidak ditemukan di tabel users", zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	var req executeErasureRequestBody
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}

	if err := h.erasure.Execute(c.Context(), c.Params("id"), processedBy, req.Confirmation); err != nil {
		switch {
		case errors.Is(err, domain.ErrErasureConfirmationRequired):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Konfirmasi tidak valid. Ketik \"KONFIRMASI\" untuk melanjutkan.", nil))
		case errors.Is(err, domain.ErrErasureRequestNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Permintaan erasure tidak ditemukan", nil))
		case errors.Is(err, domain.ErrErasureRequestAlreadyProcessed):
			return c.Status(fiber.StatusConflict).JSON(response.Error("ALREADY_PROCESSED", "Permintaan erasure sudah diproses sebelumnya", nil))
		default:
			h.logger.Error("gagal mengeksekusi erasure request", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengeksekusi erasure", nil))
		}
	}
	return c.JSON(response.Success(fiber.Map{"status": "DONE"}))
}

// Reject menangani POST /platform/erasure-requests/:id/reject -- tambahan
// (lihat komentar ErasureRepository.Reject), gerbang RequirePlatformAdmin()
// di main.go.
func (h *ErasureHandler) Reject(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	processedBy, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("JWT valid tapi tidak ditemukan di tabel users", zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	if err := h.erasure.Reject(c.Context(), c.Params("id"), processedBy); err != nil {
		switch {
		case errors.Is(err, domain.ErrErasureRequestNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Permintaan erasure tidak ditemukan", nil))
		case errors.Is(err, domain.ErrErasureRequestAlreadyProcessed):
			return c.Status(fiber.StatusConflict).JSON(response.Error("ALREADY_PROCESSED", "Permintaan erasure sudah diproses sebelumnya", nil))
		default:
			h.logger.Error("gagal menolak erasure request", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menolak erasure request", nil))
		}
	}
	return c.JSON(response.Success(fiber.Map{"status": "REJECTED"}))
}
