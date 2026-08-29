package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PlatformAuditLogEntry -- satu baris platform_audit_logs untuk respons
// GET /platform/audit-logs (S4P-22), dengan actor_email/actor_display_name
// hasil JOIN ke users supaya FE tidak perlu resolve UUID sendiri.
//
// TargetUserName/TargetTierName (feedback user 2026-08-28, kalimat
// naratif audit trail) -- entity_id merujuk ke BARIS BERBEDA tergantung
// entity_type (user yang di-suspend/diundang, ATAU tier yang diubah),
// bukan actor_id. Resolusi nama di sini (bukan di FE) supaya FE tidak
// perlu request tambahan per baris.
type PlatformAuditLogEntry struct {
	ID               string
	ActorID          *string
	ActorEmail       *string
	ActorDisplayName *string
	ActorRole        *string
	Action           string
	EntityType       string
	EntityID         *string
	TargetUserName   *string
	// TargetUserRole -- S4P-40: action code seperti user.suspended/
	// user.reactivated/user.invited/user.mfa_reset dipakai BERSAMA untuk
	// Group Admin (S4P-02/S1-05) dan Platform Admin (S4P-37/38/39) --
	// field ini membedakan target-nya supaya kalimat naratif FE benar
	// (lihat auditNarrative.ts).
	TargetUserRole *string
	TargetTierName *string
	// ActorIP -- alamat IP request yang melakukan aksi (2026-08-29,
	// permintaan user: audit trail perlu info asal request). NULL untuk
	// entry lama sebelum kolom ini dipopulasikan (writeAuditLog di
	// account_repository.go).
	ActorIP *string
	// StateBefore/StateAfter (2026-08-29, permintaan user: perubahan satu
	// nilai skalar seperti session timeout/IP allowlist enabled/status
	// tier perlu menyertakan nilai sebelum-sesudah) -- NULL untuk action
	// yang tidak relevan (perubahan multi-field diwakilkan nama/kode unik
	// di Metadata, bukan diff per-field).
	StateBefore []byte
	StateAfter  []byte
	Metadata    []byte
	LoggedAt    time.Time
}

// PlatformAuditLogFilter -- field kosong/nil berarti tidak difilter
// (S4P-22 AC: "?action_type=tier_changed" dst).
type PlatformAuditLogFilter struct {
	ActionType string
	ActorID    string
	From       *time.Time
	To         *time.Time
	Limit      int
	Offset     int
}

type PlatformAuditRepository struct {
	db *pgxpool.Pool
}

func NewPlatformAuditRepository(db *pgxpool.Pool) *PlatformAuditRepository {
	return &PlatformAuditRepository{db: db}
}

// buildWhere -- WHERE clause + args yang sama dipakai count dan list, supaya
// keduanya tidak pernah "diam-diam berbeda" satu sama lain (paginasi salah).
func (f PlatformAuditLogFilter) buildWhere(startAt int) (where string, args []any) {
	clauses := []string{"1=1"}
	args = make([]any, 0, 4)
	n := startAt
	if f.ActionType != "" {
		n++
		clauses = append(clauses, fmt.Sprintf("pal.action = $%d", n))
		args = append(args, f.ActionType)
	}
	if f.ActorID != "" {
		n++
		clauses = append(clauses, fmt.Sprintf("pal.actor_id = $%d", n))
		args = append(args, f.ActorID)
	}
	if f.From != nil {
		n++
		clauses = append(clauses, fmt.Sprintf("pal.logged_at >= $%d", n))
		args = append(args, *f.From)
	}
	if f.To != nil {
		n++
		clauses = append(clauses, fmt.Sprintf("pal.logged_at <= $%d", n))
		args = append(args, *f.To)
	}
	where = strings.Join(clauses, " AND ")
	return where, args
}

// List -- S4P-22, dipakai list ber-paginasi (limit/offset) DAN export CSV
// (limit besar/tanpa batas, offset 0, lihat handler). withPlatformAdminRLS
// wajib -- platform_audit_logs kena RLS (migration
// 20260904090000_platform_audit_logs), tanpa ini hasilnya diam-diam 0 baris
// (pola sama IG-24).
func (r *PlatformAuditRepository) List(ctx context.Context, f PlatformAuditLogFilter) (entries []PlatformAuditLogEntry, total int, err error) {
	err = withPlatformAdminRLS(ctx, r.db, func(tx pgx.Tx) error {
		where, args := f.buildWhere(0)
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM platform_audit_logs pal WHERE `+where, args...).Scan(&total); err != nil {
			return fmt.Errorf("count: %w", err)
		}

		listWhere, listArgs := f.buildWhere(0)
		listArgs = append(listArgs, f.Limit, f.Offset)
		rows, err := tx.Query(ctx, `
			SELECT pal.id, pal.actor_id, u.email, u.display_name, pal.actor_role,
			       pal.action, pal.entity_type, pal.entity_id, target_u.display_name, target_u.platform_role, target_t.name,
			       pal.actor_ip::text, pal.state_before, pal.state_after, pal.metadata, pal.logged_at
			FROM platform_audit_logs pal
			LEFT JOIN users u ON u.id = pal.actor_id
			LEFT JOIN users target_u ON pal.entity_type = 'user' AND target_u.id = pal.entity_id
			LEFT JOIN service_tiers target_t ON pal.entity_type = 'tier' AND target_t.id = pal.entity_id
			WHERE `+listWhere+fmt.Sprintf(`
			ORDER BY pal.logged_at DESC
			LIMIT $%d OFFSET $%d
		`, len(listArgs)-1, len(listArgs)), listArgs...)
		if err != nil {
			return fmt.Errorf("query: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var e PlatformAuditLogEntry
			if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorEmail, &e.ActorDisplayName, &e.ActorRole,
				&e.Action, &e.EntityType, &e.EntityID, &e.TargetUserName, &e.TargetUserRole, &e.TargetTierName,
				&e.ActorIP, &e.StateBefore, &e.StateAfter, &e.Metadata, &e.LoggedAt); err != nil {
				return fmt.Errorf("scan: %w", err)
			}
			entries = append(entries, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, 0, fmt.Errorf("repository.PlatformAuditRepository.List: %w", err)
	}
	return entries, total, nil
}
