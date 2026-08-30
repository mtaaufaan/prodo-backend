package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/cache"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// fakeSessionRepository/fakeCache/newTestSessionService -- SessionService
// dependency AuthService butuh (S1-27), tapi tesnya di file ini tidak
// tentang session tracking (lihat session_test.go untuk itu) -- fake
// no-op ini cuma supaya NewAuthService bisa dikonstruksi.
type fakeSessionRepository struct{}

func (f *fakeSessionRepository) CreateSession(_ context.Context, _, _ string, _ repository.DeviceInfo, _ time.Time) error {
	return nil
}
func (f *fakeSessionRepository) ListActiveSessions(_ context.Context, _ string) ([]repository.Session, error) {
	return nil, nil
}
func (f *fakeSessionRepository) TouchSession(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return true, nil
}
func (f *fakeSessionRepository) TouchSessionFixed(_ context.Context, _ string) (bool, error) {
	return true, nil
}
func (f *fakeSessionRepository) RenewSessionJTI(_ context.Context, _, _ string, _ time.Duration, _ time.Time) (bool, error) {
	return true, nil
}
func (f *fakeSessionRepository) RevokeSession(_ context.Context, _, _ string) (time.Duration, error) {
	return 0, nil
}
func (f *fakeSessionRepository) RevokeAllSessions(_ context.Context, _, _ string) ([]repository.RevokedSession, error) {
	return nil, nil
}
func (f *fakeSessionRepository) IsUserInOrg(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

// fakeEmailSender -- no-op (S4P-16); dites tersendiri lewat
// sentEmailSender di bawah untuk test yang khusus memverifikasi
// pengiriman alert login Platform Admin.
type fakeEmailSender struct{}

func (f *fakeEmailSender) SendPlatformAdminLoginAlertEmail(_ context.Context, _, _, _, _ string, _ time.Time) error {
	return nil
}

// sentEmailSender merekam parameter panggilan terakhir supaya test bisa
// memverifikasi alert login Platform Admin benar-benar dikirim (S4P-16).
type sentEmailSender struct {
	called      bool
	to, display string
	ip, device  string
	err         error
}

func (f *sentEmailSender) SendPlatformAdminLoginAlertEmail(_ context.Context, to, displayName, ip, device string, _ time.Time) error {
	f.called = true
	f.to, f.display, f.ip, f.device = to, displayName, ip, device
	return f.err
}

type fakeCache struct{}

func (f *fakeCache) Get(_ context.Context, _ string) (string, error)           { return "", cache.ErrNotFound }
func (f *fakeCache) Set(_ context.Context, _, _ string, _ time.Duration) error { return nil }
func (f *fakeCache) Del(_ context.Context, _ string) error                     { return nil }
func (f *fakeCache) Close() error                                              { return nil }

func newTestSessionService() *SessionService {
	return NewSessionService(&fakeSessionRepository{}, &fakeCache{})
}

// testAccessTokenJWT membangun JWT well-formed (HS256, secret dummy)
// berisi klaim jti+exp -- dibutuhkan SessionService.RecordSession
// (dipanggil dari Login() sukses) yang decode klaim ini via
// ParseUnverified, sama seperti jwtNewUnsigned tapi untuk access_token.
func testAccessTokenJWT(t *testing.T, jti string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		ID:        jti,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	signed, err := tok.SignedString([]byte("test-secret-not-verified"))
	if err != nil {
		t.Fatalf("gagal membuat test access token JWT: %v", err)
	}
	return signed
}

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

	recordedLoginUserID    string
	recordLoginErr         error
	recordedUsedBackupCode bool

	orgIDsForGroupAdmin []string
	orgIDsErr           error

	// ipDenied default false (zero value) -- SESUAI perilaku produksi
	// (tidak ada allowlist dikonfigurasi = selalu boleh). Set true HANYA
	// di test yang secara eksplisit menguji penolakan S4P-17, supaya test
	// PA lain (login/MFA/email) yang tidak terkait tidak diam-diam gagal.
	ipDenied  bool
	ipErr     error
	checkedIP string
}

