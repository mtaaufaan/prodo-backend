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
	"math"
	"net"
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

	// SuspendGroupAdmin/ReactivateGroupAdmin -- S4P-02, US-067.
	SuspendGroupAdmin(ctx context.Context, targetUserID, actorUserID string) error
	ReactivateGroupAdmin(ctx context.Context, targetUserID, actorUserID string) error

	// TransferGroup/DeleteGroupAdmin -- S4P-03/04/05, IG-21.
	TransferGroup(ctx context.Context, fromUserID, toUserID, actorUserID string) (transferredGroupCount int, err error)
	DeleteGroupAdmin(ctx context.Context, targetUserID, actorUserID string) error

	// GetGroupAdminDetail/UpdateGroupAdmin -- S4P-06/07, form "Lihat"/"Ubah"
	// Group Admin lengkap sesuai desain.
	GetGroupAdminDetail(ctx context.Context, targetUserID string) (*repository.GroupAdminSummary, error)
	UpdateGroupAdmin(ctx context.Context, targetUserID string, p *repository.UpdateGroupAdminParams, actorUserID string) (oldTier string, err error)

	// Katalog tier + lifecycle (S4P-07/11): ListServiceTiers/
	// FindActiveServiceTierIDByName untuk assign tier ke GA; Create/Update/
	// Deactivate/Reactivate/Archive/Unarchive/Delete untuk halaman "Tier &
	// Kuota Global".
	ListServiceTiers(ctx context.Context, includeArchived bool) ([]repository.ServiceTier, error)
	FindActiveServiceTierIDByName(ctx context.Context, name string) (string, error)
	CreateServiceTier(ctx context.Context, p *repository.ServiceTierParams, actorUserID string) (id string, err error)
	UpdateServiceTier(ctx context.Context, id string, p *repository.ServiceTierParams, actorUserID string) error
	DeactivateServiceTier(ctx context.Context, id, actorUserID string) error
	ReactivateServiceTier(ctx context.Context, id, actorUserID string) error
	ArchiveServiceTier(ctx context.Context, id, actorUserID string) error
	UnarchiveServiceTier(ctx context.Context, id, actorUserID string) error
	DeleteServiceTier(ctx context.Context, id, actorUserID string) error

	// GetPASessionIdleTimeoutSeconds/SetPASessionIdleTimeoutSeconds --
	// S4P-18, dibalik jadi PER-AKUN 2026-08-29 (dikonfirmasi user).
	GetPASessionIdleTimeoutSeconds(ctx context.Context, userID string) (int, error)
	SetPASessionIdleTimeoutSeconds(ctx context.Context, seconds int, userID string) error

	// GetIPAllowlistEnabled/SetIPAllowlistEnabled -- flag global terpisah
	// dari isi daftar (dikonfirmasi user 2026-08-29).
	GetIPAllowlistEnabled(ctx context.Context) (bool, error)
	SetIPAllowlistEnabled(ctx context.Context, enabled bool, actorUserID string) error

	// ListIPAllowlist/AddIPAllowlistEntry/DeleteIPAllowlistEntry -- S4P-18,
	// dibalik jadi GLOBAL 2026-08-29 (dikonfirmasi user): berlaku untuk
	// semua akun Platform Admin, bukan lagi per-akun.
	ListIPAllowlist(ctx context.Context) ([]repository.IPAllowlistEntry, error)
	AddIPAllowlistEntry(ctx context.Context, cidr, actorUserID string) (id string, err error)
	DeleteIPAllowlistEntry(ctx context.Context, entryID, actorUserID string) error

	// CreatePlatformAdminInvitation/ListPlatformAdmins/DeactivatePlatformAdmin/
	// ReactivatePlatformAdmin/ResetPlatformAdminMFA -- S4P-37/38/39/40, US-084.
	CreatePlatformAdminInvitation(ctx context.Context, p *repository.CreatePlatformAdminInvitationParams) (userID string, err error)
	ListPlatformAdmins(ctx context.Context) ([]repository.PlatformAdminSummary, error)
	DeactivatePlatformAdmin(ctx context.Context, targetUserID, actorUserID string) error
	ReactivatePlatformAdmin(ctx context.Context, targetUserID, actorUserID string) error
	ResetPlatformAdminMFA(ctx context.Context, targetUserID, actorUserID string) error
}

