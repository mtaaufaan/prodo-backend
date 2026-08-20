package service

import (
	"context"
	"encoding/base32"
	"net/url"
	"strings"
	"testing"
)

type fakeMFARepository struct {
	savedUserID string
	savedSecret string
	enabled     bool
	err         error
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

func TestMFAService_SetupTOTP_Success(t *testing.T) {
	repo := &fakeMFARepository{}
	svc := NewMFAService(repo)

	qrBase64, err := svc.SetupTOTP(context.Background(), "user-123", "ga@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if qrBase64 == "" {
		t.Fatal("QR base64 kosong")
	}
	if repo.savedUserID != "user-123" {
		t.Errorf("savedUserID = %q, want user-123", repo.savedUserID)
	}
	if _, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(repo.savedSecret); err != nil {
		t.Errorf("secret bukan base32 valid: %v", err)
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