func (f *fakeAuthRepository) ListOrgIDsForGroupAdmin(_ context.Context, _ string) ([]string, error) {
	return f.orgIDsForGroupAdmin, f.orgIDsErr
}

func (f *fakeAuthRepository) CheckIPAllowlist(_ context.Context, ip string) (bool, error) {
	f.checkedIP = ip
	return !f.ipDenied, f.ipErr
}

func (f *fakeAuthRepository) RecordLogin(_ context.Context, userID, _ string, usedBackupCode bool) error {
	f.recordedLoginUserID = userID
	f.recordedUsedBackupCode = usedBackupCode
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

func (f *fakeOIDCClient) RefreshTokenGrant(_ context.Context, _ string) (*keycloak.TokenResponse, error) {
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
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

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
	svc := NewAuthService(repo, &fakeOIDCClient{}, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, err := svc.LoginLocal(context.Background(), "unknown@example.com", "whatever")
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidCredentials", err)
	}
}

func TestAuthService_LoginLocal_AccountInactive(t *testing.T) {
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{ID: "user-1", IsActive: false}}
	svc := NewAuthService(repo, &fakeOIDCClient{}, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, err := svc.LoginLocal(context.Background(), "pending@example.com", "whatever")
	if !errors.Is(err, domain.ErrAccountInactive) {
		t.Errorf("err = %v, want wrapped domain.ErrAccountInactive", err)
	}
}

func TestAuthService_LoginLocal_AccountSuspended(t *testing.T) {
	// S4P-02 (US-067): suspended_at TERISI harus ditolak dengan
	// ErrAccountSuspended, BUKAN ErrAccountInactive -- meski IsActive juga
	// TRUE (akun ini pernah aktif sebelum disuspend PA).
	suspendedAt := time.Now().Add(-time.Hour)
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{ID: "user-1", IsActive: true, SuspendedAt: &suspendedAt}}
	svc := NewAuthService(repo, &fakeOIDCClient{}, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, err := svc.LoginLocal(context.Background(), "suspended-ga@example.com", "whatever")
	if !errors.Is(err, domain.ErrAccountSuspended) {
		t.Errorf("err = %v, want wrapped domain.ErrAccountSuspended", err)
	}
}

func TestAuthService_LoginLocal_WrongPassword(t *testing.T) {
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{ID: "user-1", IsActive: true}}
	oidc := &fakeOIDCClient{err: keycloak.ErrInvalidGrant}
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, err := svc.LoginLocal(context.Background(), "ga@example.com", "wrong-password")
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidCredentials", err)
	}
}

