package service

import (
	"context"
	"errors"
	"fmt"
	"time"

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

	// ListOrgIDsForGroupAdmin -- S3-38, dasar klaim JWT prodo_org_ids.
	ListOrgIDsForGroupAdmin(ctx context.Context, userID string) ([]string, error)

	// CheckIPAllowlist -- S4P-17, implementation_gaps.md IG-20.
	CheckIPAllowlist(ctx context.Context, userID, ip string) (allowed bool, err error)
}

// authEmailSender -- interface didefinisikan di consumer,
// diimplementasikan *EmailService (S4P-16, implementation_gaps.md IG-20).
type authEmailSender interface {
	SendPlatformAdminLoginAlertEmail(ctx context.Context, to, displayName, ip, device string, loginTime time.Time) error
}

// AuthService menangani login (US-001) lewat model Keycloak-delegated yang
// dikonfirmasi di S1-01/02: password/identitas diverifikasi Keycloak
// sendiri, backend hanya mengecek status akun PRODO dan meneruskan token
// yang diterbitkan Keycloak apa adanya -- tidak pernah menyimpan/
// membandingkan password sendiri.
type AuthService struct {
	repo     authRepository
	oidc     keycloak.OIDCClient
	keycloak keycloak.AdminClient
	mfa      *MFAService
	sessions *SessionService
	email    authEmailSender
	logger   *zap.Logger
}

func NewAuthService(repo authRepository, oidc keycloak.OIDCClient, kc keycloak.AdminClient, mfa *MFAService, sessions *SessionService, email authEmailSender, logger *zap.Logger) *AuthService {
	return &AuthService{repo: repo, oidc: oidc, keycloak: kc, mfa: mfa, sessions: sessions, email: email, logger: logger}
}

// LoginResult adalah hasil LoginLocal/LoginSSO, dipetakan langsung ke
// response POST /auth/login sukses (API_CONTRACT.md §2).
type LoginResult struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    int
	User         *repository.LoginUserRecord
	// BackupCodes cuma terisi sekali, tepat setelah MFA baru diaktifkan
	// (CompletePlatformAdminMFASetup, S4P-14/19) -- nil di login normal.
	BackupCodes []string
}

