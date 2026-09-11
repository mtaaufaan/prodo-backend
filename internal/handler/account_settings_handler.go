// Package handler -- AccountSettingsHandler (GA Pengaturan Akun, Track S4G,
// desain "GA Pengaturan Akun.dc.html"): self-service profil, password, MFA,
// dan preferensi notifikasi milik akun yang sedang login. BEDA dari
// PlatformAdminAccountsHandler/GroupAdminHandler (admin mengelola akun
// ORANG LAIN) -- seluruh endpoint di sini beroperasi atas diri sendiri,
// userID selalu dari JWT claims, tidak pernah dari parameter route.
package handler

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/validator"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

type AccountSettingsHandler struct {
	accounts *service.AccountService
	profile  *service.ProfileService
	logger   *zap.Logger
}

func NewAccountSettingsHandler(accounts *service.AccountService, profile *service.ProfileService, logger *zap.Logger) *AccountSettingsHandler {
	return &AccountSettingsHandler{accounts: accounts, profile: profile, logger: logger}
}

func profileJSON(p *repository.ProfileRecord) fiber.Map {
	return fiber.Map{
		"id":            p.ID,
		"email":         p.Email,
		"display_name":  p.DisplayName,
		"title":         p.Title,
		"phone":         p.Phone,
		"avatar_url":    p.AvatarURL,
		"platform_role": p.PlatformRole,
		"locale":        p.Locale,
		"mfa_enabled":   p.MFAEnabled,
		"last_login_at": p.LastLoginAt,
		"created_at":    p.CreatedAt,
	}
}

// resolveSelf -- helper dipakai SEMUA method di handler ini: claims JWT +
// userID PRODO dari klaim tsb. Pola identik SessionHandler.
func (h *AccountSettingsHandler) resolveSelf(c *fiber.Ctx) (claims *middleware.Claims, userID string, done bool) {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		_ = c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
		return nil, "", true
	}
	userID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("JWT valid tapi user tidak ditemukan di tabel users", zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		_ = c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
		return nil, "", true
	}
	return claims, userID, false
}

// GetProfile menangani GET /users/me.
func (h *AccountSettingsHandler) GetProfile(c *fiber.Ctx) error {
	_, userID, done := h.resolveSelf(c)
	if done {
		return nil
	}
	p, err := h.profile.GetProfile(c.Context(), userID)
	if err != nil {
		h.logger.Error("gagal mengambil profil", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil profil", nil))
	}
	return c.JSON(response.Success(profileJSON(p)))
}

type updateProfileRequest struct {
	DisplayName string `json:"display_name"`
	Title       string `json:"title"`
	Phone       string `json:"phone"`
	Locale      string `json:"locale"`
}

// UpdateProfile menangani PATCH /users/me (tab Profil, "SIMPAN PERUBAHAN").
func (h *AccountSettingsHandler) UpdateProfile(c *fiber.Ctx) error {
	claims, userID, done := h.resolveSelf(c)
	if done {
		return nil
	}
	var req updateProfileRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if len(req.DisplayName) < 2 {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Nama tampil minimal 2 karakter",
			[]response.FieldError{{Field: "display_name", Message: "minimal 2 karakter"}}))
	}
	if req.Locale != "id" && req.Locale != "en" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "locale harus 'id' atau 'en'",
			[]response.FieldError{{Field: "locale", Message: "harus 'id' atau 'en'"}}))
	}

	p, err := h.profile.UpdateProfile(c.Context(), userID, claims.PlatformRole, req.DisplayName, req.Title, req.Phone, req.Locale)
	if err != nil {
		h.logger.Error("gagal memperbarui profil", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memperbarui profil", nil))
	}
	return c.JSON(response.Success(profileJSON(p)))
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword menangani POST /auth/password/change (tab Keamanan, "GANTI
// PASSWORD").
func (h *AccountSettingsHandler) ChangePassword(c *fiber.Ctx) error {
	claims, userID, done := h.resolveSelf(c)
	if done {
		return nil
	}
	var req changePasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	if req.CurrentPassword == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Password saat ini wajib diisi",
			[]response.FieldError{{Field: "current_password", Message: "wajib diisi"}}))
	}
	if msg := validator.ValidatePasswordComplexity(req.NewPassword); msg != "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", msg,
			[]response.FieldError{{Field: "new_password", Message: msg}}))
	}
	if req.NewPassword == req.CurrentPassword {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Password baru tidak boleh sama dengan password saat ini",
			[]response.FieldError{{Field: "new_password", Message: "tidak boleh sama dengan password saat ini"}}))
	}

	revoked, err := h.profile.ChangePassword(c.Context(), userID, claims.PlatformRole, claims.Email, claims.ID, req.CurrentPassword, req.NewPassword)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidCredentials) {
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_CURRENT_PASSWORD", "Password saat ini tidak sesuai.", nil))
		}
		h.logger.Error("gagal mengganti password", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengganti password", nil))
	}
	return c.JSON(response.Success(fiber.Map{
		"message":                "Password berhasil diganti.",
		"revoked_other_sessions": revoked,
	}))
}

