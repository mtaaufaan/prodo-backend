package validator

import "net/mail"

// IsValidEmail memvalidasi format email (RFC 5322, dicek stdlib net/mail --
// superset RFC 5321 yang cukup untuk validasi format, lihat
// docs/DATABASE_SCHEMA.md §5.1 "divalidasi format RFC 5321").
func IsValidEmail(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}
