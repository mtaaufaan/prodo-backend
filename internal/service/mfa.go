package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // SHA-1 wajib dipakai untuk TOTP RFC 6238, bukan untuk hashing password
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

const (
	totpSecretBytes = 20 // 160-bit, standar RFC 4226/6238
	totpStepSeconds = 30
	totpDigits      = 6

	backupCodeCount = 10
	// Tanpa 0/O/1/I/L -- gampang tertukar kalau kode disalin manual dari
	// layar (Set Password.dc.html, langkah "Simpan kode cadangan").
	backupCodeCharset = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
)

// mfaRepository -- interface didefinisikan di consumer, lihat §3.9.
type mfaRepository interface {
	SaveTOTPSecret(ctx context.Context, userID, totpSecret string) error
	GetTOTPSecret(ctx context.Context, userID string) (string, error)
	EnableMFA(ctx context.Context, userID string) error
	GetMFAStatus(ctx context.Context, userID string) (isEnabled bool, secret string, err error)
	SaveBackupCodes(ctx context.Context, userID string, hashedCodes []string) error
	ConsumeBackupCode(ctx context.Context, userID, codeHash string) (matched bool, err error)
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

// SetupResult adalah hasil pembuatan secret TOTP baru (S1-06) -- QR untuk
// scan, dan secret mentah (base32) untuk fallback "masukkan kunci manual"
// bagi authenticator app yang tidak bisa scan QR (Set Password.dc.html).
type SetupResult struct {
	QRCodePNGBase64 string
	TOTPSecret      string
}

// SetupTOTP membuat secret TOTP baru, menyimpannya (terenkripsi, lewat
// repo), dan mengembalikan QR code (PNG, base64) berisi otpauth:// URI
// untuk dipindai aplikasi authenticator (Google Authenticator, Authy, dst).
func (s *MFAService) SetupTOTP(ctx context.Context, userID, email string) (*SetupResult, error) {
	secret, err := generateTOTPSecret()
	if err != nil {
		return nil, fmt.Errorf("service.SetupTOTP: %w", err)
	}

	if err := s.repo.SaveTOTPSecret(ctx, userID, secret); err != nil {
		return nil, fmt.Errorf("service.SetupTOTP: %w", err)
	}

	uri := buildOTPAuthURI(email, secret)
	png, err := qrcode.Encode(uri, qrcode.Medium, 256)
	if err != nil {
		return nil, fmt.Errorf("service.SetupTOTP: encode QR: %w", err)
	}

	return &SetupResult{
		QRCodePNGBase64: base64.StdEncoding.EncodeToString(png),
		TOTPSecret:      secret,
	}, nil
}

// VerifyAndEnable mencocokkan otpCode terhadap secret TOTP tersimpan
// (S1-07). Toleransi clock-skew ±1 step (30 detik) sesuai praktik umum TOTP.
// Kalau cocok, is_enabled diset TRUE dan 10 kode cadangan diterbitkan
// (ditampilkan SEKALI ke user di sini -- disimpan cuma hash-nya, lihat
// GenerateBackupCodes).
func (s *MFAService) VerifyAndEnable(ctx context.Context, userID, otpCode string) (ok bool, backupCodes []string, err error) {
	secret, err := s.repo.GetTOTPSecret(ctx, userID)
	if err != nil {
		return false, nil, fmt.Errorf("service.VerifyAndEnable: %w", err)
	}

	if !verifyTOTP(secret, otpCode, time.Now()) {
		return false, nil, nil
	}

	if err := s.repo.EnableMFA(ctx, userID); err != nil {
		return false, nil, fmt.Errorf("service.VerifyAndEnable: %w", err)
	}

	codes, err := GenerateBackupCodes()
	if err != nil {
		return false, nil, fmt.Errorf("service.VerifyAndEnable: %w", err)
	}
	hashed := make([]string, len(codes))
	for i, c := range codes {
		hashed[i] = hashBackupCode(c)
	}
	if err := s.repo.SaveBackupCodes(ctx, userID, hashed); err != nil {
		return false, nil, fmt.Errorf("service.VerifyAndEnable: %w", err)
	}

	return true, codes, nil
}

// VerifyLoginOTP memverifikasi kode MFA saat login (S1-17) -- BEDA dari
// VerifyAndEnable (S1-07): tidak pernah mengubah is_enabled, dan tidak
// menganggap "belum pernah setup MFA" sebagai error. mfaEnabled=false
// berarti user belum pernah setup MFA sama sekali -- valid tidak relevan
// (selalu false), AuthService.VerifyMFA yang memutuskan kebijakan wajib
// (GA/PA) vs opsional (member) berdasarkan mfaEnabled.
//
// code bisa berupa OTP TOTP 6 digit ATAU salah satu dari 10 kode cadangan
// format "XXXX-XXXX" (2026-08-30, menutup gap: backup_codes sudah
// diterbitkan+disimpan sejak awal tapi tidak ada jalur untuk memakainya
// saat login -- docs/API_CONTRACT.md Appendix A). Dibedakan lewat bentuk
// kode (ada strip atau tidak), BUKAN field terpisah -- pola yang sama
// dipakai GitHub/Google, satu kotak input MFA menerima kode pemulihan
// juga, tanpa endpoint/toggle baru. Kode cadangan HABIS SEKALI PAKAI:
// usedBackupCode=true berarti ConsumeBackupCode barusan menghapusnya dari
// daftar (lihat mfa_repository.go) -- caller (AuthService) memakai flag
// ini untuk mencatat audit trail tambahan.
func (s *MFAService) VerifyLoginOTP(ctx context.Context, userID, code string) (mfaEnabled, valid, usedBackupCode bool, err error) {
	enabled, secret, err := s.repo.GetMFAStatus(ctx, userID)
	if err != nil {
		return false, false, false, fmt.Errorf("service.VerifyLoginOTP: %w", err)
	}
	if !enabled {
		return false, false, false, nil
	}
	if isBackupCodeFormat(code) {
		matched, err := s.repo.ConsumeBackupCode(ctx, userID, hashBackupCode(normalizeBackupCode(code)))
		if err != nil {
			return true, false, false, fmt.Errorf("service.VerifyLoginOTP: %w", err)
		}
		return true, matched, matched, nil
	}
	return true, verifyTOTP(secret, code, time.Now()), false, nil
}

// isBackupCodeFormat -- kode cadangan SELALU mengandung satu strip di
// posisi ke-5 ("XXXX-XXXX"), OTP TOTP TIDAK PERNAH (selalu 6 digit murni).
// Cukup dan aman dipakai sebagai pembeda: kalau ternyata bukan kode
// cadangan asli, ConsumeBackupCode di atas cukup gagal cocok (0 baris),
// tidak ada celah keamanan dari false-positive deteksi bentuk ini.
func isBackupCodeFormat(code string) bool {
	return strings.Contains(code, "-")
}

// normalizeBackupCode -- huruf kode cadangan SELALU kapital saat dibuat
// (backupCodeCharset), tapi keyboard HP/autocorrect bisa mengubah jadi
// huruf kecil saat diketik ulang -- disamakan dulu sebelum hash supaya
// tidak menolak kode yang sebenarnya valid.
func normalizeBackupCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
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
		// constant-time: kode OTP 6 digit rentan brute-force kalau
		// perbandingannya bocor lewat timing (walau celahnya kecil, ini
		// operasi murah untuk dibuat aman).
		if subtle.ConstantTimeCompare([]byte(hotp(key, c)), []byte(code)) == 1 {
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

// GenerateBackupCodes membuat 10 kode cadangan sekali-pakai format
// "XXXX-XXXX" (8 karakter acak crypto/rand + hyphen di tengah) untuk
// pemulihan MFA kalau perangkat authenticator hilang. Dikembalikan sekali
// dalam bentuk plaintext -- caller wajib hash sebelum disimpan (lihat
// hashBackupCode), sama seperti password: tidak pernah didekripsi lagi,
// cuma dibandingkan saat dipakai.
func GenerateBackupCodes() ([]string, error) {
	codes := make([]string, backupCodeCount)
	for i := range codes {
		code, err := generateBackupCode()
		if err != nil {
			return nil, fmt.Errorf("GenerateBackupCodes: %w", err)
		}
		codes[i] = code
	}
	return codes, nil
}

func generateBackupCode() (string, error) {
	b := make([]byte, 8)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(backupCodeCharset))))
		if err != nil {
			return "", err
		}
		b[i] = backupCodeCharset[n.Int64()]
	}
	return string(b[:4]) + "-" + string(b[4:]), nil
}

// hashBackupCode menghitung SHA-256 dari satu kode cadangan -- disimpan di
// user_mfa_configs.backup_codes, bukan plaintext. Kode ini fungsinya seperti
// password sekali-pakai: cukup dibandingkan (constant-time) saat dipakai,
// tidak pernah perlu ditampilkan ulang, jadi hash satu-arah lebih tepat
// daripada enkripsi reversibel (beda dengan totp_secret yang memang perlu
// dibaca ulang untuk verifikasi TOTP).
func hashBackupCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
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
