package service

import (
	"context"
	"encoding/base32"
	"net/url"
	"strings"
	"testing"
)

type fakeMFARepository struct {
	savedUserID      string
	savedSecret      string
	enabled          bool
	err              error
	savedBackupCodes []string

	consumeBackupCodeResult bool
	consumeBackupCodeErr    error
	consumedCodeHash        string
}

func (f *fakeMFARepository) SaveTOTPSecret(_ context.Context, userID, secret string) error {
	f.savedUserID = userID
	f.savedSecret = secret
	return f.err
}

func (f *fakeMFARepository) GetTOTPSecret(_ context.Context, _ string) (string, error) {
	return f.savedSecret, f.err
}

func (f *fakeMFARepository) EnableMFA(_ context.Context, _ string) error {
	f.enabled = true
	return f.err
}

func (f *fakeMFARepository) GetMFAStatus(_ context.Context, _ string) (isEnabled bool, secret string, err error) {
	return f.enabled, f.savedSecret, f.err
}

func (f *fakeMFARepository) SaveBackupCodes(_ context.Context, _ string, hashedCodes []string) error {
	f.savedBackupCodes = hashedCodes
	return f.err
}

func (f *fakeMFARepository) ConsumeBackupCode(_ context.Context, _, codeHash string) (bool, error) {
	f.consumedCodeHash = codeHash
	return f.consumeBackupCodeResult, f.consumeBackupCodeErr
}

func TestMFAService_SetupTOTP_Success(t *testing.T) {
	repo := &fakeMFARepository{}
	svc := NewMFAService(repo)

	result, err := svc.SetupTOTP(context.Background(), "user-123", "ga@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.QRCodePNGBase64 == "" {
		t.Fatal("QR base64 kosong")
	}
	if result.TOTPSecret == "" {
		t.Fatal("TOTPSecret kosong")
	}
	if repo.savedUserID != "user-123" {
		t.Errorf("savedUserID = %q, want user-123", repo.savedUserID)
	}
	if _, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(repo.savedSecret); err != nil {
		t.Errorf("secret bukan base32 valid: %v", err)
	}
	if result.TOTPSecret != repo.savedSecret {
		t.Errorf("TOTPSecret = %q, want sama dengan yang disimpan (%q)", result.TOTPSecret, repo.savedSecret)
	}
}

func TestMFAService_VerifyAndEnable_GeneratesBackupCodes(t *testing.T) {
	secret := generateTestTOTPSecret(t)
	repo := &fakeMFARepository{savedSecret: secret}
	svc := NewMFAService(repo)

	code := currentTOTPCode(t, secret)
	ok, backupCodes, err := svc.VerifyAndEnable(context.Background(), "user-1", code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("harusnya ok=true untuk OTP valid")
	}
	if len(backupCodes) != backupCodeCount {
		t.Fatalf("len(backupCodes) = %d, want %d", len(backupCodes), backupCodeCount)
	}
	seen := map[string]bool{}
	for _, c := range backupCodes {
		if len(c) != 9 || c[4] != '-' {
			t.Errorf("format kode cadangan salah: %q, want XXXX-XXXX", c)
		}
		if seen[c] {
			t.Errorf("kode cadangan duplikat: %q", c)
		}
		seen[c] = true
	}
	if len(repo.savedBackupCodes) != backupCodeCount {
		t.Fatalf("repo.savedBackupCodes len = %d, want %d (harus disimpan ter-hash)", len(repo.savedBackupCodes), backupCodeCount)
	}
	for i, hashed := range repo.savedBackupCodes {
		if hashed == backupCodes[i] {
			t.Error("kode cadangan tersimpan plaintext, harusnya sudah di-hash")
		}
	}
}

func TestMFAService_VerifyLoginOTP_TOTPPathUnaffected(t *testing.T) {
	secret := generateTestTOTPSecret(t)
	repo := &fakeMFARepository{enabled: true, savedSecret: secret}
	svc := NewMFAService(repo)

	enabled, valid, usedBackupCode, err := svc.VerifyLoginOTP(context.Background(), "user-1", currentTOTPCode(t, secret))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !enabled || !valid {
		t.Fatalf("enabled=%v valid=%v, want true/true untuk OTP TOTP valid", enabled, valid)
	}
	if usedBackupCode {
		t.Error("usedBackupCode harus false untuk kode TOTP biasa (tidak ada strip)")
	}
	if repo.consumedCodeHash != "" {
		t.Error("ConsumeBackupCode tidak boleh dipanggil untuk kode TOTP")
	}
}

func TestMFAService_VerifyLoginOTP_BackupCodeMatched(t *testing.T) {
	repo := &fakeMFARepository{enabled: true, consumeBackupCodeResult: true}
	svc := NewMFAService(repo)

	enabled, valid, usedBackupCode, err := svc.VerifyLoginOTP(context.Background(), "user-1", "abcd-1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !enabled || !valid || !usedBackupCode {
		t.Fatalf("enabled=%v valid=%v usedBackupCode=%v, want true/true/true untuk kode cadangan cocok", enabled, valid, usedBackupCode)
	}
	// huruf kecil harus dinormalisasi ke kapital sebelum di-hash -- kalau
	// tidak, hash yang dicocokkan ke DB tidak akan pernah ketemu (kode
	// cadangan disimpan hash dari bentuk KAPITAL, lihat generateBackupCode).
	wantHash := hashBackupCode("ABCD-1234")
	if repo.consumedCodeHash != wantHash {
		t.Errorf("hash yang dicocokkan = %q, want %q (huruf kecil harus dinormalisasi kapital)", repo.consumedCodeHash, wantHash)
	}
}

func TestMFAService_VerifyLoginOTP_BackupCodeWrong(t *testing.T) {
	repo := &fakeMFARepository{enabled: true, consumeBackupCodeResult: false}
	svc := NewMFAService(repo)

	_, valid, usedBackupCode, err := svc.VerifyLoginOTP(context.Background(), "user-1", "wxyz-9999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if valid || usedBackupCode {
		t.Fatalf("valid=%v usedBackupCode=%v, want false/false untuk kode cadangan yang tidak cocok/sudah dipakai", valid, usedBackupCode)
	}
}

func TestMFAService_VerifyLoginOTP_NotEnabled(t *testing.T) {
	repo := &fakeMFARepository{enabled: false}
	svc := NewMFAService(repo)

	enabled, valid, usedBackupCode, err := svc.VerifyLoginOTP(context.Background(), "user-1", "abcd-1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enabled || valid || usedBackupCode {
		t.Fatalf("enabled=%v valid=%v usedBackupCode=%v, want false/false/false kalau MFA belum pernah diaktifkan", enabled, valid, usedBackupCode)
	}
	if repo.consumedCodeHash != "" {
		t.Error("ConsumeBackupCode tidak boleh dipanggil kalau MFA belum aktif")
	}
}

func TestBuildOTPAuthURI(t *testing.T) {
	uri := buildOTPAuthURI("ga@example.com", "ABCDEFGH")
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Errorf("URI = %q, harus diawali otpauth://totp/", uri)
	}

	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("URI tidak valid: %v", err)
	}
	q := parsed.Query()
	if q.Get("secret") != "ABCDEFGH" {
		t.Errorf("secret = %q, want ABCDEFGH", q.Get("secret"))
	}
	if q.Get("issuer") != "PRODO" {
		t.Errorf("issuer = %q, want PRODO", q.Get("issuer"))
	}
	if !strings.Contains(parsed.Path, "ga@example.com") {
		t.Errorf("path %q harus mengandung email", parsed.Path)
	}
}
