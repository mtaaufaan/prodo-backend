package handler

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// SessionHandler -- S1-29/30, US-004. Hanya endpoint GET (list) hari ini;
// DELETE/force-logout (S1-33/34/35) menyusul H10 meski
// SessionService.RevokeSession/RevokeAllSessions (S1-32) sudah ada.
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

// ListForUser menangani GET /admin/users/:userId/sessions -- semua sesi
// milik user target, untuk dashboard admin (S1-30). ⚠️ Gap diketahui:
// sprint_backlog.md menulis "hanya role group_admin", tapi API_CONTRACT.md
// tidak pernah mendokumentasikan endpoint ini secara eksplisit (cuma
// revoke-all yang eksplisit mengizinkan Platform Admin ATAU Group Admin
// dalam org yang sama). Endpoint ini dibatasi Platform-Admin-only untuk
// sekarang lewat middleware.RequirePlatformAdmin() -- akses Group Admin
// (dibatasi ke member org mereka sendiri) baru bisa diimplementasikan
// dengan aman setelah data organisasi/keanggotaan ada (Epic 2, belum
// dibangun sama sekali di S1). Membiarkan GA lolos tanpa pengecekan org
// akan jadi lubang keamanan (bisa lihat sesi user org lain), jadi
// sengaja ditolak dulu daripada dibuka tanpa scoping yang benar.
func (h *SessionHandler) ListForUser(c *fiber.Ctx) error {
	targetUserID := c.Params("userId")

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
