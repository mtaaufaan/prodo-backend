package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// profileRepository -- interface didefinisikan di consumer, lihat §3.9.
type profileRepository interface {
	GetProfile(ctx context.Context, userID string) (*repository.ProfileRecord, error)
	UpdateProfile(ctx context.Context, userID, platformRole, displayName, title, phone, locale string) (*repository.ProfileRecord, error)
	FindProviderSubByUserID(ctx context.Context, userID string) (string, error)
	LogAccountSecurityAction(ctx context.Context, userID, platformRole, action string) error
}

// ProfileService -- GA Pengaturan Akun (Track S4G, desain "GA Pengaturan
// Akun.dc.html"), self-service: profil sendiri (tab Profil), password+MFA
// sendiri (tab Keamanan), preferensi notifikasi sendiri (tab Notifikasi).
// BEDA dari AccountService (admin mengelola akun ORANG LAIN) -- semua method
// di sini beroperasi atas akun pemanggil sendiri.
type ProfileService struct {
	repo     profileRepository
	notif    *repository.NotificationPreferenceRepository
	oidc     keycloak.OIDCClient
	keycloak keycloak.AdminClient
	mfa      *MFAService
	sessions *SessionService
}

func NewProfileService(repo profileRepository, notif *repository.NotificationPreferenceRepository, oidc keycloak.OIDCClient, kc keycloak.AdminClient, mfa *MFAService, sessions *SessionService) *ProfileService {
	return &ProfileService{repo: repo, notif: notif, oidc: oidc, keycloak: kc, mfa: mfa, sessions: sessions}
}

func (s *ProfileService) GetProfile(ctx context.Context, userID string) (*repository.ProfileRecord, error) {
	return s.repo.GetProfile(ctx, userID)
}

func (s *ProfileService) UpdateProfile(ctx context.Context, userID, platformRole, displayName, title, phone, locale string) (*repository.ProfileRecord, error) {
	return s.repo.UpdateProfile(ctx, userID, platformRole, displayName, title, phone, locale)
}

// ChangePassword mengganti password sendiri (tab Keamanan, "GANTI
// PASSWORD"): (1) verifikasi currentPassword lewat Keycloak PasswordGrant --
// SATU-SATUNYA cara memverifikasi password di model Keycloak-delegated ini
// (lihat komentar AuthService), (2) SetPassword ke Keycloak, (3) akhiri
// semua sesi LAIN (desain: "mengakhiri seluruh sesi di perangkat lain") --
// currentJTI dikecualikan supaya sesi yang sedang dipakai request ini tidak
// ikut terputus. domain.ErrInvalidCredentials kalau currentPassword salah.
// Kompleksitas newPassword divalidasi handler SEBELUM memanggil ini (pola
// sama AuthHandler.Activate), bukan di sini.
func (s *ProfileService) ChangePassword(ctx context.Context, userID, platformRole, email, currentJTI, currentPassword, newPassword string) (revokedOtherSessions int, err error) {
	if _, err := s.oidc.PasswordGrant(ctx, email, currentPassword); err != nil {
		if errors.Is(err, keycloak.ErrInvalidGrant) {
			return 0, fmt.Errorf("service.ChangePassword: %w", domain.ErrInvalidCredentials)
		}
		return 0, fmt.Errorf("service.ChangePassword: verify current password: %w", err)
	}

	kcSub, err := s.repo.FindProviderSubByUserID(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("service.ChangePassword: %w", err)
	}
	if err := s.keycloak.SetPassword(ctx, kcSub, newPassword); err != nil {
		return 0, fmt.Errorf("service.ChangePassword: %w", err)
	}

	sessions, err := s.sessions.ListSessions(ctx, userID, currentJTI)
	if err != nil {
		return 0, fmt.Errorf("service.ChangePassword: list sessions for count: %w", err)
	}
	revokedOtherSessions = 0
	for _, sess := range sessions {
		if !sess.IsCurrent {
			revokedOtherSessions++
		}
	}
	if err := s.sessions.RevokeAllSessions(ctx, userID, currentJTI); err != nil {
		return 0, fmt.Errorf("service.ChangePassword: revoke other sessions: %w", err)
	}

	if err := s.repo.LogAccountSecurityAction(ctx, userID, platformRole, "account.password_changed"); err != nil {
		return 0, fmt.Errorf("service.ChangePassword: %w", err)
	}
	return revokedOtherSessions, nil
}

