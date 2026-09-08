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

// CreateExecutiveInvitation menyimpan undangan Eksekutif MURNI (email baru,
// tanpa workspace/role -- desain "GA Members Roles.dc.html", toggle
// "Eksekutif" di modal Undang). Constraint chk_invitation_shape (migrasi
// 20260915090300) menjaga workspace_id/role tetap NULL di baris ini.
func (r *InvitationRepository) CreateExecutiveInvitation(
	ctx context.Context,
	exec db.Executor,
	email, groupID, invitedByUserID, tokenHash string,
	expiresAt time.Time,
) (string, error) {
	var id string
	err := exec.QueryRow(ctx, `
		INSERT INTO user_invitations (email, group_id, is_executive_invite, invited_by, token_hash, expires_at)
		VALUES ($1, $2, TRUE, $3, $4, $5)
		RETURNING id
	`, email, groupID, invitedByUserID, tokenHash, expiresAt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("repository.CreateExecutiveInvitation: %w", classifyUniqueViolation(err, domain.ErrInvitationAlreadyPending))
	}

	if _, err := exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, entity_type, entity_id, metadata)
		VALUES ($1, 'invitation.created', 'user_invitation', $2, jsonb_build_object('group_id', $3::uuid, 'is_executive_invite', true))
	`, invitedByUserID, id, groupID); err != nil {
		return "", fmt.Errorf("repository.CreateExecutiveInvitation: audit: %w", err)
	}
	return id, nil
}

// InvitationTarget -- undangan PENDING yang cocok dengan hash token
// tertentu (belum accepted, belum cancelled, belum expired). WorkspaceID/
// Role kosong dan GroupID terisi kalau IsExecutiveInvite true (lihat
// chk_invitation_shape, migrasi 20260915090300) -- caller cabang di situ,
// bukan pointer nullable, supaya alur normal (mayoritas kasus) tidak perlu
// nil-check tambahan.
type InvitationTarget struct {
	ID                string
	Email             string
	WorkspaceID       string
	Role              string
	GroupID           string
	IsExecutiveInvite bool
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
		SELECT id, email, COALESCE(workspace_id::text, ''), COALESCE(role::text, ''),
		       COALESCE(group_id::text, ''), is_executive_invite
		FROM user_invitations
		WHERE token_hash = $1
		  AND accepted_at IS NULL
		  AND cancelled_at IS NULL
		  AND expires_at > NOW()
	`, tokenHash).Scan(&t.ID, &t.Email, &t.WorkspaceID, &t.Role, &t.GroupID, &t.IsExecutiveInvite)
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

// AcceptExecutiveInvitation -- varian AcceptInvitation untuk undangan
// Eksekutif murni: users + user_auth_providers sama persis, tapi baris
// keanggotaan masuk ke executive_assignments (bukan workspace_members),
// title kosong (diisi belakangan lewat panel Kelola Member, bukan saat
// aktivasi).
func (r *InvitationRepository) AcceptExecutiveInvitation(
	ctx context.Context,
	exec db.Executor,
	invitationID, email, displayName, keycloakUserID, groupID string,
) (userID string, err error) {
	err = exec.QueryRow(ctx, `
		INSERT INTO users (email, display_name, platform_role, is_active)
		VALUES ($1, $2, 'executive', TRUE)
		RETURNING id
	`, email, displayName).Scan(&userID)
	if err != nil {
		return "", fmt.Errorf("repository.AcceptExecutiveInvitation: insert users: %w", classifyUniqueViolation(err, domain.ErrEmailAlreadyExists))
	}

	if _, err = exec.Exec(ctx, `
		INSERT INTO user_auth_providers (user_id, provider, provider_sub)
		VALUES ($1, 'local', $2)
	`, userID, keycloakUserID); err != nil {
		return "", fmt.Errorf("repository.AcceptExecutiveInvitation: insert user_auth_providers: %w", err)
	}

	if _, err = exec.Exec(ctx, `
		INSERT INTO executive_assignments (user_id, group_id, assigned_by)
		VALUES ($1, $2, NULL)
	`, userID, groupID); err != nil {
		return "", fmt.Errorf("repository.AcceptExecutiveInvitation: insert executive_assignments: %w", err)
	}

	if _, err = exec.Exec(ctx, `
		UPDATE user_invitations SET accepted_at = NOW() WHERE id = $1
	`, invitationID); err != nil {
		return "", fmt.Errorf("repository.AcceptExecutiveInvitation: update accepted_at: %w", err)
	}

	if _, err := exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, entity_type, entity_id, metadata)
		VALUES ($1, 'invitation.accepted', 'user_invitation', $2, jsonb_build_object('group_id', $3::uuid, 'is_executive_invite', true))
	`, userID, invitationID, groupID); err != nil {
		return "", fmt.Errorf("repository.AcceptExecutiveInvitation: audit: %w", err)
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

// GroupPendingInvite -- satu baris undangan pending, lintas semua workspace
// dalam grup (direktori Members & Roles) DAN undangan Eksekutif murni grup
// itu sendiri. WorkspaceID/WorkspaceName/OrgName/Role kosong kalau
// IsExecutive true.
type GroupPendingInvite struct {
	ID            string
	Email         string
	Role          string
	WorkspaceID   string
	WorkspaceName string
	OrgName       string
	IsExecutive   bool
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

// ListPendingForGroup mengembalikan seluruh undangan pending lintas semua
// workspace dalam grup INI, digabung dengan undangan Eksekutif murni grup
// ini (union dua bentuk baris yang beda kolom -- dipisah 2 query, bukan SQL
// UNION, supaya scan Go tetap sederhana per bentuk).
func (r *InvitationRepository) ListPendingForGroup(ctx context.Context, exec db.Executor, groupID string) ([]GroupPendingInvite, error) {
	rows, err := exec.Query(ctx, `
		SELECT ui.id, ui.email, ui.role::text, ui.workspace_id, w.name, o.name, ui.created_at, ui.expires_at
		FROM user_invitations ui
		JOIN workspaces w ON w.id = ui.workspace_id
		JOIN organizations o ON o.id = w.org_id
		WHERE o.group_id = $1 AND ui.accepted_at IS NULL AND ui.cancelled_at IS NULL AND ui.is_executive_invite = FALSE
		ORDER BY ui.created_at DESC
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListPendingForGroup: workspace invites: %w", err)
	}
	var result []GroupPendingInvite
	for rows.Next() {
		var p GroupPendingInvite
		if err := rows.Scan(&p.ID, &p.Email, &p.Role, &p.WorkspaceID, &p.WorkspaceName, &p.OrgName, &p.CreatedAt, &p.ExpiresAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("repository.ListPendingForGroup: scan workspace invite: %w", err)
		}
		result = append(result, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("repository.ListPendingForGroup: rows workspace invites: %w", err)
	}
	rows.Close()

	execRows, err := exec.Query(ctx, `
		SELECT id, email, created_at, expires_at
		FROM user_invitations
		WHERE group_id = $1 AND accepted_at IS NULL AND cancelled_at IS NULL AND is_executive_invite = TRUE
		ORDER BY created_at DESC
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListPendingForGroup: executive invites: %w", err)
	}
	defer execRows.Close()
	for execRows.Next() {
		var p GroupPendingInvite
		if err := execRows.Scan(&p.ID, &p.Email, &p.CreatedAt, &p.ExpiresAt); err != nil {
			return nil, fmt.Errorf("repository.ListPendingForGroup: scan executive invite: %w", err)
		}
		p.IsExecutive = true
		result = append(result, p)
	}
	if err := execRows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListPendingForGroup: rows executive invites: %w", err)
	}
	return result, nil
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