// S3-38 (implementation_gaps.md IG-14): LoginLocal wajib mensinkron
// attribute Keycloak SEBELUM menukar credential -- tiga test di bawah
// mengunci perilaku itu supaya tidak regresi diam-diam ke kondisi sebelum
// S3-38 (attribute tidak pernah disetel sama sekali).
func TestAuthService_LoginLocal_SyncsGroupAdminOrgIDs(t *testing.T) {
	repo := &fakeAuthRepository{
		user: &repository.LoginUserRecord{
			ID: "ga-1", Email: "ga@example.com", PlatformRole: "group_admin",
			IsActive: true, KeycloakUserID: "kc-ga-1",
		},
		orgIDsForGroupAdmin: []string{"org-1", "org-2"},
	}
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: "at"}}
	kc := &fakeKeycloakClient{}
	svc := NewAuthService(repo, oidc, kc, NewMFAService(&fakeMFARepository{}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	if _, err := svc.LoginLocal(context.Background(), "ga@example.com", "Str0ng!Passw0rd"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if kc.lastAttributesUserID != "kc-ga-1" {
		t.Errorf("lastAttributesUserID = %q, want kc-ga-1", kc.lastAttributesUserID)
	}
	if got := kc.lastAttributes["prodo_platform_role"]; len(got) != 1 || got[0] != "group_admin" {
		t.Errorf("prodo_platform_role = %v, want [group_admin]", got)
	}
	orgIDs := kc.lastAttributes["prodo_org_ids"]
	if len(orgIDs) != 2 || orgIDs[0] != "org-1" || orgIDs[1] != "org-2" {
		t.Errorf("prodo_org_ids = %v, want [org-1 org-2]", orgIDs)
	}
}

func TestAuthService_LoginLocal_MemberDoesNotSyncOrgIDs(t *testing.T) {
	repo := &fakeAuthRepository{
		user: &repository.LoginUserRecord{
			ID: "member-1", Email: "member@example.com", PlatformRole: "member",
			IsActive: true, KeycloakUserID: "kc-member-1",
		},
	}
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: "at"}}
	kc := &fakeKeycloakClient{}
	svc := NewAuthService(repo, oidc, kc, NewMFAService(&fakeMFARepository{}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	if _, err := svc.LoginLocal(context.Background(), "member@example.com", "Str0ng!Passw0rd"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := kc.lastAttributes["prodo_platform_role"]; len(got) != 1 || got[0] != "member" {
		t.Errorf("prodo_platform_role = %v, want [member]", got)
	}
	if _, ok := kc.lastAttributes["prodo_org_ids"]; ok {
		t.Errorf("prodo_org_ids = %v, want key absent for non-GA", kc.lastAttributes["prodo_org_ids"])
	}
}

func TestAuthService_LoginLocal_KeycloakSyncFailureDoesNotBlockLogin(t *testing.T) {
	repo := &fakeAuthRepository{
		user: &repository.LoginUserRecord{ID: "user-1", Email: "member@example.com", PlatformRole: "member", IsActive: true},
	}
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: "at"}}
	kc := &fakeKeycloakClient{attributesErr: errors.New("keycloak admin api down")}
	svc := NewAuthService(repo, oidc, kc, NewMFAService(&fakeMFARepository{}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	result, err := svc.LoginLocal(context.Background(), "member@example.com", "Str0ng!Passw0rd")
	if err != nil {
		t.Fatalf("sync failure should not block login, got error: %v", err)
	}
	if result.AccessToken != "at" {
		t.Errorf("result = %+v, unexpected", result)
	}
}

func TestAuthService_LoginSSO_ExistingUser(t *testing.T) {
	idToken := jwtNewUnsigned(t, "kc-sub-1", "member@example.com", "Member Existing")
	repo := &fakeAuthRepository{
		providerSubToUserID: map[string]string{"kc-sub-1": "user-1"},
		usersByID:           map[string]*repository.LoginUserRecord{"user-1": {ID: "user-1", Email: "member@example.com", PlatformRole: "member", IsActive: true}},
	}
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: "at", IDToken: idToken, TokenType: "Bearer", ExpiresIn: 3600}}
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

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
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

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
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, err := svc.LoginSSO(context.Background(), "auth-code", "http://localhost:5173/auth/callback")
	if !errors.Is(err, domain.ErrEmailAlreadyExists) {
		t.Errorf("err = %v, want wrapped domain.ErrEmailAlreadyExists", err)
	}
}

func TestAuthService_LoginSSO_InvalidCode(t *testing.T) {
	repo := &fakeAuthRepository{}
	oidc := &fakeOIDCClient{err: keycloak.ErrInvalidGrant}
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, err := svc.LoginSSO(context.Background(), "expired-code", "http://localhost:5173/auth/callback")
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidCredentials", err)
	}
}

