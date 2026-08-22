package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/validator"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// invitationTTL -- masa berlaku link undangan workspace (DATABASE_SCHEMA.md
// §5.30), sama durasi dengan groupAdminInvitationTTL tapi konstanta
// terpisah karena beda tabel/fitur (user_invitations vs
// platform_invitations).
const invitationTTL = 72 * time.Hour

// invitationRepository -- interface didefinisikan di consumer (§3.9). exec
// (db.Executor) adalah transaksi request-scoped -- untuk CreateInvitation/
// CancelInvitation/ResendInvitation dari DBContextMiddleware (rute
// terautentikasi biasa), untuk AcceptInvitation dari konteks khusus rute
// publik (lihat komentar AcceptInvitation).
type invitationRepository interface {
	CreateInvitation(ctx context.Context, exec db.Executor, email, workspaceID, role, invitedByUserID, tokenHash string, expiresAt time.Time) (string, error)
	FindPendingByTokenHash(ctx context.Context, exec db.Executor, tokenHash string) (*repository.InvitationTarget, error)
	AcceptInvitation(ctx context.Context, exec db.Executor, invitationID, email, displayName, keycloakUserID, workspaceID, role string) (string, error)
	Cancel(ctx context.Context, exec db.Executor, workspaceID, invitationID, actorID string) error
	Resend(ctx context.Context, exec db.Executor, workspaceID, invitationID, newTokenHash string, newExpiresAt time.Time) (*repository.ResendTarget, error)
	GetWorkspaceName(ctx context.Context, exec db.Executor, workspaceID string) (string, error)
	ListPending(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.PendingInvitation, error)
}

// invitationEmailSender -- interface didefinisikan di consumer,
// diimplementasikan *EmailService.
type invitationEmailSender interface {
	SendWorkspaceInvitationEmail(ctx context.Context, to, workspaceName, inviterName, role, acceptLink string, expiresAt time.Time) error
}

// existingUserFinder -- interface didefinisikan di consumer,
// diimplementasikan *AccountRepository (S2-23).
type existingUserFinder interface {
	FindUserIDByEmail(ctx context.Context, email string) (string, error)
}

// workspaceAssigner -- interface didefinisikan di consumer,
// diimplementasikan *RBACService (S2-23: shortcut tambah langsung ke
// workspace, bukan bikin undangan, kalau email sudah terdaftar).
type workspaceAssigner interface {
	AssignRole(ctx context.Context, exec db.Executor, workspaceID, userID, role string, invitedBy *string, actorID, actorRole string) (*RoleChangeResult, error)
}

// InvitationService menangani lifecycle undangan workspace (S2-17/18/20/
// 21/22/23, US-006).
type InvitationService struct {
	repo       invitationRepository
	emailer    invitationEmailSender
	keycloak   keycloak.AdminClient
	users      existingUserFinder
	assigner   workspaceAssigner
	logger     *zap.Logger
	appBaseURL string
}

func NewInvitationService(
	repo invitationRepository,
	emailer invitationEmailSender,
	kc keycloak.AdminClient,
	users existingUserFinder,
	assigner workspaceAssigner,
	logger *zap.Logger,
	appBaseURL string,
) *InvitationService {
	return &InvitationService{repo: repo, emailer: emailer, keycloak: kc, users: users, assigner: assigner, logger: logger, appBaseURL: appBaseURL}
}

// Invitation -- hasil satu undangan yang berhasil dibuat.
type Invitation struct {
	ID          string
	Email       string
	WorkspaceID string
	Role        string
	ExpiresAt   time.Time
}

