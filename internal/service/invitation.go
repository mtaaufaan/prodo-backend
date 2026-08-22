package service

import (
	"context"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/validator"
)

// invitationTTL -- masa berlaku link undangan workspace (DATABASE_SCHEMA.md
// §5.30), sama durasi dengan groupAdminInvitationTTL tapi konstanta
// terpisah karena beda tabel/fitur (user_invitations vs
// platform_invitations).
const invitationTTL = 72 * time.Hour

// invitationRepository -- interface didefinisikan di consumer (§3.9). exec
// (db.Executor) adalah transaksi request-scoped begitu handler-nya (S2-19)
// menyuntikkan DBContextMiddleware, sama pola dengan workspaceMemberRepository.
type invitationRepository interface {
	CreateInvitation(ctx context.Context, exec db.Executor, email, workspaceID, role, invitedByUserID, tokenHash string, expiresAt time.Time) (string, error)
}

// invitationEmailSender -- interface didefinisikan di consumer,
// diimplementasikan *EmailService.
type invitationEmailSender interface {
	SendWorkspaceInvitationEmail(ctx context.Context, to, workspaceName, inviterName, role, acceptLink string, expiresAt time.Time) error
}

// InvitationService menangani pembuatan undangan workspace (S2-17/18,
// US-006).
type InvitationService struct {
	repo       invitationRepository
	emailer    invitationEmailSender
	appBaseURL string
}

func NewInvitationService(repo invitationRepository, emailer invitationEmailSender, appBaseURL string) *InvitationService {
	return &InvitationService{repo: repo, emailer: emailer, appBaseURL: appBaseURL}
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

// BulkInvitationResult -- hasil CreateBulkInvitations: undangan yang
// berhasil dibuat + pesan error per email yang gagal.
type BulkInvitationResult struct {
	Created []Invitation
	Errors  map[string]string // email -> pesan error
}

// CreateBulkInvitations membuat undangan untuk banyak email sekaligus.
// Duplikat dalam satu batch di-dedupe (satu undangan per email unik,
// BUKAN error) -- "5 email valid + 1 duplikat -> 5 undangan dibuat"
// (verifikasi S2-18). Email format invalid dicatat sebagai error per
// baris, tidak menghentikan email lain dalam batch yang sama.
func (s *InvitationService) CreateBulkInvitations(
	ctx context.Context,
	exec db.Executor,
	emails []string,
	workspaceID, role, invitedByUserID, workspaceName, inviterName string,
) (*BulkInvitationResult, error) {
	result := &BulkInvitationResult{Errors: map[string]string{}}
	seen := make(map[string]bool, len(emails))

	for _, email := range emails {
		if seen[email] {
			continue
		}
		seen[email] = true

		if !validator.IsValidEmail(email) {
			result.Errors[email] = "format email tidak valid"
			continue
		}

		inv, err := s.CreateInvitation(ctx, exec, email, workspaceID, role, invitedByUserID, workspaceName, inviterName)
		if err != nil {
			result.Errors[email] = err.Error()
			continue
		}
		result.Created = append(result.Created, *inv)
	}

	return result, nil
}
