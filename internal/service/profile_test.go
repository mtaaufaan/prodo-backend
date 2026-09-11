package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeProfileRepository struct {
	providerSub string

	lastSecurityAction string
	lastSecurityUserID string
}

func (f *fakeProfileRepository) GetProfile(_ context.Context, _ string) (*repository.ProfileRecord, error) {
	return &repository.ProfileRecord{}, nil
}

func (f *fakeProfileRepository) UpdateProfile(_ context.Context, _, _, _, _, _, _ string) (*repository.ProfileRecord, error) {
	return &repository.ProfileRecord{}, nil
}

func (f *fakeProfileRepository) FindProviderSubByUserID(_ context.Context, _ string) (string, error) {
	return f.providerSub, nil
}

func (f *fakeProfileRepository) LogAccountSecurityAction(_ context.Context, userID, _, action string) error {
	f.lastSecurityUserID = userID
	f.lastSecurityAction = action
	return nil
}

func newTestProfileService(repo *fakeProfileRepository, oidc *fakeOIDCClient, kc *fakeKeycloakClient, mfaRepo *fakeMFARepository) *ProfileService {
	mfaSvc := NewMFAService(mfaRepo)
	sessions := newTestSessionService()
	notif := repository.NewNotificationPreferenceRepository(nil)
	return NewProfileService(repo, notif, oidc, kc, mfaSvc, sessions)
}

// TestProfileService_ChangePassword_WrongCurrentPassword -- current password
// yang ditolak Keycloak (keycloak.ErrInvalidGrant) harus jadi
// domain.ErrInvalidCredentials, dan TIDAK BOLEH lanjut ke SetPassword sama
// sekali (kalau lanjut, password bisa diganti tanpa verifikasi yang benar).
func TestProfileService_ChangePassword_WrongCurrentPassword(t *testing.T) {
	repo := &fakeProfileRepository{providerSub: "kc-sub-1"}
	oidc := &fakeOIDCClient{err: keycloak.ErrInvalidGrant}
	kc := &fakeKeycloakClient{}
	svc := newTestProfileService(repo, oidc, kc, &fakeMFARepository{})

	_, err := svc.ChangePassword(context.Background(), "user-1", "group_admin", "ga@acme.co", "current-jti", "wrong-old-pw", "NewStr0ng!Pass")
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected domain.ErrInvalidCredentials, got %v", err)
	}
	if repo.lastSecurityAction != "" {
		t.Fatal("audit tidak boleh ditulis kalau password lama salah")
	}
}

// TestProfileService_ChangePassword_Success -- password lama benar ->
// SetPassword dipanggil ke Keycloak user ID yang benar (BUKAN PRODO user
// ID), dan audit 'account.password_changed' tercatat.
func TestProfileService_ChangePassword_Success(t *testing.T) {
	repo := &fakeProfileRepository{providerSub: "kc-sub-1"}
	oidc := &fakeOIDCClient{}
	kc := &fakeKeycloakClient{}
	svc := newTestProfileService(repo, oidc, kc, &fakeMFARepository{})

	if _, err := svc.ChangePassword(context.Background(), "user-1", "group_admin", "ga@acme.co", "current-jti", "old-pw", "NewStr0ng!Pass"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.lastSecurityAction != "account.password_changed" {
		t.Fatalf("expected audit action account.password_changed, got %q", repo.lastSecurityAction)
	}
	if repo.lastSecurityUserID != "user-1" {
		t.Fatalf("expected audit for user-1, got %q", repo.lastSecurityUserID)
	}
}

// TestProfileService_RegenerateBackupCodes -- 10 kode baru diterbitkan dan
// disimpan (hashed) via MFARepository, TANPA mengubah secret TOTP (beda dari
// ResetMFADevice).
func TestProfileService_RegenerateBackupCodes(t *testing.T) {
	repo := &fakeProfileRepository{}
	mfaRepo := &fakeMFARepository{}
	svc := newTestProfileService(repo, &fakeOIDCClient{}, &fakeKeycloakClient{}, mfaRepo)

	codes, err := svc.RegenerateBackupCodes(context.Background(), "user-1", "group_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(codes) != 10 {
		t.Fatalf("expected 10 backup codes, got %d", len(codes))
	}
	if len(mfaRepo.savedBackupCodes) != 10 {
		t.Fatalf("expected 10 hashed codes saved, got %d", len(mfaRepo.savedBackupCodes))
	}
	if repo.lastSecurityAction != "account.mfa_backup_codes_regenerated" {
		t.Fatalf("expected audit action account.mfa_backup_codes_regenerated, got %q", repo.lastSecurityAction)
	}
}