// CreateInvitation membuat satu undangan workspace -- token one-time (TTL
// 72 jam) + kirim email berisi link penerimaan. workspaceName/inviterName
// dipakai untuk isi email, disuplai caller (handler yang sudah resolve
// dari param request) -- service ini tidak query ulang nama workspace/user.
func (s *InvitationService) CreateInvitation(
	ctx context.Context,
	exec db.Executor,
	email, workspaceID, role, invitedByUserID, workspaceName, inviterName string,
) (*Invitation, error) {
	rawToken, tokenHash, err := generateActivationToken()
	if err != nil {
		return nil, fmt.Errorf("service.CreateInvitation: %w", err)
	}
	expiresAt := time.Now().Add(invitationTTL)

	id, err := s.repo.CreateInvitation(ctx, exec, email, workspaceID, role, invitedByUserID, tokenHash, expiresAt)
	if err != nil {
		return nil, fmt.Errorf("service.CreateInvitation: %w", err)
	}

	acceptLink := fmt.Sprintf("%s/invitations/accept?token=%s", s.appBaseURL, rawToken)
	if err := s.emailer.SendWorkspaceInvitationEmail(ctx, email, workspaceName, inviterName, role, acceptLink, expiresAt); err != nil {
		return nil, fmt.Errorf("service.CreateInvitation: %w", err)
	}

	return &Invitation{ID: id, Email: email, WorkspaceID: workspaceID, Role: role, ExpiresAt: expiresAt}, nil
}

// BulkInvitationResult -- hasil CreateBulkInvitations. Created: undangan
// baru yang benar-benar dibuat (email belum terdaftar). AddedDirectly
// (S2-23): email yang SUDAH terdaftar, langsung ditambahkan ke workspace
// tanpa undangan/email baru. Errors: pesan error per email yang gagal.
type BulkInvitationResult struct {
	Created       []Invitation
	AddedDirectly []string
	Errors        map[string]string // email -> pesan error
}

// CreateBulkInvitations membuat undangan untuk banyak email sekaligus.
// Duplikat dalam satu batch di-dedupe (satu undangan per email unik,
// BUKAN error) -- "5 email valid + 1 duplikat -> 5 undangan dibuat"
// (verifikasi S2-18). Email format invalid dicatat sebagai error per
// baris, tidak menghentikan email lain dalam batch yang sama. Email yang
// sudah terdaftar sebagai user (S2-23) di-skip dari alur undangan --
// ditambahkan langsung ke workspace_members lewat assigner.
func (s *InvitationService) CreateBulkInvitations(
	ctx context.Context,
	exec db.Executor,
	emails []string,
	workspaceID, role, invitedByUserID, actorRole, workspaceName, inviterName string,
) (*BulkInvitationResult, error) {
	result := &BulkInvitationResult{Errors: map[string]string{}}
	seen := make(map[string]bool, len(emails))

	for i, email := range emails {
		if seen[email] {
			continue
		}
		seen[email] = true

		if !validator.IsValidEmail(email) {
			result.Errors[email] = "format email tidak valid"
			continue
		}

		// savepoint per email -- WAJIB: satu email gagal (mis. unique_
		// violation "sudah pending") membuat SELURUH transaksi Postgres
		// berstatus aborted sampai ada ROLLBACK, bukan cuma baris itu yang
		// gagal. Tanpa savepoint, satu email bermasalah bikin COMMIT di
		// akhir request gagal untuk email lain yang sebenarnya valid --
		// ketahuan lewat live testing (re-invite email yang masih pending
		// -> 500 "Gagal menyimpan perubahan", bukan error per-baris).
		savepoint := fmt.Sprintf("sp_bulk_inv_%d", i)

		existingUserID, err := s.users.FindUserIDByEmail(ctx, email)
		switch {
		case err == nil:
			// S2-23: email sudah terdaftar -- tambah langsung, tanpa undangan/email.
			err := withSavepoint(ctx, exec, savepoint, func() error {
				_, err := s.assigner.AssignRole(ctx, exec, workspaceID, existingUserID, role, &invitedByUserID, invitedByUserID, actorRole)
				return err
			})
			if err != nil {
				result.Errors[email] = err.Error()
				continue
			}
			result.AddedDirectly = append(result.AddedDirectly, email)
		case errors.Is(err, pgx.ErrNoRows):
			var inv *Invitation
			err := withSavepoint(ctx, exec, savepoint, func() error {
				var err error
				inv, err = s.CreateInvitation(ctx, exec, email, workspaceID, role, invitedByUserID, workspaceName, inviterName)
				return err
			})
			if err != nil {
				result.Errors[email] = err.Error()
				continue
			}
			result.Created = append(result.Created, *inv)
		default:
			result.Errors[email] = err.Error()
		}
	}

	return result, nil
}

