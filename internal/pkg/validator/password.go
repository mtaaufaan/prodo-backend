// Package validator holds input validation helpers.
package validator

import "unicode"

const minPasswordLength = 12

// ValidatePasswordComplexity mengecek syarat kompleksitas password (US-073
// AC): minimal 12 karakter, mengandung huruf besar, huruf kecil, angka, dan
// karakter spesial. Mengembalikan pesan yang bisa langsung ditampilkan ke
// user, atau string kosong kalau valid.
func ValidatePasswordComplexity(password string) string {
	if len(password) < minPasswordLength {
		return "Password minimal 12 karakter"
	}

	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSpecial = true
		}
	}

	switch {
	case !hasUpper:
		return "Password harus mengandung huruf besar"
	case !hasLower:
		return "Password harus mengandung huruf kecil"
	case !hasDigit:
		return "Password harus mengandung angka"
	case !hasSpecial:
		return "Password harus mengandung karakter spesial"
	}
	return ""
}
