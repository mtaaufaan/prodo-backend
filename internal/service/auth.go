package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// authRepository -- interface didefinisikan di consumer, lihat §3.9.
type authRepository interface {
	FindUserForLogin(ctx context.Context, email string) (*repository.LoginUserRecord, error)
	FindUserIDByProviderSub(ctx context.Context, providerSub string) (string, error)
	FindUserByID(ctx context.Context, userID string) (*repository.LoginUserRecord, error)
	CreateSSOUser(ctx context.Context, email, displayName, providerSub string) (*repository.LoginUserRecord, error)
	RecordLogin(ctx context.Context, userID, platformRole string) error
}

// AuthService menangani login (US-001) lewat model Keycloak-delegated yang
// dikonfirmasi di S1-01/02: password/identitas diverifikasi Keycloak
// sendiri, backend hanya mengecek status akun PRODO dan meneruskan token
// yang diterbitkan Keycloak apa adanya -- tidak pernah menyimpan/
// membandingkan password sendiri.
type AuthService struct {
	repo     authRepository
	oidc     keycloak.OIDCClient
	mfa      *MFAService
	sessions *SessionService
	logger   *zap.Logger
}

func NewAuthService(repo authRepository, oidc keycloak.OIDCClient, mfa *MFAService, sessions *SessionService, logger *zap.Logger) *AuthService {
	return &AuthService{repo: repo, oidc: oidc, mfa: mfa, sessions: sessions, logger: logger}
}

// LoginResult adalah hasil LoginLocal/LoginSSO, dipetakan langsung ke
// response POST /auth/login sukses (API_CONTRACT.md §2).
type LoginResult struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    int
	User         *repository.LoginUserRecord
}

// LoginLocal memverifikasi credential lokal dan mengembalikan token
// Keycloak. Urutan pengecekan: (1) email terdaftar, (2) users.is_active
// (sumber kebenaran PRODO untuk status onboarding, lihat §5.1) sebelum
// menyentuh Keycloak sama sekali -- akun yang belum aktif tidak boleh
// mencoba password meski kebetulan benar, (3) baru verifikasi password ke
// Keycloak. MFA saat login (kalau GA sudah setup MFA) ditangani terpisah
// oleh VerifyMFA (S1-17), dipanggil handler setelah LoginLocal sukses.
func (s *AuthService) LoginLocal(ctx context.Context, email, password string) (*LoginResult, error) {
	user, err := s.repo.FindUserForLogin(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return nil, fmt.Errorf("service.LoginLocal: %w", domain.ErrInvalidCredentials)
		}
		return nil, fmt.Errorf("service.LoginLocal: %w", err)
	}

	if !user.IsActive {
		return nil, fmt.Errorf("service.LoginLocal: %w", domain.ErrAccountInactive)
	}

	tok, err := s.oidc.PasswordGrant(ctx, email, password)
	if err != nil {
		if errors.Is(err, keycloak.ErrInvalidGrant) {
			return nil, fmt.Errorf("service.LoginLocal: %w", domain.ErrInvalidCredentials)
		}
		return nil, fmt.Errorf("service.LoginLocal: %w", err)
	}

	s.logger.Info("login lokal berhasil", zap.String("user_id", user.ID))

	return &LoginResult{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		ExpiresIn:    tok.ExpiresIn,
		User:         user,
	}, nil
}

// VerifyMFA memverifikasi OTP saat login (S1-17, US-001). Kebijakan: Group
// Admin WAJIB punya MFA aktif (kalau belum -> domain.ErrMFARequired --
// seharusnya tidak pernah terjadi lewat alur onboarding normal, S1-06/07
// sudah mewajibkan setup MFA sebelum akun aktif; ini murni pengaman).
// Member yang belum setup MFA -> lolos tanpa OTP (opsional). Kalau MFA
// aktif dan kode salah -> domain.ErrInvalidOTP.
func (s *AuthService) VerifyMFA(ctx context.Context, userID string, isGroupAdmin bool, otpCode string) error {
	enabled, valid, err := s.mfa.VerifyLoginOTP(ctx, userID, otpCode)
	if err != nil {
		return fmt.Errorf("service.VerifyMFA: %w", err)
	}
	if !enabled {
		if isGroupAdmin {
			return fmt.Errorf("service.VerifyMFA: %w", domain.ErrMFARequired)
		}
		return nil
	}
	if !valid {
		return fmt.Errorf("service.VerifyMFA: %w", domain.ErrInvalidOTP)
	}
	return nil
}