// withSavepoint membungkus fn dalam SAVEPOINT Postgres -- rollback ke
// savepoint (bukan seluruh transaksi) kalau fn gagal, supaya caller bisa
// melanjutkan operasi lain dalam transaksi yang sama. name harus konstanta/
// dari sumber tepercaya (index loop), BUKAN dari input user -- tidak
// diparameterisasi via placeholder karena SAVEPOINT tidak mendukung itu.
func withSavepoint(ctx context.Context, exec db.Executor, name string, fn func() error) error {
	if _, err := exec.Exec(ctx, "SAVEPOINT "+name); err != nil {
		return fmt.Errorf("savepoint: %w", err)
	}
	if err := fn(); err != nil {
		if _, rbErr := exec.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name); rbErr != nil {
			return fmt.Errorf("%w (rollback to savepoint juga gagal: %v)", err, rbErr)
		}
		return err
	}
	if _, err := exec.Exec(ctx, "RELEASE SAVEPOINT "+name); err != nil {
		return fmt.Errorf("release savepoint: %w", err)
	}
	return nil
}

// GetWorkspaceName -- passthrough tipis ke repo, dipakai handler untuk isi
// email undangan (S2-19/22). Penempatan sementara di sini karena belum ada
// WorkspaceService (menyusul S3-08 dst) -- pindahkan ke sana begitu ada.
func (s *InvitationService) GetWorkspaceName(ctx context.Context, exec db.Executor, workspaceID string) (string, error) {
	name, err := s.repo.GetWorkspaceName(ctx, exec, workspaceID)
	if err != nil {
		return "", fmt.Errorf("service.GetWorkspaceName: %w", err)
	}
	return name, nil
}

// ListPendingInvitations -- passthrough tipis ke repo (S2-28 prasyarat,
// lihat implementation_gaps.md IG-09).
func (s *InvitationService) ListPendingInvitations(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.PendingInvitation, error) {
	invitations, err := s.repo.ListPending(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.ListPendingInvitations: %w", err)
	}
	return invitations, nil
}

// AcceptedInvitation -- hasil AcceptInvitation.
type AcceptedInvitation struct {
	UserID      string
	Email       string
	WorkspaceID string
	Role        string
}

