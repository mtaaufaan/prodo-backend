package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

// ErasureRequest -- satu baris antrian Right to Erasure (S4P-28, US-060).
type ErasureRequest struct {
	ID              string
	Subject         string // users.display_name subjek data
	OrgName         string
	RequestedByName string
	Status          string // PENDING | DONE | REJECTED
	RequestedAt     time.Time
	ProcessedAt     *time.Time
	UserID          string
}

type ErasureRepository struct {
	db *pgxpool.Pool
}

func NewErasureRepository(db *pgxpool.Pool) *ErasureRepository {
	return &ErasureRepository{db: db}
}

// Create -- S4P-29. Tidak butuh RLS context (erasure_requests sendiri
// tidak di-RLS, lihat komentar migrasi) -- FK ke organizations/users cukup
// divalidasi lewat constraint FK, bukan policy.
func (r *ErasureRepository) Create(ctx context.Context, userID, orgID, requestedBy, reason string) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `
		INSERT INTO erasure_requests (user_id, org_id, requested_by, reason)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		RETURNING id
	`, userID, orgID, requestedBy, reason).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("repository.ErasureRepository.Create: %w", err)
	}
	return id, nil
}

// List -- S4P-30, antrian PA. organizations kena RLS (S3-42) -- WAJIB
// withPlatformAdminRLS, pola sama IG-24/S4P-24.
func (r *ErasureRepository) List(ctx context.Context) ([]ErasureRequest, error) {
	var result []ErasureRequest
	err := withPlatformAdminRLS(ctx, r.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT er.id, subj.display_name, org.name, reqby.display_name,
			       er.status::text, er.requested_at, er.processed_at, er.user_id
			FROM erasure_requests er
			JOIN users subj ON subj.id = er.user_id
			JOIN organizations org ON org.id = er.org_id
			JOIN users reqby ON reqby.id = er.requested_by
			ORDER BY er.requested_at DESC
		`)
		if err != nil {
			return fmt.Errorf("query: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var e ErasureRequest
			if err := rows.Scan(&e.ID, &e.Subject, &e.OrgName, &e.RequestedByName,
				&e.Status, &e.RequestedAt, &e.ProcessedAt, &e.UserID); err != nil {
				return fmt.Errorf("scan: %w", err)
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("repository.ErasureRepository.List: %w", err)
	}
	return result, nil
}

// HasSharedWorkspaceAdminRole -- otorisasi POST (S4P-29): apakah requester
// adalah admin_workspace/project_manager di sebuah workspace yang juga
// diikuti targetUserID. Dijalankan dalam transaksi ber-RLS
// (app.current_user_id = requester) supaya query terhadap workspace_members
// (FORCE ROW LEVEL SECURITY, RLS_DESIGN.md §7.3) tidak diam-diam melihat 0
// baris -- pola sama seperti IG-14/IG-24, kali ini dicegah dari awal.
func (r *ErasureRepository) HasSharedWorkspaceAdminRole(ctx context.Context, requesterID, requesterPlatformRole, targetUserID string) (bool, error) {
	var exists bool
	err := withRLSContext(ctx, r.db, requesterID, requesterPlatformRole, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM workspace_members mine
				JOIN workspace_members theirs ON theirs.workspace_id = mine.workspace_id
				WHERE mine.user_id = $1
				  AND mine.role IN ('admin_workspace', 'project_manager')
				  AND theirs.user_id = $2
			)
		`, requesterID, targetUserID).Scan(&exists)
	})
	if err != nil {
		return false, fmt.Errorf("repository.ErasureRepository.HasSharedWorkspaceAdminRole: %w", err)
	}
	return exists, nil
}

// Execute -- S4P-31/32. Satu transaksi: pseudonymization users (persis
// docs/DATABASE_SCHEMA.md §5.1, BUKAN wording token S4P-32 yang lebih lama
// -- lihat implementation_gaps.md) + revoke user_sessions + hapus
// user_mfa_configs + tandai request DONE + audit trail. Lock row via
// SELECT ... FOR UPDATE supaya dua eksekusi bersamaan tidak lolos ganda.
func (r *ErasureRepository) Execute(ctx context.Context, requestID, processedBy string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.ErasureRepository.Execute: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // sudah commit di jalur sukses, rollback di jalur gagal cukup best-effort

	var userID, status string
	if err := tx.QueryRow(ctx, `SELECT user_id, status::text FROM erasure_requests WHERE id = $1 FOR UPDATE`, requestID).
		Scan(&userID, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrErasureRequestNotFound
		}
		return fmt.Errorf("repository.ErasureRepository.Execute: lookup: %w", err)
	}
	if status != "PENDING" {
		return domain.ErrErasureRequestAlreadyProcessed
	}

	if _, err := tx.Exec(ctx, `
		UPDATE users SET
			display_name = 'User [REDACTED]',
			email = 'redacted-' || encode(digest(id::text || clock_timestamp()::text, 'sha256'), 'hex') || '@prodo.pseudonym',
			avatar_url = NULL,
			deleted_at = NOW()
		WHERE id = $1
	`, userID); err != nil {
		return fmt.Errorf("repository.ErasureRepository.Execute: pseudonymize users: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("repository.ErasureRepository.Execute: revoke sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_mfa_configs WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("repository.ErasureRepository.Execute: delete mfa: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE erasure_requests SET status = 'DONE', processed_at = NOW(), processed_by = $2 WHERE id = $1
	`, requestID, processedBy); err != nil {
		return fmt.Errorf("repository.ErasureRepository.Execute: mark done: %w", err)
	}
	if err := logAudit(ctx, tx, processedBy, "platform_admin", "erasure.executed", userID); err != nil {
		return fmt.Errorf("repository.ErasureRepository.Execute: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.ErasureRepository.Execute: commit: %w", err)
	}
	return nil
}

// Reject -- S4P-31 (tambahan, menutup gap desain "PA Right To Erasure" yang
// punya tombol BATALKAN tapi tidak tercantum di task list). Reversibel
// secara data (tidak ada penghapusan), jadi TANPA konfirmasi dua langkah
// seperti Execute -- sesuai desain "PA Erasure Confirm" mode=reject.
func (r *ErasureRepository) Reject(ctx context.Context, requestID, processedBy string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.ErasureRepository.Reject: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // sudah commit di jalur sukses, rollback di jalur gagal cukup best-effort

	var userID, status string
	if err := tx.QueryRow(ctx, `SELECT user_id, status::text FROM erasure_requests WHERE id = $1 FOR UPDATE`, requestID).
		Scan(&userID, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrErasureRequestNotFound
		}
		return fmt.Errorf("repository.ErasureRepository.Reject: lookup: %w", err)
	}
	if status != "PENDING" {
		return domain.ErrErasureRequestAlreadyProcessed
	}

	if _, err := tx.Exec(ctx, `
		UPDATE erasure_requests SET status = 'REJECTED', processed_at = NOW(), processed_by = $2 WHERE id = $1
	`, requestID, processedBy); err != nil {
		return fmt.Errorf("repository.ErasureRepository.Reject: mark rejected: %w", err)
	}
	if err := logAudit(ctx, tx, processedBy, "platform_admin", "erasure.rejected", userID); err != nil {
		return fmt.Errorf("repository.ErasureRepository.Reject: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.ErasureRepository.Reject: commit: %w", err)
	}
	return nil
}