func TestAuthService_VerifyMFA_ValidOTP(t *testing.T) {
	secret := generateTestTOTPSecret(t)
	svc := NewAuthService(&fakeAuthRepository{}, &fakeOIDCClient{}, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{enabled: true, savedSecret: secret}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	usedBackupCode, err := svc.VerifyMFA(context.Background(), "user-1", true, false, currentTOTPCode(t, secret))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usedBackupCode {
		t.Error("usedBackupCode harus false untuk OTP TOTP biasa")
	}
}

func TestAuthService_VerifyMFA_ValidBackupCode(t *testing.T) {
	mfaRepo := &fakeMFARepository{enabled: true, consumeBackupCodeResult: true}
	svc := NewAuthService(&fakeAuthRepository{}, &fakeOIDCClient{}, &fakeKeycloakClient{}, NewMFAService(mfaRepo), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	usedBackupCode, err := svc.VerifyMFA(context.Background(), "user-1", true, false, "abcd-1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !usedBackupCode {
		t.Error("usedBackupCode harus true kalau login pakai kode cadangan yang cocok")
	}
}

func TestAuthService_VerifyMFA_WrongOTP(t *testing.T) {
	secret := generateTestTOTPSecret(t)
	svc := NewAuthService(&fakeAuthRepository{}, &fakeOIDCClient{}, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{enabled: true, savedSecret: secret}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, err := svc.VerifyMFA(context.Background(), "user-1", true, false, "000000")
	if !errors.Is(err, domain.ErrInvalidOTP) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidOTP", err)
	}
}

func TestAuthService_VerifyMFA_GroupAdminWithoutMFA_Blocked(t *testing.T) {
	svc := NewAuthService(&fakeAuthRepository{}, &fakeOIDCClient{}, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{enabled: false}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, err := svc.VerifyMFA(context.Background(), "user-1", true, false, "")
	if !errors.Is(err, domain.ErrMFARequired) {
		t.Errorf("err = %v, want wrapped domain.ErrMFARequired", err)
	}
}

func TestAuthService_VerifyMFA_PlatformAdminWithoutMFA_SetupRequired(t *testing.T) {
	svc := NewAuthService(&fakeAuthRepository{}, &fakeOIDCClient{}, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{enabled: false}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, err := svc.VerifyMFA(context.Background(), "user-1", false, true, "")
	if !errors.Is(err, domain.ErrMFASetupRequired) {
		t.Errorf("err = %v, want wrapped domain.ErrMFASetupRequired", err)
	}
}

func TestAuthService_VerifyMFA_MemberWithoutMFA_Passes(t *testing.T) {
	svc := NewAuthService(&fakeAuthRepository{}, &fakeOIDCClient{}, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{enabled: false}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, err := svc.VerifyMFA(context.Background(), "user-1", false, false, "")
	if err != nil {
		t.Errorf("member tanpa MFA harusnya lolos (opsional): %v", err)
	}
}

func TestAuthService_Login_Success_RecordsAudit(t *testing.T) {
	secret := generateTestTOTPSecret(t)
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{
		ID: "user-1", Email: "ga@example.com", PlatformRole: "group_admin", IsActive: true,
	}}
	accessToken := testAccessTokenJWT(t, "jti-login-success")
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: accessToken, TokenType: "Bearer", ExpiresIn: 3600}}
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{enabled: true, savedSecret: secret}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	result, challenge, err := svc.Login(context.Background(), "ga@example.com", "Str0ng!Passw0rd", currentTOTPCode(t, secret), "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if challenge != nil {
		t.Errorf("login sukses tidak boleh menerbitkan MFASetupChallenge: %+v", challenge)
	}
	if result.AccessToken != accessToken {
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
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{enabled: true, savedSecret: secret}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, _, err := svc.Login(context.Background(), "ga@example.com", "Str0ng!Passw0rd", "000000", "test-agent", "127.0.0.1")
	if !errors.Is(err, domain.ErrInvalidOTP) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidOTP", err)
	}
	if repo.recordedLoginUserID != "" {
		t.Error("login gagal (OTP salah) tidak boleh tercatat sebagai login berhasil")
	}
}

func TestAuthService_Login_PlatformAdminWithoutMFA_IssuesSetupChallenge(t *testing.T) {
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{
		ID: "pa-1", Email: "pa@example.com", PlatformRole: "platform_admin", IsActive: true,
	}}
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: "at", TokenType: "Bearer", ExpiresIn: 3600}}
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{enabled: false}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	result, challenge, err := svc.Login(context.Background(), "pa@example.com", "Str0ng!Passw0rd", "", "test-agent", "127.0.0.1")
	if !errors.Is(err, domain.ErrMFASetupRequired) {
		t.Fatalf("err = %v, want wrapped domain.ErrMFASetupRequired", err)
	}
	if result != nil {
		t.Errorf("result harus nil saat setup MFA dibutuhkan: %+v", result)
	}
	if challenge == nil || challenge.QRCodePNGBase64 == "" || challenge.TOTPSecret == "" {
		t.Fatalf("challenge harus berisi QR + secret: %+v", challenge)
	}
	if repo.recordedLoginUserID != "" {
		t.Error("login belum selesai (masih tahap setup MFA) tidak boleh tercatat sebagai login berhasil")
	}
}

