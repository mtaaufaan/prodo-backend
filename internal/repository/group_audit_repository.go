// Package repository -- GroupAuditRepository (Audit Trail, Track S4G,
// desain "GA Audit Trail.dc.html"). READ-ONLY di atas `audit_logs` yang
// SUDAH otomatis terisi oleh insertOrgAudit/insertWorkspaceAudit/
// insertWebhookAudit -- lihat migrasi 20260922090000 untuk kenapa ini
// bukan tabel `group_audit_logs` terpisah seperti sprint_backlog.md
// (implementation_gaps.md IG-45, dikonfirmasi user).
package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

type GroupAuditLogEntry struct {
	ID               string
	ActorID          *string
	ActorEmail       *string
	ActorDisplayName *string
	ActorRole        *string
	Action           string
	Type             string // CREATE/UPDATE/DELETE/ACCESS, dihitung server-side dari Action
	EntityType       string
	EntityID         *string
	OrgName          *string // via org_id join -- NULL untuk webhook cakupan "seluruh grup"
	// TargetName -- snapshot LIVE JOIN nama workspace/webhook (entity_id) --
	// sama keterbatasan komentar PlatformAuditLogEntry.TargetTierName: kalau
	// baris ditumpuk (mis. webhook.deleted), nama jadi tidak terselesaikan
	// lagi (LEFT JOIN NULL) karena insertWorkspaceAudit/insertWebhookAudit
	// (ditulis sebelum konvensi name-snapshot di metadata) tidak menyimpan
	// snapshot -- diterima apa adanya, bukan retrofit tabel yang sudah jalan.
	TargetName  *string
	ActorIP     *string
	StateBefore []byte
	StateAfter  []byte
	Metadata    []byte
	LoggedAt    time.Time
}

// GroupAuditActor -- satu opsi dropdown "AKTOR" (desain: nama, bukan UUID).
type GroupAuditActor struct {
	ID   string
	Name string
}

// GroupAuditLogFilter -- field kosong/nil berarti tidak difilter.
type GroupAuditLogFilter struct {
	GroupID    string
	ActorID    string
	ActionType string // CREATE/UPDATE/DELETE/ACCESS, dicocokkan ke Type hasil hitung, "" = semua
	Days       int    // 0 = semua (retensi 3 tahun, lihat design footer)
	Limit      int
	Offset     int
}

// groupScopeClause + actionTypeCase -- dipakai berulang, satu sumber
// kebenaran supaya count dan list tidak pernah diam-diam beda.
const groupScopeClause = `(o.group_id = $%d OR (al.metadata ? 'group_id' AND (al.metadata->>'group_id')::uuid = $%d))`

// 'user.login'/'user.backup_code_used' (2026-09-11, IG-57) ditambahkan
// literal ke cabang ACCESS -- action-nya TIDAK di-rename jadi 'auth.*'
// supaya tidak menyentuh Platform Admin (logAudit yang sama menulis nama
// action ini ke platform_audit_logs juga, dan narrative Platform Admin
// sendiri sudah hardcode 'user.login').
const actionTypeCase = `CASE
	WHEN al.action LIKE '%%.created' THEN 'CREATE'
	WHEN al.action LIKE '%%.deleted' THEN 'DELETE'
	WHEN al.action LIKE 'session.%%' OR al.action LIKE 'auth.%%' OR al.action IN ('user.login', 'user.backup_code_used') THEN 'ACCESS'
	ELSE 'UPDATE'
END`

func (f GroupAuditLogFilter) buildWhere() (where string, args []any) {
	n := 1
	clauses := []string{fmt.Sprintf(groupScopeClause, n, n)}
	args = []any{f.GroupID}
	if f.ActorID != "" {
		n++
		clauses = append(clauses, fmt.Sprintf("al.actor_id = $%d", n))
		args = append(args, f.ActorID)
	}
	if f.ActionType != "" {
		n++
		clauses = append(clauses, fmt.Sprintf("(%s) = $%d", actionTypeCase, n))
		args = append(args, f.ActionType)
	}
	if f.Days > 0 {
		n++
		clauses = append(clauses, fmt.Sprintf("al.logged_at > NOW() - make_interval(days => $%d)", n))
		args = append(args, f.Days)
	}
	return strings.Join(clauses, " AND "), args
}