// MFASetupChallenge diterbitkan AuthService.Login saat Platform Admin
// login tapi belum punya MFA aktif (S4P-14, implementation_gaps.md IG-20).
// FE (PlatformLoginPage, S4P-19) menampilkan QR/secret ini, lalu POST
// /auth/platform/mfa-setup/verify dengan email/password/otp_code yang sama
// untuk menyelesaikan setup DAN login sekaligus.
type MFASetupChallenge struct {
	Email           string
	QRCodePNGBase64 string
	TOTPSecret      string
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

	// S4P-02 (US-067): suspended_at dicek SEBELUM is_active -- akun yang
	// pernah aktif lalu disuspend PA harus dapat pesan "hubungi Platform
	// Admin", bukan "selesaikan aktivasi" (is_active tidak disentuh oleh
	// suspend, jadi kalau urutannya dibalik pesan yang salah yang muncul).
	if user.SuspendedAt != nil {
		return nil, fmt.Errorf("service.LoginLocal: %w", domain.ErrAccountSuspended)
	}
	if !user.IsActive {
		return nil, fmt.Errorf("service.LoginLocal: %w", domain.ErrAccountInactive)
	}

	// S3-38 (implementation_gaps.md IG-14): sinkron attribute Keycloak
	// SEBELUM menukar credential ke token -- attribute yang diubah SESUDAH
	// token diterbitkan tidak akan tercermin di token itu (perlu terbit
	// ulang). Kegagalan sinkron TIDAK memblokir login: token yang
	// dihasilkan cuma bawa klaim basi (persis kondisi sebelum S3-38 ada),
	// bukan gagal total -- login tetap harus jalan meski Admin REST API
	// Keycloak sedang bermasalah.
	if err := s.syncKeycloakClaims(ctx, user); err != nil {
		s.logger.Error("gagal sinkron attribute Keycloak sebelum login -- token mungkin bawa role/org basi",
			zap.String("user_id", user.ID), zap.Error(err))
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

// syncKeycloakClaims menyetel attribute Keycloak user yang jadi sumber
// protocol mapper custom (S3-37/38, implementation_gaps.md IG-14) supaya
// klaim JWT prodo_platform_role/prodo_org_ids selalu mencerminkan state
// Postgres terkini pada saat token diterbitkan. prodo_org_ids cuma dihitung
// untuk Group Admin -- role lain tidak butuh, dan grup_admin_assignments
// cuma relevan untuk GA.
func (s *AuthService) syncKeycloakClaims(ctx context.Context, user *repository.LoginUserRecord) error {
	attrs := map[string][]string{"prodo_platform_role": {user.PlatformRole}}

	if user.PlatformRole == string(domain.PlatformRoleGroupAdmin) {
		orgIDs, err := s.repo.ListOrgIDsForGroupAdmin(ctx, user.ID)
		if err != nil {
			return fmt.Errorf("service.syncKeycloakClaims: resolve org_ids GA: %w", err)
		}
		attrs["prodo_org_ids"] = orgIDs
	}

	if err := s.keycloak.SetUserAttributes(ctx, user.KeycloakUserID, attrs); err != nil {
		return fmt.Errorf("service.syncKeycloakClaims: %w", err)
	}
	return nil
}

// VerifyMFA memverifikasi OTP saat login (S1-17, US-001; S4P-14 untuk
// Platform Admin). Kebijakan: Group Admin WAJIB punya MFA aktif (kalau
// belum -> domain.ErrMFARequired -- seharusnya tidak pernah terjadi lewat
// alur onboarding normal, S1-06/07 sudah mewajibkan setup MFA sebelum akun
// aktif; ini murni pengaman). Platform Admin JUGA wajib MFA, tapi BEDA
// perlakuan -- akun PA tidak melalui alur invite+aktivasi seperti GA, jadi
// "belum ada MFA" adalah kondisi NORMAL untuk login pertama, bukan state
// tidak konsisten -> domain.ErrMFASetupRequired (caller menerbitkan
// tantangan setup, bukan menolak). Member yang belum setup MFA -> lolos
// tanpa OTP (opsional). Kalau MFA aktif dan kode salah -> domain.ErrInvalidOTP.
func (s *AuthService) VerifyMFA(ctx context.Context, userID string, isGroupAdmin, isPlatformAdmin bool, otpCode string) error {
	enabled, valid, err := s.mfa.VerifyLoginOTP(ctx, userID, otpCode)
	if err != nil {
		return fmt.Errorf("service.VerifyMFA: %w", err)
	}
	if !enabled {
		switch {
		case isGroupAdmin:
			return fmt.Errorf("service.VerifyMFA: %w", domain.ErrMFARequired)
		case isPlatformAdmin:
			return fmt.Errorf("service.VerifyMFA: %w", domain.ErrMFASetupRequired)
		default:
			return nil
		}
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
func (s *AuthService) Login(ctx context.Context, email, password, otpCode, userAgent, ip string) (*LoginResult, *MFASetupChallenge, error) {
	result, err := s.LoginLocal(ctx, email, password)
	if err != nil {
		return nil, nil, err
	}

	isGroupAdmin := result.User.PlatformRole == string(domain.PlatformRoleGroupAdmin)
	isPlatformAdmin := result.User.PlatformRole == string(domain.PlatformRoleAdmin)
	if isPlatformAdmin {
		if err := s.checkPAIPAllowlist(ctx, result.User.ID, ip); err != nil {
			return nil, nil, err
		}
	}
	if err := s.VerifyMFA(ctx, result.User.ID, isGroupAdmin, isPlatformAdmin, otpCode); err != nil {
		if errors.Is(err, domain.ErrMFASetupRequired) {
			challenge, setupErr := s.mfa.SetupTOTP(ctx, result.User.ID, result.User.Email)
			if setupErr != nil {
				return nil, nil, fmt.Errorf("service.Login: init MFA setup PA: %w", setupErr)
			}
			return nil, &MFASetupChallenge{
				Email:           result.User.Email,
				QRCodePNGBase64: challenge.QRCodePNGBase64,
				TOTPSecret:      challenge.TOTPSecret,
			}, err
		}
		return nil, nil, err
	}

	if err := s.repo.RecordLogin(ctx, result.User.ID, result.User.PlatformRole); err != nil {
		s.logger.Error("login berhasil tapi gagal mencatat audit trail",
			zap.String("user_id", result.User.ID), zap.Error(err))
		return nil, nil, fmt.Errorf("service.Login: %w", err)
	}

	if err := s.sessions.RecordSession(ctx, result.User.ID, result.AccessToken, userAgent, ip); err != nil {
		s.logger.Error("login berhasil tapi gagal mencatat sesi -- fitur multi-device/remote-logout tidak akan melihat sesi ini",
			zap.String("user_id", result.User.ID), zap.Error(err))
		return nil, nil, fmt.Errorf("service.Login: %w", err)
	}

	if isPlatformAdmin {
		s.sendPlatformAdminLoginAlert(ctx, result.User, userAgent, ip)
	}

	return result, nil, nil
}

// sendPlatformAdminLoginAlert (S4P-16, implementation_gaps.md IG-20) --
// best-effort, TIDAK menggagalkan login kalau SMTP gagal (beda dari
// RecordLogin/RecordSession di atas yang memang harus gagal -- keduanya
// audit/keamanan inti, ini cuma notifikasi tambahan).
func (s *AuthService) sendPlatformAdminLoginAlert(ctx context.Context, user *repository.LoginUserRecord, userAgent, ip string) {
	browser, os := parseUserAgent(userAgent)
	device := browser
	if os != "" {
		device = fmt.Sprintf("%s di %s", browser, os)
	}
	if err := s.email.SendPlatformAdminLoginAlertEmail(ctx, user.Email, user.DisplayName, ip, device, time.Now()); err != nil {
		s.logger.Error("login Platform Admin sukses tapi gagal kirim email alert",
			zap.String("user_id", user.ID), zap.Error(err))
	}
}

// checkPAIPAllowlist (S4P-17, implementation_gaps.md IG-20): ip kosong
// (dev/test client tanpa header nyata) LOLOS tanpa mengecek -- sama pola
// toleransi yang sudah ada untuk userAgent/ip di RecordSession, dan
// $1::inet tidak bisa cast string kosong sama sekali (akan error, bukan
// ditolak dengan wajar).
func (s *AuthService) checkPAIPAllowlist(ctx context.Context, userID, ip string) error {
	if ip == "" {
		return nil
	}
	allowed, err := s.repo.CheckIPAllowlist(ctx, userID, ip)
	if err != nil {
		return fmt.Errorf("service.checkPAIPAllowlist: %w", err)
	}
	if !allowed {
		return fmt.Errorf("service.checkPAIPAllowlist: %w", domain.ErrIPNotAllowed)
	}
	return nil
}

// CompletePlatformAdminMFASetup menyelesaikan setup MFA Platform Admin yang
// dimulai lewat MFASetupChallenge (S4P-14/19) DAN langsung menerbitkan token
// login -- menghindari PA harus login dua kali (setup lalu login ulang).
// Re-verifikasi email/password (bukan cuma percaya userID dari challenge
// sebelumnya) karena tidak ada sesi/token yang mengikat request setup ke
// request login pertama; keduanya request terpisah tanpa state di server.
func (s *AuthService) CompletePlatformAdminMFASetup(ctx context.Context, email, password, otpCode, userAgent, ip string) (*LoginResult, error) {
	result, err := s.LoginLocal(ctx, email, password)
	if err != nil {
		return nil, err
	}
	if result.User.PlatformRole != string(domain.PlatformRoleAdmin) {
		return nil, fmt.Errorf("service.CompletePlatformAdminMFASetup: %w", domain.ErrInvalidCredentials)
	}
	if err := s.checkPAIPAllowlist(ctx, result.User.ID, ip); err != nil {
		return nil, err
	}

	ok, backupCodes, err := s.mfa.VerifyAndEnable(ctx, result.User.ID, otpCode)
	if err != nil {
		return nil, fmt.Errorf("service.CompletePlatformAdminMFASetup: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("service.CompletePlatformAdminMFASetup: %w", domain.ErrInvalidOTP)
	}

	if err := s.repo.RecordLogin(ctx, result.User.ID, result.User.PlatformRole); err != nil {
		s.logger.Error("setup MFA PA berhasil tapi gagal mencatat audit trail",
			zap.String("user_id", result.User.ID), zap.Error(err))
		return nil, fmt.Errorf("service.CompletePlatformAdminMFASetup: %w", err)
	}
	if err := s.sessions.RecordSession(ctx, result.User.ID, result.AccessToken, userAgent, ip); err != nil {
		s.logger.Error("setup MFA PA berhasil tapi gagal mencatat sesi",
			zap.String("user_id", result.User.ID), zap.Error(err))
		return nil, fmt.Errorf("service.CompletePlatformAdminMFASetup: %w", err)
	}

	s.sendPlatformAdminLoginAlert(ctx, result.User, userAgent, ip)

	result.BackupCodes = backupCodes
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