// Login adalah orkestrasi penuh POST /auth/login (S1-18, US-001): verifikasi
// credential lokal, lalu MFA kalau berlaku, lalu catat audit trail (S1-20)
// dan sesi (S1-27). Dipanggil handler -- bukan LoginLocal langsung --
// supaya handler tetap tipis (docs/coding-conventions.md). userAgent/ip
// dari header request, dipakai buat catatan device_info sesi (S1-27) --
// bukan bagian dari kredensial, jadi lolos meski kosong (dev/test client).
func (s *AuthService) Login(ctx context.Context, email, password, otpCode, userAgent, ip string) (*LoginResult, error) {
	result, err := s.LoginLocal(ctx, email, password)
	if err != nil {
		return nil, err
	}

	isGroupAdmin := result.User.PlatformRole == string(domain.PlatformRoleGroupAdmin)
	if err := s.VerifyMFA(ctx, result.User.ID, isGroupAdmin, otpCode); err != nil {
		return nil, err
	}

	if err := s.repo.RecordLogin(ctx, result.User.ID, result.User.PlatformRole); err != nil {
		s.logger.Error("login berhasil tapi gagal mencatat audit trail",
			zap.String("user_id", result.User.ID), zap.Error(err))
		return nil, fmt.Errorf("service.Login: %w", err)
	}

	if err := s.sessions.RecordSession(ctx, result.User.ID, result.AccessToken, userAgent, ip); err != nil {
		s.logger.Error("login berhasil tapi gagal mencatat sesi -- fitur multi-device/remote-logout tidak akan melihat sesi ini",
			zap.String("user_id", result.User.ID), zap.Error(err))
		return nil, fmt.Errorf("service.Login: %w", err)
	}

	return result, nil
}

// ssoClaims -- subset claim OIDC standar yang dibutuhkan untuk mapping ke
// akun PRODO (S1-15). Bukan verifikasi ulang token (lihat catatan di
// LoginSSO) -- cukup decode untuk membaca identitas.
type ssoClaims struct {
	jwt.RegisteredClaims
	Email       string `json:"email"`
	Name        string `json:"name"`
	PrefUsrname string `json:"preferred_username"`
}

// LoginSSO menukar authorization code hasil redirect Keycloak menjadi
// token, lalu memetakan identitas ke akun PRODO (S1-15, US-001): kalau
// provider_sub sudah pernah login sebelumnya -> pakai akun existing, kalau
// belum -> auto-create (member, langsung aktif). Klaim identitas (sub,
// email, name) diambil dari ID token TANPA verifikasi signature ulang --
// token ini didapat langsung dari token endpoint Keycloak lewat panggilan
// server-to-server TLS (bukan dari input user), jadi levelnya sama dengan
// access_token di LoginLocal yang juga diteruskan tanpa diperiksa isinya.
// ponytail: kalau nanti authorization code dipertukarkan lewat jalur yang
// tidak lagi server-to-server langsung, verifikasi JWKS wajib ditambahkan
// balik (lihat internal/middleware/auth.go untuk pola JWKS yang sudah ada).
func (s *AuthService) LoginSSO(ctx context.Context, code, redirectURI string) (*LoginResult, error) {
	tok, err := s.oidc.ExchangeAuthorizationCode(ctx, code, redirectURI)
	if err != nil {
		if errors.Is(err, keycloak.ErrInvalidGrant) {
			return nil, fmt.Errorf("service.LoginSSO: %w", domain.ErrInvalidCredentials)
		}
		return nil, fmt.Errorf("service.LoginSSO: %w", err)
	}

	claims := &ssoClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(tok.IDToken, claims); err != nil {
		return nil, fmt.Errorf("service.LoginSSO: decode id_token: %w", err)
	}
	sub := claims.Subject
	displayName := claims.Name
	if displayName == "" {
		displayName = claims.PrefUsrname
	}

	user, err := s.findOrProvisionSSOUser(ctx, sub, claims.Email, displayName)
	if err != nil {
		return nil, fmt.Errorf("service.LoginSSO: %w", err)
	}

	s.logger.Info("login SSO berhasil", zap.String("user_id", user.ID))

	return &LoginResult{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		ExpiresIn:    tok.ExpiresIn,
		User:         user,
	}, nil
}

func (s *AuthService) findOrProvisionSSOUser(ctx context.Context, providerSub, email, displayName string) (*repository.LoginUserRecord, error) {
	userID, err := s.repo.FindUserIDByProviderSub(ctx, providerSub)
	if err == nil {
		return s.repo.FindUserByID(ctx, userID)
	}
	if !errors.Is(err, domain.ErrUserNotFound) {
		return nil, err
	}

	user, err := s.repo.CreateSSOUser(ctx, email, displayName, providerSub)
	if err != nil {
		return nil, err
	}
	s.logger.Info("akun SSO baru dibuat otomatis", zap.String("user_id", user.ID), zap.String("email", email))
	return user, nil
}
