package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeAccountRepository struct {
	captured repository.CreateGroupAdminInvitationParams
	returnID string
	err      error
}

func (f *fakeAccountRepository) CreateGroupAdminInvitation(_ context.Context, p *repository.CreateGroupAdminInvitationParams) (string, error) {
	f.captured = *p
	if f.err != nil {
		return "", f.err
	}
	return f.returnID, nil
}

type fakeKeycloakClient struct {
	userID string
	err    error
}

func (f *fakeKeycloakClient) CreateDisabledUser(_ context.Context, _, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.userID, nil
}

func TestAccountService_CreateGroupAdmin_Success(t *testing.T) {
	repo := &fakeAccountRepository{returnID: "user-123"}
	kc := &fakeKeycloakClient{userID: "kc-sub-456"}
	svc := NewAccountService(repo, kc, zap.NewNop())

	req := CreateGroupAdminRequest{
		Email:           "ga@example.com",
		DisplayName:     "Group Admin",
		InvitedByUserID: "platform-admin-1",
	}

	before := time.Now()
	result, err := svc.CreateGroupAdmin(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.UserID != "user-123" {
		t.Errorf("UserID = %q, want %q", result.UserID, "user-123")
	}
	if result.ActivationToken == "" {
		t.Error("ActivationToken kosong, seharusnya token acak")
	}
	if !result.ExpiresAt.After(before.Add(71 * time.Hour)) {
		t.Errorf("ExpiresAt = %v, seharusnya ~72 jam dari sekarang", result.ExpiresAt)
	}

	if repo.captured.KeycloakUserID != "kc-sub-456" {
		t.Errorf("repo menerima KeycloakUserID = %q, want %q", repo.captured.KeycloakUserID, "kc-sub-456")
	}
	if repo.captured.TokenHash == "" || repo.captured.TokenHash == result.ActivationToken {
		t.Error("TokenHash harus terisi dan berbeda dari raw token (bukti hashing, bukan plaintext)")
	}
}

func TestAccountService_CreateGroupAdmin_InvalidInput(t *testing.T) {
	svc := NewAccountService(&fakeAccountRepository{}, &fakeKeycloakClient{}, zap.NewNop())

	_, err := svc.CreateGroupAdmin(context.Background(), CreateGroupAdminRequest{})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}

func TestAccountService_CreateGroupAdmin_EmailAlreadyExistsInKeycloak(t *testing.T) {
	kc := &fakeKeycloakClient{err: keycloak.ErrUserAlreadyExists}
	svc := NewAccountService(&fakeAccountRepository{}, kc, zap.NewNop())

	_, err := svc.CreateGroupAdmin(context.Background(), CreateGroupAdminRequest{
		Email:           "dup@example.com",
		DisplayName:     "Dup",
		InvitedByUserID: "platform-admin-1",
	})
	if !errors.Is(err, domain.ErrEmailAlreadyExists) {
		t.Errorf("err = %v, want wrapped domain.ErrEmailAlreadyExists", err)
	}
}

func TestAccountService_CreateGroupAdmin_RepoFailureAfterKeycloakCreate(t *testing.T) {
	repo := &fakeAccountRepository{err: errors.New("db down")}
	kc := &fakeKeycloakClient{userID: "kc-sub-orphan"}
	svc := NewAccountService(repo, kc, zap.NewNop())

	_, err := svc.CreateGroupAdmin(context.Background(), CreateGroupAdminRequest{
		Email:           "ga@example.com",
		DisplayName:     "Group Admin",
		InvitedByUserID: "platform-admin-1",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
