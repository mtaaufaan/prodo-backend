// Package service holds business logic.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// groupAdminInvitationTTL adalah masa berlaku activation link (US-073 AC).
const groupAdminInvitationTTL = 72 * time.Hour

// accountRepository -- interface didefinisikan di sisi consumer (service)
// supaya unit test bisa pakai fake tanpa DB nyata, lihat docs/coding-conventions.md §3.9.
type accountRepository interface {
	CreateGroupAdminInvitation(ctx context.Context, p *repository.CreateGroupAdminInvitationParams) (userID string, err error)
	FindUserIDByProviderSub(ctx context.Context, providerSub string) (userID string, err error)
	FindUserContactByID(ctx context.Context, userID string) (*repository.UserContact, error)
	RegenerateInvitationToken(ctx context.Context, targetUserID, email, newTokenHash string, newExpiresAt time.Time, actorUserID string) error
	ListGroupAdmins(ctx context.Context, limit, offset int) ([]repository.GroupAdminSummary, int, error)
}

type AccountService struct {
	repo     accountRepository
	keycloak keycloak.AdminClient
	logger   *zap.Logger
}

func NewAccountService(repo accountRepository, kc keycloak.AdminClient, logger *zap.Logger) *AccountService {
	return &AccountService{repo: repo, keycloak: kc, logger: logger}
}

// CreateGroupAdminRequest adalah input pembuatan akun Group Admin oleh
// Platform Admin (US-073). Field "organisasi yang akan dikelola" pada
// acceptance criteria BELUM ditangani -- tabel groups/organizations belum
// ada di scope S1 (pola sama dengan FK yang di-deferred di S1-01/02,
// group_admin_assignments per docs/DATABASE_SCHEMA.md §5.6 baru bisa diisi
// setelah tabel groups dibuat).
type CreateGroupAdminRequest struct {
	Email           string
	DisplayName     string
	InvitedByUserID string // Platform Admin yang melakukan invite
}

// GroupAdminInvitation adalah hasil pembuatan akun. ActivationToken cuma
// terisi (raw, belum di-hash) tepat setelah pembuatan -- dipakai S1-04
// untuk mengirim email lalu dibuang; yang tersimpan permanen di DB cuma
// hash-nya (platform_invitations.token_hash).
type GroupAdminInvitation struct {
	UserID          string
	Email           string
	DisplayName     string
	ActivationToken string
	ExpiresAt       time.Time
}

// ListGroupAdmins mengembalikan daftar Group Admin untuk panel Platform
// Admin (S1-12).
func (s *AccountService) ListGroupAdmins(ctx context.Context, limit, offset int) ([]repository.GroupAdminSummary, int, error) {
	summaries, total, err := s.repo.ListGroupAdmins(ctx, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("service.ListGroupAdmins: %w", err)
	}
	return summaries, total, nil
}

// ResolveActorUserID mencari users.id internal dari Keycloak subject (klaim
// JWT "sub") -- dipakai handler untuk mengisi invited_by/actor_id dari
// Platform Admin yang sedang login (S1-05).
func (s *AccountService) ResolveActorUserID(ctx context.Context, keycloakSub string) (string, error) {
	userID, err := s.repo.FindUserIDByProviderSub(ctx, keycloakSub)
	if err != nil {
		return "", fmt.Errorf("service.ResolveActorUserID: %w", err)
	}
	return userID, nil
}

