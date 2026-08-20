package service

import (
	"encoding/base32"
	"testing"
	"time"
)

// TestVerifyTOTP_RFC6238Vector menggunakan test vector resmi RFC 6238
// Appendix B (SHA1, secret ASCII "12345678901234567890", T=59 detik ->
// kode 8-digit 94287082 -- kita pakai 6 digit terakhirnya: 287082).
func TestVerifyTOTP_RFC6238Vector(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	now := time.Unix(59, 0)

	if !verifyTOTP(secret, "287082", now) {
		t.Error("kode RFC 6238 test vector seharusnya valid")
	}
	if verifyTOTP(secret, "000000", now) {
		t.Error("kode salah seharusnya ditolak")
	}
}

func TestVerifyTOTP_ClockSkewTolerance(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	now := time.Unix(59, 0)
	code := hotp(mustDecodeBase32(secret), 59/totpStepSeconds)

	oneStepLater := now.Add(totpStepSeconds * time.Second)
	if !verifyTOTP(secret, code, oneStepLater) {
		t.Error("kode dari step sebelumnya harus tetap diterima (toleransi clock-skew ±1 step)")
	}

	threeStepsLater := now.Add(3 * totpStepSeconds * time.Second)
	if verifyTOTP(secret, code, threeStepsLater) {
		t.Error("kode 3 step lalu seharusnya sudah ditolak (di luar toleransi)")
	}
}

func mustDecodeBase32(s string) []byte {
	b, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// generateTestTOTPSecret dan currentTOTPCode dipakai test lain (activation_test.go)
// yang butuh secret + kode OTP yang benar-benar valid untuk saat ini.
func generateTestTOTPSecret(t *testing.T) string {
	t.Helper()
	secret, err := generateTOTPSecret()
	if err != nil {
		t.Fatalf("generateTOTPSecret: %v", err)
	}
	return secret
}

func currentTOTPCode(t *testing.T, secret string) string {
	t.Helper()
	now := time.Now()
	return hotp(mustDecodeBase32(secret), now.Unix()/totpStepSeconds)
}

