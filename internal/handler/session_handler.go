package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// SessionHandler -- S1-29/30/33/34/35, US-004/US-005.
type SessionHandler struct {
	accounts *service.AccountService
	sessions *service.SessionService
	logger   *zap.Logger
}

func NewSessionHandler(accounts *service.AccountService, sessions *service.SessionService, logger *zap.Logger) *SessionHandler {
	return &SessionHandler{accounts: accounts, sessions: sessions, logger: logger}
}

func sessionSummaryJSON(s *service.SessionSummary) fiber.Map {
	return fiber.Map{
		"jti": s.JTI,
		"device_info": fiber.Map{
			"browser": s.Browser,
			"os":      s.OS,
			"ip":      s.IP,
		},
		"created_at":     s.CreatedAt,
		"last_active_at": s.LastActiveAt,
		"is_current":     s.IsCurrent,
	}
}

// List menangani GET /auth/sessions -- daftar sesi aktif milik user yang
// sedang login (S1-29), sesi yang sedang dipakai request ini ditandai
// is_current lewat jti klaim JWT.
func (h *SessionHandler) List(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}

	userID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("JWT valid tapi user tidak ditemukan di tabel users", zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}

	sessions, err := h.sessions.ListSessions(c.Context(), userID, claims.ID)
	if err != nil {
		h.logger.Error("gagal mengambil daftar sesi", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil daftar sesi", nil))
	}

	data := make([]fiber.Map, len(sessions))
	for i := range sessions {
		data[i] = sessionSummaryJSON(&sessions[i])
	}
	return c.JSON(response.Success(data))
}

// Revoke menangani DELETE /auth/sessions/:jti -- remote logout satu sesi
// milik sendiri (S1-33). domain.ErrSessionNotFound dibalas 403 (bukan 404)
// baik jti tidak ada MAUPUN milik user lain -- sengaja disamakan (lihat
// domain.ErrSessionNotFound) supaya tidak membocorkan keberadaan sesi user
// lain, sesuai satu-satunya error response yang didokumentasikan
// API_CONTRACT.md untuk endpoint ini.
func (h *SessionHandler) Revoke(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}

	userID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("JWT valid tapi user tidak ditemukan di tabel users", zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}

	if err := h.sessions.RevokeSession(c.Context(), userID, c.Params("jti")); err != nil {
		if errors.Is(err, domain.ErrSessionNotFound) {
			return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Hanya pemilik akun yang dapat merevoke sesi ini.", nil))
		}
		h.logger.Error("gagal revoke sesi", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengakhiri sesi", nil))
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// RevokeAll menangani DELETE /auth/sessions -- "akhiri semua sesi lain"
// milik sendiri (S1-34), sesi yang sedang dipakai request ini (claims.ID)
// dikecualikan.
func (h *SessionHandler) RevokeAll(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}

	userID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("JWT valid tapi user tidak ditemukan di tabel users", zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}

	if err := h.sessions.RevokeAllSessions(c.Context(), userID, claims.ID); err != nil {
		h.logger.Error("gagal revoke semua sesi lain", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengakhiri sesi", nil))
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// RevokeAllForUser menangani POST /admin/users/:userId/sessions/revoke-all --
// force logout semua sesi milik user target (S1-35). Otorisasi: Platform
// Admin (semua user) ATAU Group Admin dengan targetUserID member salah satu
// org yang dia kelola (S3-40, menutup implementation_gaps.md IG-01) --
// gerbang kasar PA/GA di routing (middleware.RequirePlatformRole), scoping
// halus di sini lewat middleware.RequireGroupAdminInOrg.
func (h *SessionHandler) RevokeAllForUser(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	targetUserID := c.Params("userId")

	if err := h.accounts.UserExists(c.Context(), targetUserID); err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(response.Error("USER_NOT_FOUND", "User tidak ditemukan.", nil))
		}
		h.logger.Error("gagal cek keberadaan user target", zap.String("target_user_id", targetUserID), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memproses permintaan", nil))
	}
	if err := middleware.RequireGroupAdminInOrg(c.Context(), h.sessions, claims, targetUserID); err != nil {
		if errors.Is(err, domain.ErrForbidden) {
			return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas user ini.", nil))
		}
		h.logger.Error("gagal cek otorisasi Group Admin", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memproses permintaan", nil))
	}

	if err := h.sessions.RevokeAllSessions(c.Context(), targetUserID, ""); err != nil {
		h.logger.Error("gagal force-logout user target", zap.String("target_user_id", targetUserID), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengakhiri sesi", nil))
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// ListForUser menangani GET /admin/users/:userId/sessions -- semua sesi
// milik user target, untuk dashboard admin (S1-30). Otorisasi sama seperti
// RevokeAllForUser (S3-40).
func (h *SessionHandler) ListForUser(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	targetUserID := c.Params("userId")

	if err := middleware.RequireGroupAdminInOrg(c.Context(), h.sessions, claims, targetUserID); err != nil {
		if errors.Is(err, domain.ErrForbidden) {
			return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas user ini.", nil))
		}
		h.logger.Error("gagal cek otorisasi Group Admin", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memproses permintaan", nil))
	}

	sessions, err := h.sessions.ListSessions(c.Context(), targetUserID, "")
	if err != nil {
		h.logger.Error("gagal mengambil daftar sesi user target", zap.String("target_user_id", targetUserID), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil daftar sesi", nil))
	}

	data := make([]fiber.Map, len(sessions))
	for i := range sessions {
		data[i] = sessionSummaryJSON(&sessions[i])
	}
	return c.JSON(response.Success(data))
}
