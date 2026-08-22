// Package repository -- InvitationRepository (S2-16/17/18/19/20/21/22/24,
// US-006). Tabel tenant-scoped (workspace_id), lihat
// docs/DATABASE_SCHEMA.md §5.30.
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

// classifyUniqueViolation menerjemahkan Postgres unique_violation (23505)
// jadi target error yang lebih ramah -- dipakai untuk 2 constraint beda
// tabel (users.email di AcceptInvitation, uq_invitation_pending di
// CreateInvitation), pola sama dengan pengecekan inline di
// account_repository.go tapi diparameterisasi supaya bisa dipakai untuk
// keduanya.
func classifyUniqueViolation(err, target error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return target
	}
	return err
}

type InvitationRepository struct{}

func NewInvitationRepository() *InvitationRepository {
	return &InvitationRepository{}
}

// CreateInvitation menyimpan satu undangan (token plaintext TIDAK pernah
// disimpan -- hanya hash-nya, lihat DATABASE_SCHEMA.md §5.30) dan mencatat
// audit trail (S2-24, action 'invitation.created').
func (r *InvitationRepository) CreateInvitation(
	ctx context.Context,
	exec db.Executor,
	email, workspaceID, role, invitedByUserID, tokenHash string,
	expiresAt time.Time,
) (string, error) {
	var id string
	err := exec.QueryRow(ctx, `
		INSERT INTO user_invitations (email, workspace_id, role, invited_by, token_hash, expires_at)
		VALUES ($1, $2, $3::workspace_role, $4, $5, $6)
		RETURNING id
	`, email, workspaceID, role, invitedByUserID, tokenHash, expiresAt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("repository.CreateInvitation: %w", classifyUniqueViolation(err, domain.ErrInvitationAlreadyPending))
	}

	if err := insertInvitationAudit(ctx, exec, invitedByUserID, "invitation.created", id, workspaceID); err != nil {
		return "", fmt.Errorf("repository.CreateInvitation: %w", err)
	}
	return id, nil
}

// InvitationTarget -- undangan PENDING yang cocok dengan hash token
// tertentu (belum accepted, belum cancelled, belum expired).
type InvitationTarget struct {
	ID          string
	Email       string
	WorkspaceID string
	Role        string
}

// FindPendingByTokenHash mencari undangan pending berdasarkan hash token.
// Tidak ditemukan (token salah, sudah accepted, sudah cancelled, ATAU
// sudah expired -- keempatnya SENGAJA disamakan jadi satu error, pola
// sama dengan AccountRepository.FindActivationTarget/domain.
// ErrInvitationNotFound: tidak membocorkan alasan spesifik ke client) ->
// domain.ErrInvitationNotFound.
func (r *InvitationRepository) FindPendingByTokenHash(ctx context.Context, exec db.Executor, tokenHash string) (*InvitationTarget, error) {
	t := &InvitationTarget{}
	err := exec.QueryRow(ctx, `
		SELECT id, email, workspace_id, role
		FROM user_invitations
		WHERE token_hash = $1
		  AND accepted_at IS NULL
		  AND cancelled_at IS NULL
		  AND expires_at > NOW()
	`, tokenHash).Scan(&t.ID, &t.Email, &t.WorkspaceID, &t.Role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.FindPendingByTokenHash: %w", domain.ErrInvitationNotFound)
		}
		return nil, fmt.Errorf("repository.FindPendingByTokenHash: %w", err)
	}
	return t, nil
}

// AcceptInvitation (S2-20) menyimpan user baru + auth provider + membership
// workspace, menandai undangan accepted, dan mencatat audit trail --
// SEMUA lewat exec yang sama (transaksi tunggal dari pemanggil, lihat
// service.InvitationService.AcceptInvitation soal konteks RLS khusus rute
// publik ini). Kalau email sudah terdaftar (race jarang antara invite dan
// accept), INSERT users gagal unique_violation -> domain.ErrEmailAlreadyExists.
func (r *InvitationRepository) AcceptInvitation(
	ctx context.Context,
	exec db.Executor,
	invitationID, email, displayName, keycloakUserID, workspaceID, role string,
) (userID string, err error) {
	err = exec.QueryRow(ctx, `
		INSERT INTO users (email, display_name, platform_role, is_active)
		VALUES ($1, $2, 'member', TRUE)
		RETURNING id
	`, email, displayName).Scan(&userID)
	if err != nil {
		return "", fmt.Errorf("repository.AcceptInvitation: insert users: %w", classifyUniqueViolation(err, domain.ErrEmailAlreadyExists))
	}

	if _, err = exec.Exec(ctx, `
		INSERT INTO user_auth_providers (user_id, provider, provider_sub)
		VALUES ($1, 'local', $2)
	`, userID, keycloakUserID); err != nil {
		return "", fmt.Errorf("repository.AcceptInvitation: insert user_auth_providers: %w", err)
	}

	if _, err = exec.Exec(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role, invited_by)
		VALUES ($1, $2, $3::workspace_role, NULL)
	`, workspaceID, userID, role); err != nil {
		return "", fmt.Errorf("repository.AcceptInvitation: insert workspace_members: %w", err)
	}

	if _, err = exec.Exec(ctx, `
		UPDATE user_invitations SET accepted_at = NOW() WHERE id = $1
	`, invitationID); err != nil {
		return "", fmt.Errorf("repository.AcceptInvitation: update accepted_at: %w", err)
	}

	if err = insertInvitationAudit(ctx, exec, userID, "invitation.accepted", invitationID, workspaceID); err != nil {
		return "", fmt.Errorf("repository.AcceptInvitation: %w", err)
	}
	return userID, nil
}

// Cancel (S2-21) menandai undangan dibatalkan -- baris TIDAK dihapus
// (DATABASE_SCHEMA.md §5.30). Hanya berlaku kalau undangan masih pending
// DAN milik workspace yang diminta (guard workspace_id supaya AW workspace
// lain tidak bisa membatalkan pakai tebak ID). 0 baris ter-update (tidak
// ditemukan/sudah accepted/sudah cancelled) -> domain.ErrInvitationNotFound,
// konsisten dengan Resend/FindPendingByTokenHash.
func (r *InvitationRepository) Cancel(ctx context.Context, exec db.Executor, workspaceID, invitationID, actorID string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE user_invitations SET cancelled_at = NOW()
		WHERE id = $1 AND workspace_id = $2 AND accepted_at IS NULL AND cancelled_at IS NULL
	`, invitationID, workspaceID)
	if err != nil {
		return fmt.Errorf("repository.Cancel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Cancel: %w", domain.ErrInvitationNotFound)
	}
	if err := insertInvitationAudit(ctx, exec, actorID, "invitation.cancelled", invitationID, workspaceID); err != nil {
		return fmt.Errorf("repository.Cancel: %w", err)
	}
	return nil
}

