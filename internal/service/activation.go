package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// activationRepository -- interface didefinisikan di consumer, lihat §3.9.
type activationRepository interface {
	FindActivationTarget(ctx context.Context, tokenHash string) (*repository.ActivationTarget, error)
	MarkInvitationAccepted(ctx context.Context, invitationID, userID string) error
	FindMFAVerificationTarget(ctx context.Context, tokenHash string) (*repository.MFAVerificationTarget, error)
	ActivateUser(ctx context.Context, userID string) error
}

// ActivationService menangani langkah pertama aktivasi akun Group Admin
// (S1-06, US-073): validasi token, set password, lalu mewajibkan setup MFA.
// Verifikasi OTP pertama yang benar-benar mengaktifkan akun ada di S1-07
// (belum diimplementasikan).
type ActivationService struct {
	repo     activationRepository
	keycloak keycloak.AdminClient
	mfa      *MFAService
	logger   *zap.Logger
}

func NewActivationService(repo activationRepository, kc keycloak.AdminClient, mfa *MFAService, logger *zap.Logger) *ActivationService {
	return &ActivationService{repo: repo, keycloak: kc, mfa: mfa, logger: logger}
}

// ActivationResult adalah hasil langkah pertama aktivasi -- dikembalikan ke
// handler untuk direspons sebagai QR code setup MFA (bukan access token;
// itu baru diterbitkan setelah OTP diverifikasi di S1-07). TOTPSecret adalah
// fallback "masukkan kunci manual" kalau authenticator app tidak bisa scan
// QR (Set Password.dc.html).
type ActivationResult struct {
	QRCodePNGBase64 string
	TOTPSecret      string
	Email           string
	DisplayName     string
}

// SetPasswordAndInitMFA memvalidasi token aktivasi, menyetel password baru
// di Keycloak (permanen, requiredAction UPDATE_PASSWORD dihapus), lalu
// membuat secret TOTP + QR untuk langkah setup MFA berikutnya. Token
// ditandai accepted di langkah ini juga -- one-time use (US-073 AC), bahkan
// sebelum MFA benar-benar diverifikasi.
func (s *ActivationService) SetPasswordAndInitMFA(ctx context.Context, rawToken, newPassword string) (*ActivationResult, error) {
	target, err := s.repo.FindActivationTarget(ctx, hashActivationToken(rawToken))
	if err != nil {
		return nil, fmt.Errorf("service.SetPasswordAndInitMFA: %w", err)
	}

	if err := s.keycloak.SetPassword(ctx, target.KeycloakSub, newPassword); err != nil {
		return nil, fmt.Errorf("service.SetPasswordAndInitMFA: %w", err)
	}

	setup, err := s.mfa.SetupTOTP(ctx, target.UserID, target.Email)
	if err != nil {
		return nil, fmt.Errorf("service.SetPasswordAndInitMFA: %w", err)
	}

	if err := s.repo.MarkInvitationAccepted(ctx, target.InvitationID, target.UserID); err != nil {
		s.logger.Error("password+TOTP berhasil disetel tapi gagal menandai invitation accepted -- token bisa dipakai ulang",
			zap.String("invitation_id", target.InvitationID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("service.SetPasswordAndInitMFA: %w", err)
	}

	s.logger.Info("password Group Admin disetel, menunggu setup MFA",
		zap.String("user_id", target.UserID),
		zap.String("email", target.Email),
	)

	return &ActivationResult{
		QRCodePNGBase64: setup.QRCodePNGBase64,
		TOTPSecret:      setup.TOTPSecret,
		Email:           target.Email,
		DisplayName:     target.DisplayName,
	}, nil
}

// MFAActivationResult adalah hasil langkah terakhir aktivasi -- backup
// codes cuma pernah dikembalikan SEKALI ini, tidak pernah bisa diambil ulang
// (hanya hash-nya yang disimpan, lihat MFAService.VerifyAndEnable).
type MFAActivationResult struct {
	BackupCodes []string
}

// VerifyMFAAndActivate adalah langkah terakhir onboarding Group Admin
// (S1-07, US-073): mencocokkan OTP pertama terhadap secret TOTP dari S1-06,
// lalu mengaktifkan akun penuh -- users.is_active=TRUE, Keycloak enabled=true
// + requiredActions dikosongkan. Kode salah -> domain.ErrInvalidOTP (tidak
// mengubah state apapun, aman dicoba ulang).
func (s *ActivationService) VerifyMFAAndActivate(ctx context.Context, rawToken, otpCode string) (*MFAActivationResult, error) {
	target, err := s.repo.FindMFAVerificationTarget(ctx, hashActivationToken(rawToken))
	if err != nil {
		return nil, fmt.Errorf("service.VerifyMFAAndActivate: %w", err)
	}

	ok, backupCodes, err := s.mfa.VerifyAndEnable(ctx, target.UserID, otpCode)
	if err != nil {
		return nil, fmt.Errorf("service.VerifyMFAAndActivate: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("service.VerifyMFAAndActivate: %w", domain.ErrInvalidOTP)
	}

	if err := s.repo.ActivateUser(ctx, target.UserID); err != nil {
		return nil, fmt.Errorf("service.VerifyMFAAndActivate: %w", err)
	}

	if err := s.keycloak.EnableUser(ctx, target.KeycloakSub); err != nil {
		s.logger.Error("MFA terverifikasi dan users.is_active sudah TRUE, tapi gagal enable user Keycloak -- akun PRODO aktif tapi tidak bisa login sampai diperbaiki manual",
			zap.String("user_id", target.UserID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("service.VerifyMFAAndActivate: %w", err)
	}

	s.logger.Info("akun Group Admin aktif sepenuhnya",
		zap.String("user_id", target.UserID),
	)
	return &MFAActivationResult{BackupCodes: backupCodes}, nil
}

// hashActivationToken menghitung SHA-256 dari token mentah untuk dicocokkan
// dengan platform_invitations.token_hash -- konsisten dengan hash yang
// disimpan generateActivationToken() di S1-03 (account.go).
func hashActivationToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}
