package service

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
)

func TestAccountService_CreatePlatformAdmin_Success(t *testing.T) {
	repo := &fakeAccountRepository{returnID: "pa-123"}
	kc := &fakeKeycloakClient{userID: "kc-sub-pa"}
	svc := NewAccountService(repo, kc, zap.NewNop())

	result, err := svc.CreatePlatformAdmin(context.Background(), "pa@example.com", "New PA", "inviter-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.UserID != "pa-123" || result.ActivationToken == "" {
		t.Errorf("result = %+v, want UserID=pa-123 and non-empty token", result)
	}
}

func TestAccountService_CreatePlatformAdmin_InvalidInput(t *testing.T) {
	svc := NewAccountService(&fakeAccountRepository{}, &fakeKeycloakClient{}, zap.NewNop())
	if _, err := svc.CreatePlatformAdmin(context.Background(), "", "", ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want ErrInvalidInput", err)
	}
}

func TestAccountService_CreatePlatformAdmin_EmailAlreadyExists(t *testing.T) {
	kc := &fakeKeycloakClient{err: keycloak.ErrUserAlreadyExists}
	svc := NewAccountService(&fakeAccountRepository{}, kc, zap.NewNop())
	_, err := svc.CreatePlatformAdmin(context.Background(), "dup@example.com", "Dup", "inviter-1")
	if !errors.Is(err, domain.ErrEmailAlreadyExists) {
		t.Errorf("err = %v, want ErrEmailAlreadyExists", err)
	}
}

func TestAccountService_DeactivatePlatformAdmin_RejectsSelf(t *testing.T) {
	svc := NewAccountService(&fakeAccountRepository{}, &fakeKeycloakClient{}, zap.NewNop())
	if err := svc.DeactivatePlatformAdmin(context.Background(), "pa-1", "pa-1"); !errors.Is(err, domain.ErrCannotDeactivateSelf) {
		t.Errorf("err = %v, want ErrCannotDeactivateSelf", err)
	}
}

func TestAccountService_DeactivatePlatformAdmin_PropagatesMinimumActiveAdminError(t *testing.T) {
	repo := &fakeAccountRepository{err: domain.ErrMinimumActiveAdminRequired}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())
	if err := svc.DeactivatePlatformAdmin(context.Background(), "pa-target", "pa-actor"); !errors.Is(err, domain.ErrMinimumActiveAdminRequired) {
		t.Errorf("err = %v, want ErrMinimumActiveAdminRequired", err)
	}
}

func TestAccountService_DeactivatePlatformAdmin_Success(t *testing.T) {
	svc := NewAccountService(&fakeAccountRepository{}, &fakeKeycloakClient{}, zap.NewNop())
	if err := svc.DeactivatePlatformAdmin(context.Background(), "pa-target", "pa-actor"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAccountService_ResetPlatformAdminMFA_RejectsSelf(t *testing.T) {
	svc := NewAccountService(&fakeAccountRepository{}, &fakeKeycloakClient{}, zap.NewNop())
	if err := svc.ResetPlatformAdminMFA(context.Background(), "pa-1", "pa-1"); !errors.Is(err, domain.ErrCannotResetOwnMFA) {
		t.Errorf("err = %v, want ErrCannotResetOwnMFA", err)
	}
}

func TestAccountService_ResetPlatformAdminMFA_Success(t *testing.T) {
	svc := NewAccountService(&fakeAccountRepository{}, &fakeKeycloakClient{}, zap.NewNop())
	if err := svc.ResetPlatformAdminMFA(context.Background(), "pa-target", "pa-actor"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAccountService_ListPlatformAdmins_PropagatesError(t *testing.T) {
	repo := &fakeAccountRepository{err: errors.New("db down")}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())
	if _, err := svc.ListPlatformAdmins(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}
