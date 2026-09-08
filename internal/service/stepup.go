package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/cache"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

// stepUpTTL -- masa berlaku token step-up (S16-04, forward-pull Track S4G):
// SEKALI verifikasi OTP sukses, berlaku untuk 15 menit ke depan (bukan
// per-request) -- selama itu RequireStepUp() meloloskan aksi destruktif apa
// pun yang dipasangi middleware ini, tanpa OTP ulang.
const stepUpTTL = 15 * time.Minute

// stepUpCacheKey -- pola sama session.go ("session:revoked:"+jti), diikat
// ke jti (Claims.RegisteredClaims.ID) BUKAN user_id: token step-up otomatis
// basi begitu sesi/token JWT-nya sendiri basi, tidak perlu cleanup terpisah.
func stepUpCacheKey(jti string) string {
	return "stepup:" + jti
}

// stepUpMFAVerifier -- interface didefinisikan di consumer, diimplementasikan
// *MFAService (reuse penuh VerifyLoginOTP yang sudah dipakai alur login).
type stepUpMFAVerifier interface {
	VerifyLoginOTP(ctx context.Context, userID, code string) (mfaEnabled, valid, usedBackupCode bool, err error)
}

// StepUpService menangani verifikasi step-up (S16-04/05, DG-10): akun WAJIB
// sudah punya MFA aktif -- GA dan Platform Admin (satu-satunya aktor yang
// dipasangi RequireStepUp sejauh ini) memang wajib MFA sejak login, jadi
// domain.ErrMFARequired di sini murni pengaman, seharusnya tidak pernah
// terjadi lewat alur normal (sama pola komentar AuthService.VerifyMFA).
type StepUpService struct {
	mfa   stepUpMFAVerifier
	cache cache.Cache
}

func NewStepUpService(mfa stepUpMFAVerifier, c cache.Cache) *StepUpService {
	return &StepUpService{mfa: mfa, cache: c}
}

// Verify memvalidasi kode OTP (TOTP 6-digit atau kode cadangan, lewat
// MFAService.VerifyLoginOTP) lalu menandai sesi (jti) ini "step-up valid"
// selama stepUpTTL.
func (s *StepUpService) Verify(ctx context.Context, userID, jti, code string) error {
	enabled, valid, _, err := s.mfa.VerifyLoginOTP(ctx, userID, code)
	if err != nil {
		return fmt.Errorf("service.StepUpService.Verify: %w", err)
	}
	if !enabled {
		return fmt.Errorf("service.StepUpService.Verify: %w", domain.ErrMFARequired)
	}
	if !valid {
		return fmt.Errorf("service.StepUpService.Verify: %w", domain.ErrInvalidOTP)
	}
	if err := s.cache.Set(ctx, stepUpCacheKey(jti), "1", stepUpTTL); err != nil {
		return fmt.Errorf("service.StepUpService.Verify: %w", err)
	}
	return nil
}

// HasValidStepUp -- dipanggil middleware.RequireStepUp lewat interface
// middleware.StepUpChecker.
func (s *StepUpService) HasValidStepUp(ctx context.Context, jti string) (bool, error) {
	_, err := s.cache.Get(ctx, stepUpCacheKey(jti))
	if err != nil {
		if errors.Is(err, cache.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("service.StepUpService.HasValidStepUp: %w", err)
	}
	return true, nil
}
