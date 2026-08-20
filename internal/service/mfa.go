package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // SHA-1 wajib dipakai untuk TOTP RFC 6238, bukan untuk hashing password
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

const (
	totpSecretBytes = 20 // 160-bit, standar RFC 4226/6238
	totpStepSeconds = 30
	totpDigits      = 6
)

// mfaRepository -- interface didefinisikan di consumer, lihat §3.9.
type mfaRepository interface {
	SaveTOTPSecret(ctx context.Context, userID, totpSecret string) error
	GetTOTPSecret(ctx context.Context, userID string) (string, error)
	EnableMFA(ctx context.Context, userID string) error
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

// VerifyAndEnable mencocokkan otpCode terhadap secret TOTP tersimpan
// (S1-07). Toleransi clock-skew ±1 step (30 detik) sesuai praktik umum TOTP.
// Kalau cocok, is_enabled diset TRUE lewat repo.
func (s *MFAService) VerifyAndEnable(ctx context.Context, userID, otpCode string) (ok bool, err error) {
	secret, err := s.repo.GetTOTPSecret(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("service.VerifyAndEnable: %w", err)
	}

	if !verifyTOTP(secret, otpCode, time.Now()) {
		return false, nil
	}

	if err := s.repo.EnableMFA(ctx, userID); err != nil {
		return false, fmt.Errorf("service.VerifyAndEnable: %w", err)
	}
	return true, nil
}

// verifyTOTP mengimplementasikan RFC 6238 (TOTP) di atas RFC 4226 (HOTP)
// langsung dengan stdlib -- algoritmanya singkat & terstandar, tidak perlu
// dependency pihak ketiga hanya untuk ini (beda dengan JWKS/QR encoding).
func verifyTOTP(base32Secret, code string, now time.Time) bool {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(base32Secret)
	if err != nil {
		return false
	}

	counter := now.Unix() / totpStepSeconds
	for _, c := range []int64{counter - 1, counter, counter + 1} {
		if hotp(key, c) == code {
			return true
		}
	}
	return false
}

// hotp menghitung satu kode HOTP (RFC 4226) untuk counter tertentu.
func hotp(key []byte, counter int64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(counter))

	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	truncated := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])

	mod := uint32(1)
	for range totpDigits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, truncated%mod)
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