func TestAuthService_CompletePlatformAdminMFASetup_Success(t *testing.T) {
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{
		ID: "pa-1", Email: "pa@example.com", PlatformRole: "platform_admin", IsActive: true,
	}}
	accessToken := testAccessTokenJWT(t, "jti-pa-setup")
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: accessToken, TokenType: "Bearer", ExpiresIn: 3600}}
	mfaRepo := &fakeMFARepository{enabled: false}
	mfaSvc := NewMFAService(mfaRepo)
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, mfaSvc, newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	setup, err := mfaSvc.SetupTOTP(context.Background(), "pa-1", "pa@example.com")
	if err != nil {
		t.Fatalf("SetupTOTP: %v", err)
	}

	result, err := svc.CompletePlatformAdminMFASetup(context.Background(), "pa@example.com", "Str0ng!Passw0rd", currentTOTPCode(t, setup.TOTPSecret), "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AccessToken != accessToken {
		t.Errorf("result = %+v, unexpected", result)
	}
	if len(result.BackupCodes) == 0 {
		t.Error("setup MFA sukses harus menerbitkan backup codes")
	}
	if repo.recordedLoginUserID != "pa-1" {
		t.Error("setup MFA + login sukses harusnya tercatat lewat RecordLogin")
	}
}

func TestAuthService_CompletePlatformAdminMFASetup_WrongOTP(t *testing.T) {
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{
		ID: "pa-1", Email: "pa@example.com", PlatformRole: "platform_admin", IsActive: true,
	}}
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: "at", TokenType: "Bearer", ExpiresIn: 3600}}
	mfaRepo := &fakeMFARepository{enabled: false}
	mfaSvc := NewMFAService(mfaRepo)
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, mfaSvc, newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	if _, err := mfaSvc.SetupTOTP(context.Background(), "pa-1", "pa@example.com"); err != nil {
		t.Fatalf("SetupTOTP: %v", err)
	}

	_, err := svc.CompletePlatformAdminMFASetup(context.Background(), "pa@example.com", "Str0ng!Passw0rd", "000000", "test-agent", "127.0.0.1")
	if !errors.Is(err, domain.ErrInvalidOTP) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidOTP", err)
	}
	if repo.recordedLoginUserID != "" {
		t.Error("setup gagal (OTP salah) tidak boleh tercatat sebagai login berhasil")
	}
}

