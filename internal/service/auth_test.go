package service

import (
	"context"
	"errors"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// jwtNewUnsigned membangun JWT well-formed (HS256, secret dummy) berisi
// klaim sub/email/name -- cukup untuk LoginSSO karena ParseUnverified tidak
// pernah memeriksa signature (lihat komentar di auth.go).
func jwtNewUnsigned(t *testing.T, sub, email, name string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":   sub,
		"email": email,
		"name":  name,
	})
	signed, err := tok.SignedString([]byte("test-secret-not-verified"))
	if err != nil {
		t.Fatalf("gagal membuat test JWT: %v", err)
	}
	return signed
}

type fakeAuthRepository struct {
	user    *repository.LoginUserRecord
	findErr error

	providerSubToUserID map[string]string
	usersByID           map[string]*repository.LoginUserRecord

	createdUser *repository.LoginUserRecord
	createErr   error

	recordedLoginUserID string
	recordLoginErr      error
}

func (f *fakeAuthRepository) RecordLogin(_ context.Context, userID, _ string) error {
	f.recordedLoginUserID = userID
	return f.recordLoginErr
}

func (f *fakeAuthRepository) FindUserForLogin(_ context.Context, _ string) (*repository.LoginUserRecord, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.user, nil
}

func (f *fakeAuthRepository) FindUserIDByProviderSub(_ context.Context, providerSub string) (string, error) {
	if id, ok := f.providerSubToUserID[providerSub]; ok {
		return id, nil
	}
	return "", domain.ErrUserNotFound
}

func (f *fakeAuthRepository) FindUserByID(_ context.Context, userID string) (*repository.LoginUserRecord, error) {
	if u, ok := f.usersByID[userID]; ok {
		return u, nil
	}
	return nil, domain.ErrUserNotFound
}

func (f *fakeAuthRepository) CreateSSOUser(_ context.Context, email, displayName, _ string) (*repository.LoginUserRecord, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.createdUser = &repository.LoginUserRecord{ID: "new-user-1", Email: email, DisplayName: displayName, PlatformRole: "member", IsActive: true}
	return f.createdUser, nil
}

type fakeOIDCClient struct {
	token *keycloak.TokenResponse
	err   error
}

func (f *fakeOIDCClient) PasswordGrant(_ context.Context, _, _ string) (*keycloak.TokenResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.token, nil
}

func (f *fakeOIDCClient) ExchangeAuthorizationCode(_ context.Context, _, _ string) (*keycloak.TokenResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.token, nil
}

func TestAuthService_LoginLocal_Success(t *testing.T) {
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{
		ID: "user-1", Email: "ga@example.com", DisplayName: "GA", PlatformRole: "group_admin", IsActive: true,
	}}
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: "at", RefreshToken: "rt", TokenType: "Bearer", ExpiresIn: 3600}}
	svc := NewAuthService(repo, oidc, NewMFAService(&fakeMFARepository{}), zap.NewNop())

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
	svc := NewAuthService(repo, &fakeOIDCClient{}, NewMFAService(&fakeMFARepository{}), zap.NewNop())

	_, err := svc.LoginLocal(context.Background(), "unknown@example.com", "whatever")
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidCredentials", err)
	}
}

func TestAuthService_LoginLocal_AccountInactive(t *testing.T) {
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{ID: "user-1", IsActive: false}}
	svc := NewAuthService(repo, &fakeOIDCClient{}, NewMFAService(&fakeMFARepository{}), zap.NewNop())

	_, err := svc.LoginLocal(context.Background(), "pending@example.com", "whatever")
	if !errors.Is(err, domain.ErrAccountInactive) {
		t.Errorf("err = %v, want wrapped domain.ErrAccountInactive", err)
	}
}

func TestAuthService_LoginLocal_WrongPassword(t *testing.T) {
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{ID: "user-1", IsActive: true}}
	oidc := &fakeOIDCClient{err: keycloak.ErrInvalidGrant}
	svc := NewAuthService(repo, oidc, NewMFAService(&fakeMFARepository{}), zap.NewNop())

	_, err := svc.LoginLocal(context.Background(), "ga@example.com", "wrong-password")
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidCredentials", err)
	}
}

func TestAuthService_LoginSSO_ExistingUser(t *testing.T) {
	idToken := jwtNewUnsigned(t, "kc-sub-1", "member@example.com", "Member Existing")
	repo := &fakeAuthRepository{
		providerSubToUserID: map[string]string{"kc-sub-1": "user-1"},
		usersByID:           map[string]*repository.LoginUserRecord{"user-1": {ID: "user-1", Email: "member@example.com", PlatformRole: "member", IsActive: true}},
	}
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: "at", IDToken: idToken, TokenType: "Bearer", ExpiresIn: 3600}}
	svc := NewAuthService(repo, oidc, NewMFAService(&fakeMFARepository{}), zap.NewNop())

	result, err := svc.LoginSSO(context.Background(), "auth-code", "http://localhost:5173/auth/callback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.User.ID != "user-1" {
		t.Errorf("User.ID = %q, want user-1 (existing user, bukan auto-create)", result.User.ID)
	}
	if repo.createdUser != nil {
		t.Error("tidak boleh auto-create -- provider_sub sudah terdaftar")
	}
}