type GroupAuditRepository struct{}

func NewGroupAuditRepository() *GroupAuditRepository {
	return &GroupAuditRepository{}
}

// List -- dipakai list ber-paginasi DAN ekspor CSV (limit besar, offset 0).
func (r *GroupAuditRepository) List(ctx context.Context, exec db.Executor, f GroupAuditLogFilter) (entries []GroupAuditLogEntry, total int, err error) {
	where, args := f.buildWhere()
	countSQL := `
		SELECT count(*)
		FROM audit_logs al
		LEFT JOIN organizations o ON o.id = al.org_id
		WHERE ` + where
	if err := exec.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repository.GroupAuditRepository.List: count: %w", err)
	}

	listArgs := append(append([]any{}, args...), f.Limit, f.Offset)
	listSQL := fmt.Sprintf(`
		SELECT al.id, al.actor_id, u.email, u.display_name, al.actor_role,
		       al.action, (%s) AS type, al.entity_type, al.entity_id, o.name,
		       COALESCE(tw.name, twc.name), al.actor_ip::text, al.state_before, al.state_after, al.metadata, al.logged_at
		FROM audit_logs al
		LEFT JOIN organizations o ON o.id = al.org_id
		LEFT JOIN users u ON u.id = al.actor_id
		LEFT JOIN workspaces tw ON al.entity_type = 'workspace' AND tw.id = al.entity_id
		LEFT JOIN webhook_configs twc ON al.entity_type = 'webhook' AND twc.id = al.entity_id
		WHERE %s
		ORDER BY al.logged_at DESC
		LIMIT $%d OFFSET $%d
	`, actionTypeCase, where, len(listArgs)-1, len(listArgs))
	rows, err := exec.Query(ctx, listSQL, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("repository.GroupAuditRepository.List: query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var e GroupAuditLogEntry
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorEmail, &e.ActorDisplayName, &e.ActorRole,
			&e.Action, &e.Type, &e.EntityType, &e.EntityID, &e.OrgName, &e.TargetName,
			&e.ActorIP, &e.StateBefore, &e.StateAfter, &e.Metadata, &e.LoggedAt); err != nil {
			return nil, 0, fmt.Errorf("repository.GroupAuditRepository.List: scan: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repository.GroupAuditRepository.List: %w", err)
	}
	return entries, total, nil
}

// ListActors -- opsi dropdown "AKTOR", diambil dari SELURUH histori grup
// (bukan cuma halaman yang sedang tampil) supaya daftarnya stabil dan
// lengkap walau lagi difilter aksi/rentang tertentu.
func (r *GroupAuditRepository) ListActors(ctx context.Context, exec db.Executor, groupID string) ([]GroupAuditActor, error) {
	rows, err := exec.Query(ctx, `
		SELECT DISTINCT u.id, u.display_name
		FROM audit_logs al
		LEFT JOIN organizations o ON o.id = al.org_id
		JOIN users u ON u.id = al.actor_id
		WHERE `+fmt.Sprintf(groupScopeClause, 1, 1)+`
		ORDER BY u.display_name
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("repository.GroupAuditRepository.ListActors: %w", err)
	}
	defer rows.Close()

	list := make([]GroupAuditActor, 0)
	for rows.Next() {
		var a GroupAuditActor
		if err := rows.Scan(&a.ID, &a.Name); err != nil {
			return nil, fmt.Errorf("repository.GroupAuditRepository.ListActors: scan: %w", err)
		}
		list = append(list, a)
	}
	return list, rows.Err()
}