// ResetMFADevice memulai "PINDAHKAN KE PERANGKAT BARU" (tab Keamanan) --
// menerbitkan secret TOTP BARU (menggantikan yang lama, lihat
// MFAService.SetupTOTP) untuk di-scan authenticator app baru. Konsekuensi
// jujur (BEDA dari salinan desain "perangkat lama tetap aktif sampai
// perangkat baru terdaftar" -- SaveTOTPSecret mengganti secret+set
// is_enabled=FALSE seketika, pola yang sama dipakai onboarding S1-06/07
// sejak awal, tidak diubah di sini): perangkat LAMA berhenti berfungsi
// begitu QR baru diterbitkan, sampai OTP dari perangkat baru dikonfirmasi
// lewat ConfirmMFAReset. FE menampilkan copy yang jujur soal ini (bukan
// menyalin toast desain apa adanya) -- lihat catatan implementation_gaps.md.
func (s *ProfileService) ResetMFADevice(ctx context.Context, userID, email string) (*SetupResult, error) {
	return s.mfa.SetupTOTP(ctx, userID, email)
}

// ConfirmMFAReset menyelesaikan "PINDAHKAN KE PERANGKAT BARU": verifikasi
// OTP pertama dari perangkat baru, aktifkan kembali MFA, terbitkan kode
// cadangan baru (VerifyAndEnable SELALU menerbitkan backup codes baru --
// wajar, perangkat lama yang hilang/diganti seharusnya tidak menyisakan
// kode cadangan lama yang masih berlaku).
func (s *ProfileService) ConfirmMFAReset(ctx context.Context, userID, platformRole, otpCode string) (backupCodes []string, err error) {
	ok, codes, err := s.mfa.VerifyAndEnable(ctx, userID, otpCode)
	if err != nil {
		return nil, fmt.Errorf("service.ConfirmMFAReset: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("service.ConfirmMFAReset: %w", domain.ErrInvalidOTP)
	}
	if err := s.repo.LogAccountSecurityAction(ctx, userID, platformRole, "account.mfa_device_reset"); err != nil {
		return nil, fmt.Errorf("service.ConfirmMFAReset: %w", err)
	}
	return codes, nil
}

// RegenerateBackupCodes -- tombol "BUAT ULANG KODE PEMULIHAN" (tab
// Keamanan), TIDAK mengganti secret TOTP (beda dari ResetMFADevice).
func (s *ProfileService) RegenerateBackupCodes(ctx context.Context, userID, platformRole string) ([]string, error) {
	codes, err := s.mfa.RegenerateBackupCodes(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("service.RegenerateBackupCodes: %w", err)
	}
	if err := s.repo.LogAccountSecurityAction(ctx, userID, platformRole, "account.mfa_backup_codes_regenerated"); err != nil {
		return nil, fmt.Errorf("service.RegenerateBackupCodes: %w", err)
	}
	return codes, nil
}

// ListNotificationPreferences -- GET /users/me/notification-preferences.
func (s *ProfileService) ListNotificationPreferences(ctx context.Context, userID string) ([]repository.NotificationPreference, error) {
	return s.notif.ListForGroupAdmin(ctx, userID)
}

// UpdateNotificationPreference -- PATCH /users/me/notification-preferences,
// satu event_type per panggilan (klik toggle EMAIL/PUSH di grid Notifikasi;
// IN-APP terkunci aktif, tidak pernah dikirim dari FE). Menyimpan ketiga
// channel (in_app selalu true) supaya baris tidak pernah kehilangan channel
// lain yang sebelumnya sudah diatur -- lihat komentar
// NotificationPreferenceRepository.Upsert.
func (s *ProfileService) UpdateNotificationPreference(ctx context.Context, userID, platformRole, eventType string, push, email bool) error {
	if err := s.notif.Upsert(ctx, userID, eventType, true, push, email); err != nil {
		return fmt.Errorf("service.UpdateNotificationPreference: %w", err)
	}
	if err := s.repo.LogAccountSecurityAction(ctx, userID, platformRole, "account.notification_preferences_updated"); err != nil {
		return fmt.Errorf("service.UpdateNotificationPreference: %w", err)
	}
	return nil
}