// minPASessionIdleTimeout -- US-070 AC: "dapat dikonfigurasi ... minimum 10
// menit" (S4P-18).
const minPASessionIdleTimeout = 10 * time.Minute

type AccountService struct {
	repo     accountRepository
	keycloak keycloak.AdminClient
	logger   *zap.Logger
}

func NewAccountService(repo accountRepository, kc keycloak.AdminClient, logger *zap.Logger) *AccountService {
	return &AccountService{repo: repo, keycloak: kc, logger: logger}
}

// defaultServiceTierName -- tier yang dipakai kalau request tidak
// menyertakan tier_id (S4P-07/11).
const defaultServiceTierName = "starter"

// CreateGroupAdminRequest adalah input pembuatan akun Group Admin oleh
// Platform Admin (US-073). GroupName WAJIB (IG-21, sesuai desain "Tambah
// Group Admin" -- field "Nama Perusahaan / Grup") -- setiap GA baru
// langsung mengelola satu grup baru, tidak ada lagi GA "yatim" seperti
// sejak S1-05. JobTitle/Address/Phone opsional (kontak PIC). TierID kosong
// berarti pakai tier default "starter" (S4P-07); StorageQuotaGB nil
// berarti pakai plafon default tier tsb. TierID sejak S4P-11 -- bukan lagi
// nama tier, supaya rename tier tidak memutus request lama.
type CreateGroupAdminRequest struct {
	Email           string
	DisplayName     string
	GroupName       string
	JobTitle        string
	Address         string
	Phone           string
	TierID          string
	StorageQuotaGB  *int
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
func (s *AccountService) CreateGroupAdmin(ctx context.Context, req *CreateGroupAdminRequest) (*GroupAdminInvitation, error) {
	if req.Email == "" || req.DisplayName == "" || req.GroupName == "" || req.InvitedByUserID == "" {
		return nil, fmt.Errorf("service.CreateGroupAdmin: %w", domain.ErrInvalidInput)
	}
	if req.TierID == "" {
		tierID, err := s.repo.FindActiveServiceTierIDByName(ctx, defaultServiceTierName)
		if err != nil {
			return nil, fmt.Errorf("service.CreateGroupAdmin: resolve default tier: %w", err)
		}
		req.TierID = tierID
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
		GroupName:       req.GroupName,
		JobTitle:        req.JobTitle,
		Address:         req.Address,
		Phone:           req.Phone,
		TierID:          req.TierID,
		StorageQuotaGB:  req.StorageQuotaGB,
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

// SuspendGroupAdmin menonaktifkan akun Group Admin (S4P-02, US-067) --
// tidak menghapus/mengubah data lain, murni memblokir login sampai
// direaktivasi.
func (s *AccountService) SuspendGroupAdmin(ctx context.Context, targetUserID, actorUserID string) error {
	if err := s.repo.SuspendGroupAdmin(ctx, targetUserID, actorUserID); err != nil {
		return fmt.Errorf("service.SuspendGroupAdmin: %w", err)
	}
	return nil
}

// ReactivateGroupAdmin mengaktifkan kembali akun Group Admin yang
// sebelumnya disuspend (S4P-02, US-067).
func (s *AccountService) ReactivateGroupAdmin(ctx context.Context, targetUserID, actorUserID string) error {
	if err := s.repo.ReactivateGroupAdmin(ctx, targetUserID, actorUserID); err != nil {
		return fmt.Errorf("service.ReactivateGroupAdmin: %w", err)
	}
	return nil
}

// PlatformAdminInvitation adalah hasil pembuatan akun PA baru (S4P-37,
// US-084). ActivationToken cuma terisi (raw) tepat setelah pembuatan --
// dipakai untuk kirim email lalu dibuang, sama pola GroupAdminInvitation.
type PlatformAdminInvitation struct {
	UserID          string
	Email           string
	DisplayName     string
	ActivationToken string
	ExpiresAt       time.Time
}

// CreatePlatformAdmin membuat akun Platform Admin baru oleh Platform
// Admin lain (S4P-37, US-084) -- pola sama dengan CreateGroupAdmin
// (Keycloak disabled user + token aktivasi + platform_invitations), TAPI
// jauh lebih sederhana karena PA tidak punya konsep grup/tier/kuota.
// Aktivasi (set password + setup MFA) memakai ActivationService yang
// SAMA persis dengan Group Admin.
func (s *AccountService) CreatePlatformAdmin(ctx context.Context, email, displayName, invitedByUserID string) (*PlatformAdminInvitation, error) {
	if email == "" || displayName == "" || invitedByUserID == "" {
		return nil, fmt.Errorf("service.CreatePlatformAdmin: %w", domain.ErrInvalidInput)
	}

	kcUserID, err := s.keycloak.CreateDisabledUser(ctx, email, displayName)
	if err != nil {
		if errors.Is(err, keycloak.ErrUserAlreadyExists) {
			return nil, fmt.Errorf("service.CreatePlatformAdmin: %w", domain.ErrEmailAlreadyExists)
		}
		return nil, fmt.Errorf("service.CreatePlatformAdmin: %w", err)
	}

	rawToken, tokenHash, err := generateActivationToken()
	if err != nil {
		return nil, fmt.Errorf("service.CreatePlatformAdmin: %w", err)
	}
	expiresAt := time.Now().Add(groupAdminInvitationTTL)

	userID, err := s.repo.CreatePlatformAdminInvitation(ctx, &repository.CreatePlatformAdminInvitationParams{
		Email:           email,
		DisplayName:     displayName,
		KeycloakUserID:  kcUserID,
		TokenHash:       tokenHash,
		ExpiresAt:       expiresAt,
		InvitedByUserID: invitedByUserID,
	})
	if err != nil {
		s.logger.Error("gagal simpan invitation Platform Admin setelah user Keycloak dibuat -- kemungkinan orphan, perlu cleanup manual",
			zap.String("email", email),
			zap.String("keycloak_user_id", kcUserID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("service.CreatePlatformAdmin: %w", err)
	}

	return &PlatformAdminInvitation{
		UserID:          userID,
		Email:           email,
		DisplayName:     displayName,
		ActivationToken: rawToken,
		ExpiresAt:       expiresAt,
	}, nil
}

// ListPlatformAdmins -- S4P-40.
func (s *AccountService) ListPlatformAdmins(ctx context.Context) ([]repository.PlatformAdminSummary, error) {
	admins, err := s.repo.ListPlatformAdmins(ctx)
	if err != nil {
		return nil, fmt.Errorf("service.ListPlatformAdmins: %w", err)
	}
	return admins, nil
}

// DeactivatePlatformAdmin -- S4P-38. Guard "bukan diri sendiri" DI SINI
// (bukan repo) supaya pesan error jelas tanpa perlu round-trip DB; guard
// "minimal satu PA aktif tersisa" ada di repo (butuh query, dan harus
// atomik dalam transaksi yang sama dengan UPDATE-nya).
func (s *AccountService) DeactivatePlatformAdmin(ctx context.Context, targetUserID, actorUserID string) error {
	if targetUserID == actorUserID {
		return fmt.Errorf("service.DeactivatePlatformAdmin: %w", domain.ErrCannotDeactivateSelf)
	}
	if err := s.repo.DeactivatePlatformAdmin(ctx, targetUserID, actorUserID); err != nil {
		return fmt.Errorf("service.DeactivatePlatformAdmin: %w", err)
	}
	return nil
}

// ReactivatePlatformAdmin -- S4P-38 (tambahan, dikonfirmasi user).
func (s *AccountService) ReactivatePlatformAdmin(ctx context.Context, targetUserID, actorUserID string) error {
	if err := s.repo.ReactivatePlatformAdmin(ctx, targetUserID, actorUserID); err != nil {
		return fmt.Errorf("service.ReactivatePlatformAdmin: %w", err)
	}
	return nil
}

// ResetPlatformAdminMFA -- S4P-39. Guard "bukan diri sendiri" di sini,
// pola sama DeactivatePlatformAdmin.
func (s *AccountService) ResetPlatformAdminMFA(ctx context.Context, targetUserID, actorUserID string) error {
	if targetUserID == actorUserID {
		return fmt.Errorf("service.ResetPlatformAdminMFA: %w", domain.ErrCannotResetOwnMFA)
	}
	if err := s.repo.ResetPlatformAdminMFA(ctx, targetUserID, actorUserID); err != nil {
		return fmt.Errorf("service.ResetPlatformAdminMFA: %w", err)
	}
	return nil
}

// TransferGroup memindahkan pengelolaan seluruh grup dari satu GA ke GA
// lain (S4P-03/04, IG-21).
func (s *AccountService) TransferGroup(ctx context.Context, fromUserID, toUserID, actorUserID string) (int, error) {
	count, err := s.repo.TransferGroup(ctx, fromUserID, toUserID, actorUserID)
	if err != nil {
		return 0, fmt.Errorf("service.TransferGroup: %w", err)
	}
	return count, nil
}

// GetGroupAdminDetail -- S4P-06, mode "Lihat"/"Ubah".
func (s *AccountService) GetGroupAdminDetail(ctx context.Context, targetUserID string) (*repository.GroupAdminSummary, error) {
	detail, err := s.repo.GetGroupAdminDetail(ctx, targetUserID)
	if err != nil {
		return nil, fmt.Errorf("service.GetGroupAdminDetail: %w", err)
	}
	return detail, nil
}

// UpdateGroupAdmin -- S4P-06, form "Ubah Group Admin". Validasi tier dan
// status di sini (bukan cuma di repository) supaya pesan error konsisten
// dengan CreateGroupAdmin.
func (s *AccountService) UpdateGroupAdmin(ctx context.Context, targetUserID string, p *repository.UpdateGroupAdminParams, actorUserID string) (string, error) {
	if p.DisplayName == "" || p.GroupName == "" || p.TierID == "" {
		return "", fmt.Errorf("service.UpdateGroupAdmin: %w", domain.ErrInvalidInput)
	}
	if p.NewStatus != "" && p.NewStatus != "AKTIF" && p.NewStatus != "SUSPENDED" {
		return "", fmt.Errorf("service.UpdateGroupAdmin: %w", domain.ErrInvalidStatusTransition)
	}
	// IG-23: aturan plafonError() dari desain "PA Group Admin Form" --
	// plafon storage tidak boleh diturunkan di bawah pemakaian grup saat
	// ini. Baca ulang UsedStorageMB (query yang sama dipakai GetGroupAdminDetail)
	// alih-alih menduplikasi agregat SQL-nya di repository.
	if p.StorageQuotaGB != nil {
		detail, err := s.repo.GetGroupAdminDetail(ctx, targetUserID)
		if err != nil {
			return "", fmt.Errorf("service.UpdateGroupAdmin: cek pemakaian: %w", err)
		}
		usedGB := int(math.Ceil(float64(detail.UsedStorageMB) / 1024))
		if *p.StorageQuotaGB < usedGB {
			return "", fmt.Errorf("service.UpdateGroupAdmin: %w", &domain.StorageQuotaBelowUsageError{MinimumGB: usedGB})
		}
	}
	oldTier, err := s.repo.UpdateGroupAdmin(ctx, targetUserID, p, actorUserID)
	if err != nil {
		return "", fmt.Errorf("service.UpdateGroupAdmin: %w", err)
	}
	return oldTier, nil
}

// ListServiceTiers -- S4P-07/11, katalog tier untuk dropdown Tier + panel
// "Paket Tier (Otomatis)" (includeArchived=false) atau halaman admin
// "Tier & Kuota Global" (includeArchived=true).
func (s *AccountService) ListServiceTiers(ctx context.Context, includeArchived bool) ([]repository.ServiceTier, error) {
	tiers, err := s.repo.ListServiceTiers(ctx, includeArchived)
	if err != nil {
		return nil, fmt.Errorf("service.ListServiceTiers: %w", err)
	}
	return tiers, nil
}

// validateServiceTierParams -- aturan sama persis dengan desain "PA Tier
// Editor" (S4P-11): nama wajib, semua batas numerik > 0, retensi minimum
// >= 30 hari (batas kepatuhan UU PDP), retensi maksimum <= 3650 hari, dan
// maks >= min.
func validateServiceTierParams(p *repository.ServiceTierParams) error {
	if p.Name == "" {
		return domain.ErrInvalidInput
	}
	if p.MaxStorageGB <= 0 || p.MaxOrg <= 0 || p.MaxMembers <= 0 || p.WebhookRate <= 0 {
		return domain.ErrInvalidInput
	}
	if p.MinRetentionDays < 30 || p.MaxRetentionDays > 3650 || p.MaxRetentionDays < p.MinRetentionDays {
		return domain.ErrInvalidInput
	}
	return nil
}

// CreateServiceTier menambah tier custom baru ke katalog (S4P-11).
func (s *AccountService) CreateServiceTier(ctx context.Context, p *repository.ServiceTierParams, actorUserID string) (string, error) {
	if err := validateServiceTierParams(p); err != nil {
		return "", fmt.Errorf("service.CreateServiceTier: %w", err)
	}
	id, err := s.repo.CreateServiceTier(ctx, p, actorUserID)
	if err != nil {
		return "", fmt.Errorf("service.CreateServiceTier: %w", err)
	}
	return id, nil
}

// UpdateServiceTier mengubah definisi tier, termasuk rename (S4P-11).
func (s *AccountService) UpdateServiceTier(ctx context.Context, id string, p *repository.ServiceTierParams, actorUserID string) error {
	if err := validateServiceTierParams(p); err != nil {
		return fmt.Errorf("service.UpdateServiceTier: %w", err)
	}
	if err := s.repo.UpdateServiceTier(ctx, id, p, actorUserID); err != nil {
		return fmt.Errorf("service.UpdateServiceTier: %w", err)
	}
	return nil
}

// DeactivateServiceTier/ReactivateServiceTier/ArchiveServiceTier/
// UnarchiveServiceTier/DeleteServiceTier -- lifecycle tier (S4P-11), tipis
// di atas repository (tidak ada validasi tambahan di level service).
func (s *AccountService) DeactivateServiceTier(ctx context.Context, id, actorUserID string) error {
	if err := s.repo.DeactivateServiceTier(ctx, id, actorUserID); err != nil {
		return fmt.Errorf("service.DeactivateServiceTier: %w", err)
	}
	return nil
}

func (s *AccountService) ReactivateServiceTier(ctx context.Context, id, actorUserID string) error {
	if err := s.repo.ReactivateServiceTier(ctx, id, actorUserID); err != nil {
		return fmt.Errorf("service.ReactivateServiceTier: %w", err)
	}
	return nil
}

func (s *AccountService) ArchiveServiceTier(ctx context.Context, id, actorUserID string) error {
	if err := s.repo.ArchiveServiceTier(ctx, id, actorUserID); err != nil {
		return fmt.Errorf("service.ArchiveServiceTier: %w", err)
	}
	return nil
}

func (s *AccountService) UnarchiveServiceTier(ctx context.Context, id, actorUserID string) error {
	if err := s.repo.UnarchiveServiceTier(ctx, id, actorUserID); err != nil {
		return fmt.Errorf("service.UnarchiveServiceTier: %w", err)
	}
	return nil
}

func (s *AccountService) DeleteServiceTier(ctx context.Context, id, actorUserID string) error {
	if err := s.repo.DeleteServiceTier(ctx, id, actorUserID); err != nil {
		return fmt.Errorf("service.DeleteServiceTier: %w", err)
	}
	return nil
}

// DeleteGroupAdmin menghapus akun Group Admin (S4P-05, IG-21) -- HANYA
// kalau dia sudah tidak mengelola grup manapun (lihat komentar
// repository.DeleteGroupAdmin).
func (s *AccountService) DeleteGroupAdmin(ctx context.Context, targetUserID, actorUserID string) error {
	if err := s.repo.DeleteGroupAdmin(ctx, targetUserID, actorUserID); err != nil {
		return fmt.Errorf("service.DeleteGroupAdmin: %w", err)
	}
	return nil
}

// GetPASessionIdleTimeoutSeconds -- S4P-18, dibalik jadi PER-AKUN 2026-08-29
// (dikonfirmasi user): dipakai FE PlatformSecuritySettings menampilkan nilai
// efektif milik pemanggil sendiri (override atau fallback default sistem).
func (s *AccountService) GetPASessionIdleTimeoutSeconds(ctx context.Context, userID string) (int, error) {
	seconds, err := s.repo.GetPASessionIdleTimeoutSeconds(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("service.GetPASessionIdleTimeoutSeconds: %w", err)
	}
	return seconds, nil
}

// SetPASessionIdleTimeout mengubah session timeout PER-AKUN (dibalik
// 2026-08-29 -- dikonfirmasi user): hanya berlaku untuk userID itu sendiri.
// domain.ErrSessionTimeoutTooShort kalau di bawah 10 menit (US-070 AC).
func (s *AccountService) SetPASessionIdleTimeout(ctx context.Context, seconds int, userID string) error {
	if time.Duration(seconds)*time.Second < minPASessionIdleTimeout {
		return fmt.Errorf("service.SetPASessionIdleTimeout: %w", domain.ErrSessionTimeoutTooShort)
	}
	if err := s.repo.SetPASessionIdleTimeoutSeconds(ctx, seconds, userID); err != nil {
		return fmt.Errorf("service.SetPASessionIdleTimeout: %w", err)
	}
	return nil
}

// GetIPAllowlistEnabled/SetIPAllowlistEnabled -- flag global (dikonfirmasi
// user 2026-08-29), terpisah dari isi daftar CIDR.
func (s *AccountService) GetIPAllowlistEnabled(ctx context.Context) (bool, error) {
	enabled, err := s.repo.GetIPAllowlistEnabled(ctx)
	if err != nil {
		return false, fmt.Errorf("service.GetIPAllowlistEnabled: %w", err)
	}
	return enabled, nil
}

func (s *AccountService) SetIPAllowlistEnabled(ctx context.Context, enabled bool, actorUserID string) error {
	if err := s.repo.SetIPAllowlistEnabled(ctx, enabled, actorUserID); err != nil {
		return fmt.Errorf("service.SetIPAllowlistEnabled: %w", err)
	}
	return nil
}

// ListIPAllowlist -- S4P-18, dibalik jadi GLOBAL 2026-08-29 (dikonfirmasi
// user): seluruh entry, berlaku untuk semua akun Platform Admin.
func (s *AccountService) ListIPAllowlist(ctx context.Context) ([]repository.IPAllowlistEntry, error) {
	entries, err := s.repo.ListIPAllowlist(ctx)
	if err != nil {
		return nil, fmt.Errorf("service.ListIPAllowlist: %w", err)
	}
	return entries, nil
}

// AddIPAllowlistEntry -- S4P-18, dibalik jadi GLOBAL 2026-08-29 (dikonfirmasi
// user). Validasi format CIDR di sini (stdlib net.ParseCIDR) SEBELUM
// menyentuh DB -- repository juga menjaga lewat cast Postgres ($2::cidr)
// sebagai pengaman kedua, tapi pesan error yang jelas (domain.ErrInvalidCIDR)
// lebih murah divalidasi di Go dulu.
func (s *AccountService) AddIPAllowlistEntry(ctx context.Context, cidr, actorUserID string) (string, error) {
	if _, _, err := net.ParseCIDR(cidr); err != nil {
		return "", fmt.Errorf("service.AddIPAllowlistEntry: %w", domain.ErrInvalidCIDR)
	}
	id, err := s.repo.AddIPAllowlistEntry(ctx, cidr, actorUserID)
	if err != nil {
		return "", fmt.Errorf("service.AddIPAllowlistEntry: %w", err)
	}
	return id, nil
}

// DeleteIPAllowlistEntry -- S4P-18, dibalik jadi GLOBAL 2026-08-29
// (dikonfirmasi user): PA mana pun boleh menghapus entry mana pun.
func (s *AccountService) DeleteIPAllowlistEntry(ctx context.Context, entryID, actorUserID string) error {
	if err := s.repo.DeleteIPAllowlistEntry(ctx, entryID, actorUserID); err != nil {
		return fmt.Errorf("service.DeleteIPAllowlistEntry: %w", err)
	}
	return nil
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