// ResendTarget -- data undangan yang dibutuhkan untuk mengirim ulang email
// (S2-22).
type ResendTarget struct {
	Email string
	Role  string
}

// Resend (S2-22) menerbitkan token baru untuk undangan yang masih pending
// -- menimpa token_hash lama (token mentah lama otomatis invalid karena
// hash-nya tidak lagi cocok baris manapun) dan memperpanjang expires_at.
// Guard sama dengan Cancel: workspace_id harus cocok, harus masih pending.
func (r *InvitationRepository) Resend(ctx context.Context, exec db.Executor, workspaceID, invitationID, newTokenHash string, newExpiresAt time.Time) (*ResendTarget, error) {
	t := &ResendTarget{}
	err := exec.QueryRow(ctx, `
		UPDATE user_invitations
		SET token_hash = $1, expires_at = $2
		WHERE id = $3 AND workspace_id = $4 AND accepted_at IS NULL AND cancelled_at IS NULL
		RETURNING email, role
	`, newTokenHash, newExpiresAt, invitationID, workspaceID).Scan(&t.Email, &t.Role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.Resend: %w", domain.ErrInvitationNotFound)
		}
		return nil, fmt.Errorf("repository.Resend: %w", err)
	}
	return t, nil
}

// PendingInvitation -- satu baris hasil ListPending.
type PendingInvitation struct {
	ID        string
	Email     string
	Role      string
	ExpiresAt time.Time
}

// ListPending mengembalikan seluruh undangan PENDING (belum accepted,
// belum cancelled -- termasuk yang sudah lewat expires_at, biar AW tetap
// bisa lihat lalu resend) di workspace ini. Prasyarat minimal S2-28
// (daftar undangan pending di FE) yang belum pernah dijadwalkan sebagai
// task backend terpisah -- lihat implementation_gaps.md IG-09.
func (r *InvitationRepository) ListPending(ctx context.Context, exec db.Executor, workspaceID string) ([]PendingInvitation, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, email, role, expires_at
		FROM user_invitations
		WHERE workspace_id = $1 AND accepted_at IS NULL AND cancelled_at IS NULL
		ORDER BY created_at DESC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListPending: %w", err)
	}
	defer rows.Close()

	var invitations []PendingInvitation
	for rows.Next() {
		var inv PendingInvitation
		if err := rows.Scan(&inv.ID, &inv.Email, &inv.Role, &inv.ExpiresAt); err != nil {
			return nil, fmt.Errorf("repository.ListPending: scan: %w", err)
		}
		invitations = append(invitations, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListPending: rows: %w", err)
	}
	return invitations, nil
}

// GetWorkspaceName -- dipakai handler untuk isi email undangan (nama
// workspace, bukan UUID). Prasyarat minimal: `workspaces` sendiri baru
// prasyarat FK dari IG-09, belum ada handler/query lain yang membacanya --
// query di sini SATU-SATUNYA yang menyentuh kolom `name`-nya sejauh ini.
func (r *InvitationRepository) GetWorkspaceName(ctx context.Context, exec db.Executor, workspaceID string) (string, error) {
	var name string
	if err := exec.QueryRow(ctx, `SELECT name FROM workspaces WHERE id = $1`, workspaceID).Scan(&name); err != nil {
		return "", fmt.Errorf("repository.GetWorkspaceName: %w", err)
	}
	return name, nil
}

// insertInvitationAudit -- helper lokal (bukan logAudit di
// account_repository.go, karena entity_type di sini 'user_invitation' dan
// perlu kolom workspace_id, beda dari logAudit yang hardcode entity_type
// 'user' tanpa workspace_id).
func insertInvitationAudit(ctx context.Context, exec db.Executor, actorID, action, invitationID, workspaceID string) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, entity_type, entity_id, workspace_id)
		VALUES ($1, $2, 'user_invitation', $3, $4)
	`, actorID, action, invitationID, workspaceID)
	return err
}