// SetupSelfMFA menangani POST /auth/mfa/setup dalam mode self-service saja
// (JWT wajib) -- "PINDAHKAN KE PERANGKAT BARU", langkah 1: terbitkan secret
// TOTP baru. Varian mfa_setup_token pra-login (member self-signup, belum
// dibangun -- lihat komentar AuthHandler.Login soal domain.ErrMFARequired)
// TIDAK ditangani di sini.
func (h *AccountSettingsHandler) SetupSelfMFA(c *fiber.Ctx) error {
	claims, userID, done := h.resolveSelf(c)
	if done {
		return nil
	}
	setup, err := h.profile.ResetMFADevice(c.Context(), userID, claims.Email)
	if err != nil {
		h.logger.Error("gagal memulai reset MFA", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memulai setup MFA", nil))
	}
	return c.JSON(response.Success(fiber.Map{
		"totp_qr_url": "data:image/png;base64," + setup.QRCodePNGBase64,
		"totp_secret": setup.TOTPSecret,
	}))
}

type verifySelfMFARequest struct {
	OTPCode string `json:"otp_code"`
}

// VerifySelfMFA menangani POST /auth/mfa/verify self-service -- "PINDAHKAN
// KE PERANGKAT BARU", langkah 2: konfirmasi OTP dari perangkat baru.
func (h *AccountSettingsHandler) VerifySelfMFA(c *fiber.Ctx) error {
	claims, userID, done := h.resolveSelf(c)
	if done {
		return nil
	}
	var req verifySelfMFARequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	if !isSixDigits(req.OTPCode) {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "otp_code harus 6 digit angka", nil))
	}

	codes, err := h.profile.ConfirmMFAReset(c.Context(), userID, claims.PlatformRole, req.OTPCode)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidOTP) {
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_OTP", "Kode OTP tidak valid atau sudah kedaluwarsa.", nil))
		}
		h.logger.Error("gagal verifikasi reset MFA", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal verifikasi MFA", nil))
	}
	return c.JSON(response.Success(fiber.Map{
		"mfa_enabled":  true,
		"backup_codes": codes,
	}))
}

// RegenerateBackupCodes menangani POST /auth/mfa/backup-codes/regenerate --
// "BUAT ULANG KODE PEMULIHAN", tidak mengganti secret TOTP.
func (h *AccountSettingsHandler) RegenerateBackupCodes(c *fiber.Ctx) error {
	claims, userID, done := h.resolveSelf(c)
	if done {
		return nil
	}
	codes, err := h.profile.RegenerateBackupCodes(c.Context(), userID, claims.PlatformRole)
	if err != nil {
		h.logger.Error("gagal membuat ulang kode pemulihan", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal membuat ulang kode pemulihan", nil))
	}
	return c.JSON(response.Success(fiber.Map{"backup_codes": codes}))
}

// ListNotificationPreferences menangani GET /users/me/notification-preferences.
func (h *AccountSettingsHandler) ListNotificationPreferences(c *fiber.Ctx) error {
	_, userID, done := h.resolveSelf(c)
	if done {
		return nil
	}
	prefs, err := h.profile.ListNotificationPreferences(c.Context(), userID)
	if err != nil {
		h.logger.Error("gagal mengambil preferensi notifikasi", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil preferensi notifikasi", nil))
	}
	data := make([]fiber.Map, len(prefs))
	for i, p := range prefs {
		data[i] = fiber.Map{"event_type": p.EventType, "in_app": p.InApp, "push": p.Push, "email": p.Email}
	}
	return c.JSON(response.Success(data))
}

type updateNotificationPreferenceRequest struct {
	EventType string `json:"event_type"`
	Push      bool   `json:"push"`
	Email     bool   `json:"email"`
}

// UpdateNotificationPreference menangani PATCH /users/me/notification-preferences.
func (h *AccountSettingsHandler) UpdateNotificationPreference(c *fiber.Ctx) error {
	claims, userID, done := h.resolveSelf(c)
	if done {
		return nil
	}
	var req updateNotificationPreferenceRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	valid := false
	for _, ev := range repository.GroupAdminNotificationEvents {
		if ev.Key == req.EventType {
			valid = true
			break
		}
	}
	if !valid {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "event_type tidak dikenal", nil))
	}

	if err := h.profile.UpdateNotificationPreference(c.Context(), userID, claims.PlatformRole, req.EventType, req.Push, req.Email); err != nil {
		h.logger.Error("gagal memperbarui preferensi notifikasi", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memperbarui preferensi notifikasi", nil))
	}
	return c.JSON(response.Success(fiber.Map{"event_type": req.EventType, "in_app": true, "push": req.Push, "email": req.Email}))
}