func TestAuthService_Login_PlatformAdmin_SendsLoginAlertEmail(t *testing.T) {
	// S4P-16 (implementation_gaps.md IG-20): setiap login PA yang SUKSES
	// (MFA sudah aktif, kode benar) mengirim email alert.
	secret := generateTestTOTPSecret(t)
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{
		ID: "pa-1", Email: "pa@example.com", DisplayName: "PA Demo", PlatformRole: "platform_admin", IsActive: true,
	}}
	accessToken := testAccessTokenJWT(t, "jti-pa-alert")
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: accessToken, TokenType: "Bearer", ExpiresIn: 3600}}
	emailer := &sentEmailSender{}
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{enabled: true, savedSecret: secret}), newTestSessionService(), emailer, zap.NewNop())

	_, _, err := svc.Login(context.Background(), "pa@example.com", "Str0ng!Passw0rd", currentTOTPCode(t, secret), "Mozilla/5.0 Chrome/125.0", "203.0.113.5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !emailer.called {
		t.Fatal("login PA sukses harusnya mengirim email alert")
	}
	if emailer.to != "pa@example.com" || emailer.display != "PA Demo" {
		t.Errorf("emailer dipanggil dengan to=%q display=%q, want pa@example.com/PA Demo", emailer.to, emailer.display)
	}
	if emailer.ip != "203.0.113.5" {
		t.Errorf("emailer.ip = %q, want 203.0.113.5", emailer.ip)
	}
}

func TestAuthService_Login_PlatformAdmin_EmailFailureDoesNotFailLogin(t *testing.T) {
	// Beda dari RecordLogin/RecordSession -- email alert cuma notifikasi
	// tambahan, kegagalannya tidak boleh menggagalkan login yang sudah sah.
	secret := generateTestTOTPSecret(t)
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{
		ID: "pa-1", Email: "pa@example.com", PlatformRole: "platform_admin", IsActive: true,
	}}
	accessToken := testAccessTokenJWT(t, "jti-pa-alert-fail")
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: accessToken, TokenType: "Bearer", ExpiresIn: 3600}}
	emailer := &sentEmailSender{err: errors.New("smtp down")}
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{enabled: true, savedSecret: secret}), newTestSessionService(), emailer, zap.NewNop())

	result, _, err := svc.Login(context.Background(), "pa@example.com", "Str0ng!Passw0rd", currentTOTPCode(t, secret), "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("login harus tetap sukses meski email alert gagal: %v", err)
	}
	if result == nil {
		t.Fatal("result tidak boleh nil")
	}
	if !emailer.called {
		t.Error("pengiriman email harusnya tetap dicoba")
	}
}

func TestAuthService_Login_GroupAdmin_DoesNotSendPlatformAdminAlert(t *testing.T) {
	// Email alert khusus Platform Admin (S4P-16) -- GA TIDAK terpengaruh.
	secret := generateTestTOTPSecret(t)
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{
		ID: "ga-1", Email: "ga@example.com", PlatformRole: "group_admin", IsActive: true,
	}}
	accessToken := testAccessTokenJWT(t, "jti-ga-noalert")
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: accessToken, TokenType: "Bearer", ExpiresIn: 3600}}
	emailer := &sentEmailSender{}
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{enabled: true, savedSecret: secret}), newTestSessionService(), emailer, zap.NewNop())

	_, _, err := svc.Login(context.Background(), "ga@example.com", "Str0ng!Passw0rd", currentTOTPCode(t, secret), "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emailer.called {
		t.Error("login Group Admin tidak boleh mengirim alert khusus Platform Admin")
	}
}

func TestAuthService_CompletePlatformAdminMFASetup_SendsLoginAlertEmail(t *testing.T) {
	repo := &fakeAuthRepository{user: &repository.LoginUserRecord{
		ID: "pa-1", Email: "pa@example.com", DisplayName: "PA Demo", PlatformRole: "platform_admin", IsActive: true,
	}}
	accessToken := testAccessTokenJWT(t, "jti-pa-setup-alert")
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: accessToken, TokenType: "Bearer", ExpiresIn: 3600}}
	mfaSvc := NewMFAService(&fakeMFARepository{enabled: false})
	emailer := &sentEmailSender{}
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, mfaSvc, newTestSessionService(), emailer, zap.NewNop())

	setup, err := mfaSvc.SetupTOTP(context.Background(), "pa-1", "pa@example.com")
	if err != nil {
		t.Fatalf("SetupTOTP: %v", err)
	}

	_, err = svc.CompletePlatformAdminMFASetup(context.Background(), "pa@example.com", "Str0ng!Passw0rd", currentTOTPCode(t, setup.TOTPSecret), "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !emailer.called {
		t.Error("setup MFA pertama kali + login sukses harusnya juga mengirim alert login")
	}
}

