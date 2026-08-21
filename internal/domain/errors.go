package domain

import "errors"

var (
	// ErrInvalidInput dikembalikan saat field wajib pada request kosong.
	ErrInvalidInput = errors.New("invalid input")

	// ErrEmailAlreadyExists dikembalikan saat email sudah terdaftar --
	// baik di tabel users maupun di Keycloak.
	ErrEmailAlreadyExists = errors.New("email already registered")

	// ErrInvitationNotFound dikembalikan saat token aktivasi tidak
	// ditemukan, sudah dipakai, atau sudah kedaluwarsa (US-073 AC).
	ErrInvitationNotFound = errors.New("activation token not found, used, or expired")

	// ErrUserNotFound dikembalikan saat lookup user (misal by Keycloak
	// provider_sub) tidak menemukan baris.
	ErrUserNotFound = errors.New("user not found")

	// ErrWeakPassword dikembalikan saat password tidak memenuhi syarat
	// kompleksitas (US-073 AC: 12+ karakter, huruf besar, kecil, angka,
	// karakter spesial).
	ErrWeakPassword = errors.New("password does not meet complexity requirements")

	// ErrInvalidOTP dikembalikan saat kode OTP TOTP tidak cocok (S1-07).
	ErrInvalidOTP = errors.New("invalid otp code")

	// ErrInvalidCredentials dikembalikan saat email tidak terdaftar atau
	// password salah (S1-14). Kedua kasus sengaja disamakan -- tidak
	// membocorkan apakah suatu email terdaftar di PRODO (user enumeration).
	ErrInvalidCredentials = errors.New("invalid email or password")

	// ErrAccountInactive dikembalikan saat users.is_active masih FALSE
	// (belum menyelesaikan alur aktivasi US-073) (S1-14).
	ErrAccountInactive = errors.New("account is not active")

	// ErrMFARequired dikembalikan saat Group Admin login tapi belum punya
	// MFA aktif (S1-17) -- seharusnya tidak pernah terjadi lewat alur
	// onboarding normal (S1-06/07 mewajibkan setup MFA sebelum is_active
	// TRUE), jadi ini murni pengaman untuk state yang tidak konsisten.
	ErrMFARequired = errors.New("mfa is required for this account but not configured")
)
