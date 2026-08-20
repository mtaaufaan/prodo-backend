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
)