func TestAuthService_Login_PlatformAdmin_IPDenied_RejectsBeforeMFA(t *testing.T) {
	// S4P-17 (implementation_gaps.md IG-20): IP ditolak HARUS dicek
	// sebelum MFA -- PA tidak dapat info status MFA-nya dari IP terlarang.
	repo := &fakeAuthRepository{
		user: &repository.LoginUserRecord{
			ID: "pa-1", Email: "pa@example.com", PlatformRole: "platform_admin", IsActive: true,
		},
		ipDenied: true,
	}
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: "at", TokenType: "Bearer", ExpiresIn: 3600}}
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{enabled: false}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, challenge, err := svc.Login(context.Background(), "pa@example.com", "Str0ng!Passw0rd", "", "test-agent", "198.51.100.9")
	if !errors.Is(err, domain.ErrIPNotAllowed) {
		t.Fatalf("err = %v, want wrapped domain.ErrIPNotAllowed", err)
	}
	if challenge != nil {
		t.Error("IP ditolak tidak boleh menerbitkan MFASetupChallenge -- PA belum tentu tahu statusnya butuh setup atau tidak")
	}
	if repo.checkedIP != "198.51.100.9" {
		t.Errorf("CheckIPAllowlist dipanggil dengan ip=%q, want 198.51.100.9", repo.checkedIP)
	}
}

func TestAuthService_Login_PlatformAdmin_EmptyIP_SkipsAllowlistCheck(t *testing.T) {
	// Client dev/test tanpa header IP nyata (ip="") -- lolos tanpa
	// mengecek, sama pola toleransi userAgent/ip kosong di RecordSession.
	secret := generateTestTOTPSecret(t)
	repo := &fakeAuthRepository{
		user: &repository.LoginUserRecord{
			ID: "pa-1", Email: "pa@example.com", PlatformRole: "platform_admin", IsActive: true,
		},
		ipDenied: true,
	}
	accessToken := testAccessTokenJWT(t, "jti-pa-empty-ip")
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: accessToken, TokenType: "Bearer", ExpiresIn: 3600}}
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{enabled: true, savedSecret: secret}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, _, err := svc.Login(context.Background(), "pa@example.com", "Str0ng!Passw0rd", currentTOTPCode(t, secret), "test-agent", "")
	if err != nil {
		t.Fatalf("ip kosong harusnya lolos tanpa cek allowlist: %v", err)
	}
	if repo.checkedIP != "" {
		t.Error("CheckIPAllowlist tidak boleh dipanggil sama sekali kalau ip kosong")
	}
}

func TestAuthService_Login_GroupAdmin_NotSubjectToIPAllowlist(t *testing.T) {
	// IP allowlist khusus Platform Admin (S4P-17) -- GA TIDAK terpengaruh
	// meski repo bilang ip ditolak.
	secret := generateTestTOTPSecret(t)
	repo := &fakeAuthRepository{
		user: &repository.LoginUserRecord{
			ID: "ga-1", Email: "ga@example.com", PlatformRole: "group_admin", IsActive: true,
		},
		ipDenied: true,
	}
	accessToken := testAccessTokenJWT(t, "jti-ga-ip")
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: accessToken, TokenType: "Bearer", ExpiresIn: 3600}}
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{enabled: true, savedSecret: secret}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, _, err := svc.Login(context.Background(), "ga@example.com", "Str0ng!Passw0rd", currentTOTPCode(t, secret), "test-agent", "198.51.100.9")
	if err != nil {
		t.Fatalf("GA tidak boleh terkena IP allowlist Platform Admin: %v", err)
	}
}