func TestAuthService_LoginSSO_FirstTimeAutoCreate(t *testing.T) {
	idToken := jwtNewUnsigned(t, "kc-sub-new", "newmember@example.com", "New Member")
	repo := &fakeAuthRepository{providerSubToUserID: map[string]string{}}
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: "at", IDToken: idToken, TokenType: "Bearer", ExpiresIn: 3600}}
	svc := NewAuthService(repo, oidc, NewMFAService(&fakeMFARepository{}), zap.NewNop())

	result, err := svc.LoginSSO(context.Background(), "auth-code", "http://localhost:5173/auth/callback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.createdUser == nil {
		t.Fatal("harusnya auto-create akun baru")
	}
	if result.User.Email != "newmember@example.com" || !result.User.IsActive {
		t.Errorf("result.User = %+v, unexpected", result.User)
	}
}

func TestAuthService_LoginSSO_EmailCollision(t *testing.T) {
	idToken := jwtNewUnsigned(t, "kc-sub-new", "taken@example.com", "New Member")
	repo := &fakeAuthRepository{providerSubToUserID: map[string]string{}, createErr: domain.ErrEmailAlreadyExists}
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: "at", IDToken: idToken, TokenType: "Bearer", ExpiresIn: 3600}}
	svc := NewAuthService(repo, oidc, NewMFAService(&fakeMFARepository{}), zap.NewNop())

	_, err := svc.LoginSSO(context.Background(), "auth-code", "http://localhost:5173/auth/callback")
	if !errors.Is(err, domain.ErrEmailAlreadyExists) {
		t.Errorf("err = %v, want wrapped domain.ErrEmailAlreadyExists", err)
	}
}

func TestAuthService_LoginSSO_InvalidCode(t *testing.T) {
	repo := &fakeAuthRepository{}
	oidc := &fakeOIDCClient{err: keycloak.ErrInvalidGrant}
	svc := NewAuthService(repo, oidc, NewMFAService(&fakeMFARepository{}), zap.NewNop())

	_, err := svc.LoginSSO(context.Background(), "expired-code", "http://localhost:5173/auth/callback")
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidCredentials", err)
	}
}

func TestAuthService_VerifyMFA_ValidOTP(t *testing.T) {
	secret := generateTestTOTPSecret(t)
	svc := NewAuthService(&fakeAuthRepository{}, &fakeOIDCClient{}, NewMFAService(&fakeMFARepository{enabled: true, savedSecret: secret}), zap.NewNop())

	err := svc.VerifyMFA(context.Background(), "user-1", true, currentTOTPCode(t, secret))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAuthService_VerifyMFA_WrongOTP(t *testing.T) {
	secret := generateTestTOTPSecret(t)
	svc := NewAuthService(&fakeAuthRepository{}, &fakeOIDCClient{}, NewMFAService(&fakeMFARepository{enabled: true, savedSecret: secret}), zap.NewNop())

	err := svc.VerifyMFA(context.Background(), "user-1", true, "000000")
	if !errors.Is(err, domain.ErrInvalidOTP) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidOTP", err)
	}
}

func TestAuthService_VerifyMFA_GroupAdminWithoutMFA_Blocked(t *testing.T) {
	svc := NewAuthService(&fakeAuthRepository{}, &fakeOIDCClient{}, NewMFAService(&fakeMFARepository{enabled: false}), zap.NewNop())

	err := svc.VerifyMFA(context.Background(), "user-1", true, "")
	if !errors.Is(err, domain.ErrMFARequired) {
		t.Errorf("err = %v, want wrapped domain.ErrMFARequired", err)
	}
}

func TestAuthService_VerifyMFA_MemberWithoutMFA_Passes(t *testing.T) {
	svc := NewAuthService(&fakeAuthRepository{}, &fakeOIDCClient{}, NewMFAService(&fakeMFARepository{enabled: false}), zap.NewNop())

	err := svc.VerifyMFA(context.Background(), "user-1", false, "")
	if err != nil {
		t.Errorf("member tanpa MFA harusnya lolos (opsional): %v", err)
	}
}

func TestAuthService_Login_Success_RecordsAudit(t *testing.T) {
	secret := generateTestTOTPSecret(t)
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{
		ID: "user-1", Email: "ga@example.com", PlatformRole: "group_admin", IsActive: true,
	}}
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: "at", TokenType: "Bearer", ExpiresIn: 3600}}
	svc := NewAuthService(repo, oidc, NewMFAService(&fakeMFARepository{enabled: true, savedSecret: secret}), zap.NewNop())

	result, err := svc.Login(context.Background(), "ga@example.com", "Str0ng!Passw0rd", currentTOTPCode(t, secret))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AccessToken != "at" {
		t.Errorf("result = %+v, unexpected", result)
	}
	if repo.recordedLoginUserID != "user-1" {
		t.Error("login sukses harusnya tercatat lewat RecordLogin")
	}
}

func TestAuthService_Login_WrongOTP_NoAuditRecorded(t *testing.T) {
	secret := generateTestTOTPSecret(t)
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{
		ID: "user-1", Email: "ga@example.com", PlatformRole: "group_admin", IsActive: true,
	}}
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: "at", TokenType: "Bearer", ExpiresIn: 3600}}
	svc := NewAuthService(repo, oidc, NewMFAService(&fakeMFARepository{enabled: true, savedSecret: secret}), zap.NewNop())

	_, err := svc.Login(context.Background(), "ga@example.com", "Str0ng!Passw0rd", "000000")
	if !errors.Is(err, domain.ErrInvalidOTP) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidOTP", err)
	}
	if repo.recordedLoginUserID != "" {
		t.Error("login gagal (OTP salah) tidak boleh tercatat sebagai login berhasil")
	}
}
