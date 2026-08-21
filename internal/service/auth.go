package service

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// authRepository -- interface didefinisikan di consumer, lihat §3.9.
type authRepository interface {
	FindUserForLogin(ctx context.Context, email string) (*repository.LoginUserRecord, error)
}

// AuthService menangani login credential lokal (S1-14, US-001) lewat model
// Keycloak-delegated: password diverifikasi Keycloak sendiri (Direct Access
// Grant), backend hanya mengecek status akun (users.is_active) dan
// meneruskan token yang diterbitkan Keycloak apa adanya -- tidak pernah
// menyimpan atau membandingkan password sendiri.
type AuthService struct {
	repo   authRepository
	ropc   keycloak.ROPCClient
	logger *zap.Logger
}

func NewAuthService(repo authRepository, ropc keycloak.ROPCClient, logger *zap.Logger) *AuthService {
	return &AuthService{repo: repo, ropc: ropc, logger: logger}
}

// LoginResult adalah hasil LoginLocal, dipetakan langsung ke response
// POST /auth/login sukses (API_CONTRACT.md §2).
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

	tok, err := s.ropc.PasswordGrant(ctx, email, password)
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