func TestAuthService_CompletePlatformAdminMFASetup_IPDenied(t *testing.T) {
	repo := &fakeAuthRepository{
		user: &repository.LoginUserRecord{
			ID: "pa-1", Email: "pa@example.com", PlatformRole: "platform_admin", IsActive: true,
		},
		ipDenied: true,
	}
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: "at", TokenType: "Bearer", ExpiresIn: 3600}}
	mfaSvc := NewMFAService(&fakeMFARepository{enabled: false})
	svc := NewAuthService(repo, oidc, &fakeKeycloakClient{}, mfaSvc, newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	if _, err := mfaSvc.SetupTOTP(context.Background(), "pa-1", "pa@example.com"); err != nil {
		t.Fatalf("SetupTOTP: %v", err)
	}

	_, err := svc.CompletePlatformAdminMFASetup(context.Background(), "pa@example.com", "Str0ng!Passw0rd", "000000", "test-agent", "198.51.100.9")
	if !errors.Is(err, domain.ErrIPNotAllowed) {
		t.Errorf("err = %v, want wrapped domain.ErrIPNotAllowed", err)
	}
}

func TestAuthService_RefreshAccessToken_Success(t *testing.T) {
	newAccessToken := testAccessTokenJWT(t, "jti-new")
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{
		AccessToken: newAccessToken, RefreshToken: "rt-new", TokenType: "Bearer", ExpiresIn: 300,
	}}
	svc := NewAuthService(&fakeAuthRepository{}, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	result, err := svc.RefreshAccessToken(context.Background(), "jti-old", "rt-old")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AccessToken != newAccessToken || result.RefreshToken != "rt-new" {
		t.Errorf("result = %+v", result)
	}
}

func TestAuthService_RefreshAccessToken_KeycloakInvalidGrant(t *testing.T) {
	oidc := &fakeOIDCClient{err: keycloak.ErrInvalidGrant}
	svc := NewAuthService(&fakeAuthRepository{}, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{}), newTestSessionService(), &fakeEmailSender{}, zap.NewNop())

	_, err := svc.RefreshAccessToken(context.Background(), "jti-old", "rt-expired")
	if !errors.Is(err, keycloak.ErrInvalidGrant) {
		t.Errorf("err = %v, want wrapped keycloak.ErrInvalidGrant", err)
	}
}

// TestAuthService_RefreshAccessToken_SessionAlreadyExpired -- sesi lama
// sudah lewat idle-timeout-nya sendiri (RenewSessionJTI repo bilang
// tidak valid) -- refresh HARUS ditolak meski Keycloak sendiri masih mau
// menukar refresh_token-nya. Ini adalah titik penegakan idle-timeout
// yang sesungguhnya (2026-08-29), bukan lagi di lapisan JWT 5 menit.
func TestAuthService_RefreshAccessToken_SessionAlreadyExpired(t *testing.T) {
	newAccessToken := testAccessTokenJWT(t, "jti-new")
	oidc := &fakeOIDCClient{token: &keycloak.TokenResponse{AccessToken: newAccessToken, RefreshToken: "rt-new", ExpiresIn: 300}}
	sessions := NewSessionService(&stubSessionRepository{renewValid: false}, &fakeCache{})
	svc := NewAuthService(&fakeAuthRepository{}, oidc, &fakeKeycloakClient{}, NewMFAService(&fakeMFARepository{}), sessions, &fakeEmailSender{}, zap.NewNop())

	_, err := svc.RefreshAccessToken(context.Background(), "jti-old", "rt-old")
	if !errors.Is(err, domain.ErrSessionExpired) {
		t.Errorf("err = %v, want wrapped domain.ErrSessionExpired", err)
	}
}
