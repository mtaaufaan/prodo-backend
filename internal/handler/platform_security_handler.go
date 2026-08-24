package handler

import (
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// PlatformSecurityHandler -- S4P-18, US-070: panel Platform Admin untuk
// mengubah session timeout (global, semua akun PA) dan IP allowlist
// (self-service, per akun PA sendiri).
type PlatformSecurityHandler struct {
	accounts *service.AccountService
	logger   *zap.Logger
}

func NewPlatformSecurityHandler(accounts *service.AccountService, logger *zap.Logger) *PlatformSecurityHandler {
	return &PlatformSecurityHandler{accounts: accounts, logger: logger}
}

// resolveActor -- dipakai keempat handler di bawah, sama pola dengan
// GroupAdminHandler. failed=true berarti response error SUDAH ditulis ke c
// -- caller wajib langsung `return nil` tanpa lanjut memproses (fasthttp
// membuffer respons sampai handler selesai, jadi kalau caller tetap lanjut
// dan menulis response sukses di akhir, response error yang sudah ditulis
// di sini akan TERTIMPA -- ini BUKAN net/http yang panic dobel-write).
func (h *PlatformSecurityHandler) resolveActor(c *fiber.Ctx) (actorUserID string, failed bool) {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		_ = c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
		return "", true
	}
	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users",
			zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		_ = c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
		return "", true
	}
	return actorUserID, false
}

// Get menangani GET /platform/security-settings.
func (h *PlatformSecurityHandler) Get(c *fiber.Ctx) error {
	actorUserID, failed := h.resolveActor(c)
	if failed {
		return nil
	}

	seconds, err := h.accounts.GetPASessionIdleTimeoutSeconds(c.Context())
	if err != nil {
		h.logger.Error("gagal membaca session timeout Platform Admin", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil pengaturan keamanan", nil))
	}

	entries, err := h.accounts.ListIPAllowlist(c.Context(), actorUserID)
	if err != nil {
		h.logger.Error("gagal membaca IP allowlist Platform Admin", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil pengaturan keamanan", nil))
	}
	data := make([]fiber.Map, len(entries))
	for i, e := range entries {
		data[i] = fiber.Map{
			"id":         e.ID,
			"cidr":       e.CIDR,
			"created_at": e.CreatedAt.UTC().Format(time.RFC3339),
		}
	}

	return c.JSON(response.Success(fiber.Map{
		"session_idle_timeout_seconds": seconds,
		"ip_allowlist":                 data,
	}))
}

type updateSessionTimeoutRequest struct {
	Seconds int `json:"seconds"`
}

// UpdateSessionTimeout menangani PUT /platform/security-settings/session-timeout.
func (h *PlatformSecurityHandler) UpdateSessionTimeout(c *fiber.Ctx) error {
	actorUserID, failed := h.resolveActor(c)
	if failed {
		return nil
	}

	var req updateSessionTimeoutRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}

	if err := h.accounts.SetPASessionIdleTimeout(c.Context(), req.Seconds, actorUserID); err != nil {
		switch {
		case errors.Is(err, domain.ErrSessionTimeoutTooShort):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("SESSION_TIMEOUT_TOO_SHORT",
				"Session timeout minimal 10 menit (600 detik).", nil))
		default:
			h.logger.Error("gagal mengubah session timeout Platform Admin", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengubah pengaturan keamanan", nil))
		}
	}

	return c.JSON(response.Success(fiber.Map{"session_idle_timeout_seconds": req.Seconds}))
}

type addIPAllowlistRequest struct {
	CIDR string `json:"cidr"`
}

// AddIPAllowlist menangani POST /platform/security-settings/ip-allowlist.
func (h *PlatformSecurityHandler) AddIPAllowlist(c *fiber.Ctx) error {
	actorUserID, failed := h.resolveActor(c)
	if failed {
		return nil
	}

	var req addIPAllowlistRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.CIDR = strings.TrimSpace(req.CIDR)
	if req.CIDR == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "cidr wajib diisi", nil))
	}

	id, err := h.accounts.AddIPAllowlistEntry(c.Context(), actorUserID, req.CIDR, actorUserID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidCIDR):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("INVALID_CIDR", "Format CIDR tidak valid.", nil))
		default:
			h.logger.Error("gagal menambah IP allowlist Platform Admin", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menambah IP allowlist", nil))
		}
	}

	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{"id": id, "cidr": req.CIDR}))
}

// DeleteIPAllowlist menangani DELETE /platform/security-settings/ip-allowlist/:id.
func (h *PlatformSecurityHandler) DeleteIPAllowlist(c *fiber.Ctx) error {
	actorUserID, failed := h.resolveActor(c)
	if failed {
		return nil
	}

	entryID := c.Params("id")
	if entryID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "ID entry wajib diisi", nil))
	}

	if err := h.accounts.DeleteIPAllowlistEntry(c.Context(), actorUserID, entryID, actorUserID); err != nil {
		switch {
		case errors.Is(err, domain.ErrIPAllowlistEntryNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Entry IP allowlist tidak ditemukan", nil))
		default:
			h.logger.Error("gagal menghapus IP allowlist Platform Admin", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menghapus IP allowlist", nil))
		}
	}

	return c.JSON(response.Success(fiber.Map{"id": entryID, "deleted": true}))
}