// AcceptInvitation (S2-20, non-SSO) memvalidasi token, membuat akun
// Keycloak+PRODO baru, menambahkan ke workspace, dan menandai undangan
// accepted. Reuse 3 method AdminClient yang sudah ada dari alur aktivasi
// Group Admin (CreateDisabledUser -> SetPassword -> EnableUser) --
// EnableUser mengosongkan SEMUA requiredAction, termasuk CONFIGURE_TOTP
// bawaan CreateDisabledUser, jadi member biasa TIDAK diwajibkan setup MFA
// (beda dari Group Admin/US-073 yang memang mewajibkannya).
//
// exec di sini BUKAN dari DBContextMiddleware biasa -- endpoint ini publik
// (belum ada sesi/JWT sama sekali, invitee belum py akun), jadi tidak ada
// actor claims untuk diresolve. Handler membangun transaksi lewat
// db.SetRLSContext dengan platform_role="platform_admin" (bypass RLS
// wm_insert yang mensyaratkan actor sudah jadi workspace member -- tidak
// berlaku di sini karena user ini JUSTRU baru akan jadi member pertama
// kalinya). Ini operasi tepercaya karena sudah divalidasi lewat kepemilikan
// token, bukan klaim JWT -- lihat internal/handler/invitation_handler.go.
//
// Alur SSO ("auto-activate") BELUM diimplementasikan -- organizations.
// sso_enabled ada di skema tapi belum ada satupun organisasi yang benar-benar
// mengaktifkannya (SSO config UI/backend belum dibangun, US-074/S12).
func (s *InvitationService) AcceptInvitation(ctx context.Context, exec db.Executor, rawToken, displayName, password string) (*AcceptedInvitation, error) {
	if len(displayName) < 2 {
		return nil, fmt.Errorf("service.AcceptInvitation: %w", domain.ErrInvalidInput)
	}

	target, err := s.repo.FindPendingByTokenHash(ctx, exec, hashActivationToken(rawToken))
	if err != nil {
		return nil, fmt.Errorf("service.AcceptInvitation: %w", err)
	}

	kcUserID, err := s.keycloak.CreateDisabledUser(ctx, target.Email, displayName)
	if err != nil {
		if errors.Is(err, keycloak.ErrUserAlreadyExists) {
			return nil, fmt.Errorf("service.AcceptInvitation: %w", domain.ErrEmailAlreadyExists)
		}
		return nil, fmt.Errorf("service.AcceptInvitation: %w", err)
	}
	if err := s.keycloak.SetPassword(ctx, kcUserID, password); err != nil {
		return nil, fmt.Errorf("service.AcceptInvitation: %w", err)
	}
	if err := s.keycloak.EnableUser(ctx, kcUserID); err != nil {
		return nil, fmt.Errorf("service.AcceptInvitation: %w", err)
	}

	userID, err := s.repo.AcceptInvitation(ctx, exec, target.ID, target.Email, displayName, kcUserID, target.WorkspaceID, target.Role)
	if err != nil {
		s.logger.Error("user Keycloak berhasil dibuat tapi gagal simpan PRODO -- kemungkinan orphan, perlu cleanup manual",
			zap.String("email", target.Email),
			zap.String("keycloak_user_id", kcUserID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("service.AcceptInvitation: %w", err)
	}

	return &AcceptedInvitation{UserID: userID, Email: target.Email, WorkspaceID: target.WorkspaceID, Role: target.Role}, nil
}

// CancelInvitation (S2-21) membatalkan undangan pending -- baris tidak
// dihapus (audit trail tetap ada). domain.ErrInvitationNotFound kalau
// sudah accepted/cancelled/tidak ada di workspace ini.
func (s *InvitationService) CancelInvitation(ctx context.Context, exec db.Executor, workspaceID, invitationID, actorID string) error {
	if err := s.repo.Cancel(ctx, exec, workspaceID, invitationID, actorID); err != nil {
		return fmt.Errorf("service.CancelInvitation: %w", err)
	}
	return nil
}

// ResendInvitation (S2-22) menerbitkan token baru untuk undangan pending
// dan mengirim ulang email -- token lama otomatis invalid (hash-nya
// ditimpa). domain.ErrInvitationNotFound kalau sudah accepted/cancelled.
func (s *InvitationService) ResendInvitation(ctx context.Context, exec db.Executor, workspaceID, invitationID, workspaceName, inviterName string) error {
	rawToken, tokenHash, err := generateActivationToken()
	if err != nil {
		return fmt.Errorf("service.ResendInvitation: %w", err)
	}
	expiresAt := time.Now().Add(invitationTTL)

	target, err := s.repo.Resend(ctx, exec, workspaceID, invitationID, tokenHash, expiresAt)
	if err != nil {
		return fmt.Errorf("service.ResendInvitation: %w", err)
	}

	acceptLink := fmt.Sprintf("%s/invitations/accept?token=%s", s.appBaseURL, rawToken)
	if err := s.emailer.SendWorkspaceInvitationEmail(ctx, target.Email, workspaceName, inviterName, target.Role, acceptLink, expiresAt); err != nil {
		return fmt.Errorf("service.ResendInvitation: %w", err)
	}
	return nil
}
