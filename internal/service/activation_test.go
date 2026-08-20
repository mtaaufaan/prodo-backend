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
}

func (f *fakeActivationRepository) FindActivationTarget(_ context.Context, _ string) (*repository.ActivationTarget, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.target, nil
}

func (f *fakeActivationRepository) MarkInvitationAccepted(_ context.Context, invitationID string) error {
	f.markedInvitation = invitationID
	return f.markAcceptedErr
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
