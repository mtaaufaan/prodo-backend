package service

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"net/url"

	qrcode "github.com/skip2/go-qrcode"
)

const totpSecretBytes = 20 // 160-bit, standar RFC 4226/6238

// mfaRepository -- interface didefinisikan di consumer, lihat §3.9.
type mfaRepository interface {
	SaveTOTPSecret(ctx context.Context, userID, totpSecret string) error
}

// MFAService menghasilkan dan menyimpan secret TOTP + QR code untuk setup
// MFA (US-073 AC: wajib setelah password disetel). Verifikasi OTP pertama
// (yang benar-benar mengaktifkan is_enabled=true) ada di S1-07, belum di sini.
type MFAService struct {
	repo mfaRepository
}

func NewMFAService(repo mfaRepository) *MFAService {
	return &MFAService{repo: repo}
}

// SetupTOTP membuat secret TOTP baru, menyimpannya (terenkripsi, lewat
// repo), dan mengembalikan QR code (PNG, base64) berisi otpauth:// URI
// untuk dipindai aplikasi authenticator (Google Authenticator, Authy, dst).
func (s *MFAService) SetupTOTP(ctx context.Context, userID, email string) (qrPNGBase64 string, err error) {
	secret, err := generateTOTPSecret()
	if err != nil {
		return "", fmt.Errorf("service.SetupTOTP: %w", err)
	}

	if err := s.repo.SaveTOTPSecret(ctx, userID, secret); err != nil {
		return "", fmt.Errorf("service.SetupTOTP: %w", err)
	}

	uri := buildOTPAuthURI(email, secret)
	png, err := qrcode.Encode(uri, qrcode.Medium, 256)
	if err != nil {
		return "", fmt.Errorf("service.SetupTOTP: encode QR: %w", err)
	}

	return base64.StdEncoding.EncodeToString(png), nil
}

// generateTOTPSecret menghasilkan secret acak 160-bit, di-encode base32
// (tanpa padding) -- format standar yang dibaca semua aplikasi authenticator.
func generateTOTPSecret() (string, error) {
	b := make([]byte, totpSecretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generateTOTPSecret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

// buildOTPAuthURI menyusun otpauth://totp/PRODO:{email}?secret=...&issuer=PRODO
// sesuai spek Google Authenticator Key URI Format.
func buildOTPAuthURI(email, secret string) string {
	label := url.PathEscape(fmt.Sprintf("PRODO:%s", email))
	q := url.Values{
		"secret":    {secret},
		"issuer":    {"PRODO"},
		"algorithm": {"SHA1"},
		"digits":    {"6"},
		"period":    {"30"},
	}
	return fmt.Sprintf("otpauth://totp/%s?%s", label, q.Encode())
}
