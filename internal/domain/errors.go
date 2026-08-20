package domain

import "errors"

var (
	// ErrInvalidInput dikembalikan saat field wajib pada request kosong.
	ErrInvalidInput = errors.New("invalid input")

	// ErrEmailAlreadyExists dikembalikan saat email sudah terdaftar --
	// baik di tabel users maupun di Keycloak.
	ErrEmailAlreadyExists = errors.New("email already registered")
)