// CreateGroupAdmin membuat user Keycloak (disabled, wajib set password +
// MFA saat aktivasi), lalu menyimpan users/user_auth_providers/
// platform_invitations/audit_logs dalam satu transaksi.
//
// ponytail: kalau insert DB gagal setelah user Keycloak berhasil dibuat,
// user Keycloak itu jadi orphan (tidak ada compensating transaction/saga) --
// cukup di-log sebagai error supaya bisa dibersihkan manual. Upgrade ke
// cleanup otomatis kalau ini jadi masalah operasional nyata.
func (s *AccountService) CreateGroupAdmin(ctx context.Context, req CreateGroupAdminRequest) (*GroupAdminInvitation, error) {
	if req.Email == "" || req.DisplayName == "" || req.InvitedByUserID == "" {
		return nil, fmt.Errorf("service.CreateGroupAdmin: %w", domain.ErrInvalidInput)
	}

	kcUserID, err := s.keycloak.CreateDisabledUser(ctx, req.Email, req.DisplayName)
	if err != nil {
		if errors.Is(err, keycloak.ErrUserAlreadyExists) {
			return nil, fmt.Errorf("service.CreateGroupAdmin: %w", domain.ErrEmailAlreadyExists)
		}
		return nil, fmt.Errorf("service.CreateGroupAdmin: %w", err)
	}

	rawToken, tokenHash, err := generateActivationToken()
	if err != nil {
		return nil, fmt.Errorf("service.CreateGroupAdmin: %w", err)
	}

	expiresAt := time.Now().Add(groupAdminInvitationTTL)

	userID, err := s.repo.CreateGroupAdminInvitation(ctx, &repository.CreateGroupAdminInvitationParams{
		Email:           req.Email,
		DisplayName:     req.DisplayName,
		KeycloakUserID:  kcUserID,
		TokenHash:       tokenHash,
		ExpiresAt:       expiresAt,
		InvitedByUserID: req.InvitedByUserID,
	})
	if err != nil {
		s.logger.Error("gagal simpan invitation Group Admin setelah user Keycloak dibuat -- kemungkinan orphan, perlu cleanup manual",
			zap.String("email", req.Email),
			zap.String("keycloak_user_id", kcUserID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("service.CreateGroupAdmin: %w", err)
	}

	s.logger.Info("akun Group Admin dibuat, menunggu aktivasi",
		zap.String("user_id", userID),
		zap.String("email", req.Email),
		zap.Time("expires_at", expiresAt),
	)

	return &GroupAdminInvitation{
		UserID:          userID,
		Email:           req.Email,
		DisplayName:     req.DisplayName,
		ActivationToken: rawToken,
		ExpiresAt:       expiresAt,
	}, nil
}

// ResendActivation meng-invalidate token aktivasi lama dan menerbitkan yang
// baru (S1-08) -- dipakai Platform Admin saat Group Admin belum sempat
// mengaktifkan akun sebelum link lama kedaluwarsa/hilang. Tidak menyentuh
// Keycloak sama sekali (user Keycloak-nya sudah ada sejak S1-03 invite-time).
func (s *AccountService) ResendActivation(ctx context.Context, targetUserID, actorUserID string) (*GroupAdminInvitation, error) {
	contact, err := s.repo.FindUserContactByID(ctx, targetUserID)
	if err != nil {
		return nil, fmt.Errorf("service.ResendActivation: %w", err)
	}

	rawToken, tokenHash, err := generateActivationToken()
	if err != nil {
		return nil, fmt.Errorf("service.ResendActivation: %w", err)
	}
	expiresAt := time.Now().Add(groupAdminInvitationTTL)

	if err := s.repo.RegenerateInvitationToken(ctx, targetUserID, contact.Email, tokenHash, expiresAt, actorUserID); err != nil {
		return nil, fmt.Errorf("service.ResendActivation: %w", err)
	}

	s.logger.Info("token aktivasi Group Admin diterbitkan ulang",
		zap.String("user_id", targetUserID),
		zap.String("email", contact.Email),
		zap.Time("expires_at", expiresAt),
	)

	return &GroupAdminInvitation{
		UserID:          targetUserID,
		Email:           contact.Email,
		DisplayName:     contact.DisplayName,
		ActivationToken: rawToken,
		ExpiresAt:       expiresAt,
	}, nil
}

// UserExists mengecek keberadaan users.id -- dipakai handler force-logout
// (S1-35) untuk membalas 404 USER_NOT_FOUND sebelum memanggil SessionService,
// karena RevokeAllSessions sendiri tidak membedakan "user tidak ada" dari
// "user ada tapi tidak punya sesi aktif" (keduanya sama-sama 0 baris ter-revoke).
func (s *AccountService) UserExists(ctx context.Context, userID string) error {
	if _, err := s.repo.FindUserContactByID(ctx, userID); err != nil {
		return fmt.Errorf("service.UserExists: %w", err)
	}
	return nil
}

// GetDisplayName -- dipakai handler undangan workspace (S2-19/22) untuk
// isi email "X mengundang Anda..." dengan nama tampilan actor, bukan UUID.
func (s *AccountService) GetDisplayName(ctx context.Context, userID string) (string, error) {
	contact, err := s.repo.FindUserContactByID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("service.GetDisplayName: %w", err)
	}
	return contact.DisplayName, nil
}

// generateActivationToken menghasilkan token acak (256-bit) untuk dikirim
// ke user lewat email, dan hash SHA-256-nya untuk disimpan di DB -- token
// mentah tidak pernah disimpan (kalau DB bocor, token tidak bisa dipakai
// ulang tanpa reverse hash).
func generateActivationToken() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generateActivationToken: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(sum[:]), nil
}
