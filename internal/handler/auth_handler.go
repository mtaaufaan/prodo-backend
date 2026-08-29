package handler

import (
	"errors"
	"strings"
	"unicode"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
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
		"message":      "Password berhasil disetel. Lanjutkan setup MFA.",
		"totp_qr_url":  "data:image/png;base64," + result.QRCodePNGBase64,
		"totp_secret":  result.TOTPSecret,
		"email":        result.Email,
		"display_name": result.DisplayName,
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

	result, err := h.activation.VerifyMFAAndActivate(c.Context(), req.Token, req.OTPCode)
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
		"message":      "Akun berhasil diaktifkan.",
		"mfa_enabled":  true,
		"backup_codes": result.BackupCodes,
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

	result, challenge, err := h.auth.Login(c.Context(), req.Email, req.Password, req.MFACode, c.Get("User-Agent"), c.IP())
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidCredentials):
			return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Email atau password tidak valid.", nil))
		case errors.Is(err, domain.ErrAccountInactive):
			return c.Status(fiber.StatusForbidden).JSON(response.Error("ACCOUNT_INACTIVE",
				"Akun belum aktif. Periksa email undangan Anda atau hubungi administrator.", nil))
		case errors.Is(err, domain.ErrAccountSuspended):
			// S4P-02, US-067.
			return c.Status(fiber.StatusForbidden).JSON(response.Error("ACCOUNT_SUSPENDED",
				"Akun ini telah dinonaktifkan. Hubungi Platform Admin.", nil))
		case errors.Is(err, domain.ErrInvalidOTP):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_OTP", "Kode OTP tidak valid.", nil))
		case errors.Is(err, domain.ErrIPNotAllowed):
			// S4P-17 (implementation_gaps.md IG-20): dicek SEBELUM MFA --
			// PA yang login dari IP terlarang tidak perlu tahu status MFA-nya.
			return c.Status(fiber.StatusForbidden).JSON(response.Error("IP_NOT_ALLOWED",
				"Login tidak diizinkan dari alamat IP ini.", nil))
		case errors.Is(err, domain.ErrMFASetupRequired):
			// S4P-14/19 (implementation_gaps.md IG-20): BUKAN error bagi FE
			// -- 200 dengan payload setup, PlatformLoginPage lanjut ke
			// layar QR lalu POST /auth/platform/mfa-setup/verify.
			return c.JSON(response.Success(fiber.Map{
				"mfa_setup_required": true,
				"totp_qr_url":        "data:image/png;base64," + challenge.QRCodePNGBase64,
				"totp_secret":        challenge.TOTPSecret,
				"email":              challenge.Email,
			}))
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

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
	JTI          string `json:"jti"`
}

// Refresh menangani POST /auth/refresh (ditambahkan 2026-08-29, menutup
// gap: tidak ada jalur refresh sama sekali sebelum ini, SEMUA sesi
// otomatis logout begitu access_token 5 menit kedaluwarsa). BUKAN di
// belakang middleware.JWTAuth -- access_token lama SEHARUSNYA sudah
// kedaluwarsa saat endpoint ini dipanggil, jti-nya dikirim klien di body
// (didekode klien sendiri dari access_token lama, tanpa verifikasi --
// aman karena backend cuma memakainya sebagai kunci lookup, bukan klaim
// tepercaya, lihat komentar RenewSessionJTI).
func (h *AuthHandler) Refresh(c *fiber.Ctx) error {
	var req refreshRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	if req.RefreshToken == "" || req.JTI == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "refresh_token dan jti wajib diisi", nil))
	}

	result, err := h.auth.RefreshAccessToken(c.Context(), req.JTI, req.RefreshToken)
	if err != nil {
		switch {
		case errors.Is(err, keycloak.ErrInvalidGrant):
			return c.Status(fiber.StatusUnauthorized).JSON(response.Error("TOKEN_EXPIRED", "Refresh token sudah tidak valid.", nil))
		case errors.Is(err, domain.ErrSessionExpired):
			return c.Status(fiber.StatusUnauthorized).JSON(response.Error("TOKEN_EXPIRED", "Sesi sudah berakhir atau tidak aktif.", nil))
		default:
			h.logger.Error("gagal memperbarui access token", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memperbarui sesi", nil))
		}
	}

	return c.JSON(response.Success(fiber.Map{
		"access_token":  result.AccessToken,
		"refresh_token": result.RefreshToken,
		"token_type":    "Bearer",
		"expires_in":    result.ExpiresIn,
	}))
}

type platformMFASetupVerifyRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	OTPCode  string `json:"otp_code"`
}

// CompletePlatformAdminMFASetup menangani POST /auth/platform/mfa-setup/verify
// (S4P-14/19, `[PUBLIC]`) -- langkah kedua alur login pertama Platform
// Admin: verifikasi OTP dari QR yang diterbitkan POST /auth/login
// (respons `mfa_setup_required`), aktifkan MFA, dan langsung terbitkan
// token (tidak perlu login ulang).
func (h *AuthHandler) CompletePlatformAdminMFASetup(c *fiber.Ctx) error {
	var req platformMFASetupVerifyRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "email dan password wajib diisi", nil))
	}
	if !isSixDigits(req.OTPCode) {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "otp_code harus 6 digit angka", nil))
	}

	result, err := h.auth.CompletePlatformAdminMFASetup(c.Context(), req.Email, req.Password, req.OTPCode, c.Get("User-Agent"), c.IP())
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidCredentials):
			return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Email atau password tidak valid.", nil))
		case errors.Is(err, domain.ErrAccountInactive):
			return c.Status(fiber.StatusForbidden).JSON(response.Error("ACCOUNT_INACTIVE", "Akun belum aktif.", nil))
		case errors.Is(err, domain.ErrAccountSuspended):
			return c.Status(fiber.StatusForbidden).JSON(response.Error("ACCOUNT_SUSPENDED",
				"Akun ini telah dinonaktifkan. Hubungi Platform Admin.", nil))
		case errors.Is(err, domain.ErrInvalidOTP):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_OTP", "Kode OTP tidak valid.", nil))
		case errors.Is(err, domain.ErrIPNotAllowed):
			return c.Status(fiber.StatusForbidden).JSON(response.Error("IP_NOT_ALLOWED",
				"Login tidak diizinkan dari alamat IP ini.", nil))
		default:
			h.logger.Error("gagal menyelesaikan setup MFA Platform Admin", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyelesaikan setup MFA", nil))
		}
	}

	return c.JSON(response.Success(fiber.Map{
		"access_token":  result.AccessToken,
		"refresh_token": result.RefreshToken,
		"token_type":    result.TokenType,
		"expires_in":    result.ExpiresIn,
		"backup_codes":  result.BackupCodes,
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
