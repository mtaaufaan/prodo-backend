package service

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeActivationRepository struct {
	target           *repository.ActivationTarget
	findErr          error
	markAcceptedErr  error
	markedInvitation string

	mfaTarget     *repository.MFAVerificationTarget
	findMFAErr    error
	activateErr   error
	activatedUser string
}

func (f *fakeActivationRepository) FindActivationTarget(_ context.Context, _ string) (*repository.ActivationTarget, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.target, nil
}

func (f *fakeActivationRepository) MarkInvitationAccepted(_ context.Context, invitationID, _ string) error {
	f.markedInvitation = invitationID
	return f.markAcceptedErr
}

func (f *fakeActivationRepository) FindMFAVerificationTarget(_ context.Context, _ string) (*repository.MFAVerificationTarget, error) {
	if f.findMFAErr != nil {
		return nil, f.findMFAErr
	}
	return f.mfaTarget, nil
}

func (f *fakeActivationRepository) ActivateUser(_ context.Context, userID string) error {
	f.activatedUser = userID
	return f.activateErr
}

func TestActivationService_SetPasswordAndInitMFA_Success(t *testing.T) {
	repo := &fakeActivationRepository{target: &repository.ActivationTarget{
		InvitationID: "inv-1",
		UserID:       "user-1",
		Email:        "ga@example.com",
		DisplayName:  "GA",
		KeycloakSub:  "kc-sub-1",
	}}
	kc := &fakeKeycloakClient{}
	mfa := NewMFAService(&fakeMFARepository{})
	svc := NewActivationService(repo, kc, mfa, zap.NewNop())

	result, err := svc.SetPasswordAndInitMFA(context.Background(), "raw-token", "Str0ng!Passw0rd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.QRCodePNGBase64 == "" {
		t.Error("QRCodePNGBase64 kosong")
	}
	if repo.markedInvitation != "inv-1" {
		t.Errorf("markedInvitation = %q, want inv-1", repo.markedInvitation)
	}
}

func TestActivationService_SetPasswordAndInitMFA_TokenNotFound(t *testing.T) {
	repo := &fakeActivationRepository{findErr: domain.ErrInvitationNotFound}
	svc := NewActivationService(repo, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{}), zap.NewNop())

	_, err := svc.SetPasswordAndInitMFA(context.Background(), "bad-token", "Str0ng!Passw0rd")
	if !errors.Is(err, domain.ErrInvitationNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrInvitationNotFound", err)
	}
}

func TestActivationService_VerifyMFAAndActivate_Success(t *testing.T) {
	secret := generateTestTOTPSecret(t)
	repo := &fakeActivationRepository{mfaTarget: &repository.MFAVerificationTarget{
		UserID:      "user-1",
		KeycloakSub: "kc-sub-1",
	}}
	mfaRepo := &fakeMFARepository{savedSecret: secret}
	svc := NewActivationService(repo, &fakeKeycloakClient{}, NewMFAService(mfaRepo), zap.NewNop())

	code := currentTOTPCode(t, secret)
	err := svc.VerifyMFAAndActivate(context.Background(), "raw-token", code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.activatedUser != "user-1" {
		t.Errorf("activatedUser = %q, want user-1", repo.activatedUser)
	}
	if !mfaRepo.enabled {
		t.Error("MFA seharusnya enabled setelah verifikasi berhasil")
	}
}

func TestActivationService_VerifyMFAAndActivate_WrongOTP(t *testing.T) {
	secret := generateTestTOTPSecret(t)
	repo := &fakeActivationRepository{mfaTarget: &repository.MFAVerificationTarget{UserID: "user-1", KeycloakSub: "kc-sub-1"}}
	mfaRepo := &fakeMFARepository{savedSecret: secret}
	svc := NewActivationService(repo, &fakeKeycloakClient{}, NewMFAService(mfaRepo), zap.NewNop())

	err := svc.VerifyMFAAndActivate(context.Background(), "raw-token", "000000")
	if !errors.Is(err, domain.ErrInvalidOTP) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidOTP", err)
	}
	if repo.activatedUser != "" {
		t.Error("akun tidak boleh diaktifkan kalau OTP salah")
	}
	if mfaRepo.enabled {
		t.Error("MFA tidak boleh enabled kalau OTP salah")
	}
}

func TestActivationService_VerifyMFAAndActivate_TokenNotFound(t *testing.T) {
	repo := &fakeActivationRepository{findMFAErr: domain.ErrInvitationNotFound}
	svc := NewActivationService(repo, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{}), zap.NewNop())

	err := svc.VerifyMFAAndActivate(context.Background(), "bad-token", "123456")
	if !errors.Is(err, domain.ErrInvitationNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrInvitationNotFound", err)
	}
}
