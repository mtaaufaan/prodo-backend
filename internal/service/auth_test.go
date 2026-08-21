package service

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeAuthRepository struct {
	user    *repository.LoginUserRecord
	findErr error
}

func (f *fakeAuthRepository) FindUserForLogin(_ context.Context, _ string) (*repository.LoginUserRecord, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.user, nil
}

type fakeROPCClient struct {
	token *keycloak.TokenResponse
	err   error
}

func (f *fakeROPCClient) PasswordGrant(_ context.Context, _, _ string) (*keycloak.TokenResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.token, nil
}

func TestAuthService_LoginLocal_Success(t *testing.T) {
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{
		ID: "user-1", Email: "ga@example.com", DisplayName: "GA", PlatformRole: "group_admin", IsActive: true,
	}}
	ropc := &fakeROPCClient{token: &keycloak.TokenResponse{AccessToken: "at", RefreshToken: "rt", TokenType: "Bearer", ExpiresIn: 3600}}
	svc := NewAuthService(repo, ropc, zap.NewNop())

	result, err := svc.LoginLocal(context.Background(), "ga@example.com", "Str0ng!Passw0rd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AccessToken != "at" || result.User.ID != "user-1" {
		t.Errorf("result = %+v, unexpected", result)
	}
}

func TestAuthService_LoginLocal_EmailNotFound(t *testing.T) {
	repo := &fakeAuthRepository{findErr: domain.ErrUserNotFound}
	svc := NewAuthService(repo, &fakeROPCClient{}, zap.NewNop())

	_, err := svc.LoginLocal(context.Background(), "unknown@example.com", "whatever")
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidCredentials", err)
	}
}

func TestAuthService_LoginLocal_AccountInactive(t *testing.T) {
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{ID: "user-1", IsActive: false}}
	svc := NewAuthService(repo, &fakeROPCClient{}, zap.NewNop())

	_, err := svc.LoginLocal(context.Background(), "pending@example.com", "whatever")
	if !errors.Is(err, domain.ErrAccountInactive) {
		t.Errorf("err = %v, want wrapped domain.ErrAccountInactive", err)
	}
}

func TestAuthService_LoginLocal_WrongPassword(t *testing.T) {
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{ID: "user-1", IsActive: true}}
	ropc := &fakeROPCClient{err: keycloak.ErrInvalidGrant}
	svc := NewAuthService(repo, ropc, zap.NewNop())

	_, err := svc.LoginLocal(context.Background(), "ga@example.com", "wrong-password")
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidCredentials", err)
	}
}
