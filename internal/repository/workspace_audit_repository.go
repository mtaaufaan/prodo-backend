// Package repository -- WorkspaceAuditRepository (S4W-16/17, US-058,
// desain "AW Audit Trail.dc.html"). READ-ONLY di atas `audit_logs`, pola
// PERSIS GroupAuditRepository -- lihat komentar package di sana untuk kenapa
// ini bukan tabel terpisah (implementation_gaps.md IG-45).
//
// BEDA PENTING dari GroupAuditRepository -- resolusi TargetName SELALU
// mengutamakan snapshot immutable di metadata/state_before/state_after
// (COALESCE berantai di bawah), live JOIN cuma FALLBACK untuk baris LAMA
// yang ditulis sebelum snapshot ditambahkan (atau untuk action yang belum
// tentu perlu snapshot, mis. project.deleted/restored yang tidak pernah
// menyertakan metadata.name sama sekali). GroupAuditRepository.TargetName
// live-JOIN TANPA snapshot sama sekali ke `webhook_configs` -- begitu
// baris itu di-HARD-DELETE (bukan soft-delete seperti tabel lain di
// codebase ini), JOIN-nya rusak permanen untuk SELURUH histori webhook itu
// (persis kelas bug implementation_gaps.md IG-29 -- tier/CIDR Platform
// Admin, komentar GroupAuditRepository sendiri mengakui gap ini apa
// adanya). Insert*Audit webhook/rule DI SINI sudah diperbaiki (lihat
// insertWebhookAudit/insertRuleAudit) supaya baris BARU tidak mewarisi gap
// yang sama -- `webhook_configs` SENGAJA TIDAK di-JOIN sama sekali di
// bawah (hard-delete, live JOIN untuknya SELALU salah, snapshot satu-
// satunya sumber yang benar).
//
// Live JOIN fallback HANYA untuk tabel yang diverifikasi TIDAK PERNAH
// hard-delete (soft-delete `deleted_at`, atau tidak pernah dihapus sama
// sekali): `projects`, `workspaces`, `automation_rules`, `custom_statuses`,
// `task_attachments`, `users` (entity_type 'workspace_member'/
// 'project_member', entity_id = user_id -- ditambah 'project_member'
// 2026-09-21, IG-89), `user_invitations` (status diturunkan accepted_at/
// cancelled_at, baris tidak pernah dihapus).
package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type WorkspaceAuditLogEntry struct {
	ID               string
	ActorID          *string
	ActorEmail       *string
	ActorDisplayName *string
	ActorRole        *string
	Action           string
	Type             string // CREATE/UPDATE/DELETE/ACCESS, dihitung server-side dari Action
	EntityType       string
	EntityID         *string
	TargetName       *string
	ActorIP          *string
	RequestPath      *string
	StateBefore      []byte
	StateAfter       []byte
	Metadata         []byte
	LoggedAt         time.Time
}

type WorkspaceAuditActor struct {
	ID   string
	Name string
}

// WorkspaceAuditLogFilter -- field kosong/nil berarti tidak difilter, pola
// PERSIS GroupAuditLogFilter.
type WorkspaceAuditLogFilter struct {
	WorkspaceID string
	// ProjectID terisi -> cakupan PROJECT (Audit Trail PM, IG-130): menggantikan
	// WorkspaceID sebagai penyaring cakupan.
	ProjectID  string
	ActorID    string
	ActionType string // CREATE/UPDATE/DELETE/ACCESS, "" = semua
	Days       int    // 0 = semua (retensi 3 tahun)
	Limit      int
	Offset     int
}

// workspaceScopeClause -- dual-clause: kolom asli audit_logs.workspace_id
// ATAU metadata->>'workspace_id' -- insertRuleAudit/insertWebhookAudit
// menulis lewat chokepoint generik writeAuditLog yang tidak punya
// parameter kolom workspace_id, jadi workspace_id-nya HANYA ada di
// metadata untuk baris itu (dikonfirmasi langsung ke data live, bukan
// asumsi -- lihat implementation_gaps.md untuk detail investigasi).
const workspaceScopeClause = `((al.workspace_id IS NOT NULL AND al.workspace_id = $%d) OR ((al.metadata ? 'workspace_id') AND (al.metadata->>'workspace_id') IS NOT NULL AND (al.metadata->>'workspace_id')::uuid = $%d))`

// projectScopeClause -- cakupan satu PROJECT (IG-130). Baris BARU membawa
// metadata.project_id (snapshot saat dicatat, lihat audit_project_snapshot.go);
// baris LAMA yang ditulis sebelum snapshot ditambahkan dikaitkan lewat tabel
// anak yang diverifikasi tidak dihapus keras -- task/sprint/status/rule/lampiran
// yang sudah hilang tidak bisa lagi dikaitkan (ACCEPTED RISK, tidak di-backfill
// karena audit immutable). Placeholder @P = nomor parameter id project.
const projectScopeClause = `(
	((al.metadata ? 'project_id') AND al.metadata->>'project_id' = @P::text)
	OR (al.entity_type = 'project' AND al.entity_id = @P::uuid)
	OR (al.entity_type = 'task' AND al.entity_id IN (SELECT t.id FROM tasks t WHERE t.project_id = @P::uuid))
	OR (al.entity_type = 'sprint' AND al.entity_id IN (SELECT s.id FROM sprints s WHERE s.project_id = @P::uuid))
	OR (al.entity_type = 'custom_status' AND al.entity_id IN (SELECT c.id FROM custom_statuses c WHERE c.scope_type = 'project' AND c.scope_id = @P::uuid))
	OR (al.entity_type = 'automation_rule' AND al.entity_id IN (SELECT r.id FROM automation_rules r WHERE r.scope_type = 'project' AND r.scope_id = @P::uuid))
	OR (al.entity_type = 'task_attachment' AND al.entity_id IN (SELECT ta.id FROM task_attachments ta JOIN tasks t2 ON t2.id = ta.task_id WHERE t2.project_id = @P::uuid))
)`

func projectScope(n int) string {
	return strings.ReplaceAll(projectScopeClause, "@P", fmt.Sprintf("$%d", n))
}

// targetNameExpr -- lihat komentar package: snapshot immutable DULU, live
// JOIN cuma fallback untuk baris/action yang belum punya snapshot -- HANYA
// ke tabel yang diverifikasi tidak pernah hard-delete. `webhook_configs`
// SENGAJA TIDAK ada di daftar JOIN (hard-delete, lihat komentar package).
const targetNameExpr = `COALESCE(
	al.metadata->>'name', al.metadata->>'webhook_name', al.metadata->>'rule_name', al.metadata->>'email',
	al.state_after->>'name', al.state_before->>'name',
	tp.name, tw.name, tar.name, tcs.name, tta.display_name, tsp.name,
	ttk.title, tu.display_name, tui.email
)`

func (f *WorkspaceAuditLogFilter) buildWhere() (where string, args []any) {
	n := 1
	clauses := []string{fmt.Sprintf(workspaceScopeClause, n, n)}
	args = []any{f.WorkspaceID}
	if f.ProjectID != "" {
		clauses = []string{projectScope(n)}
		args = []any{f.ProjectID}
	}
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

type WorkspaceAuditRepository struct{}

func NewWorkspaceAuditRepository() *WorkspaceAuditRepository { return &WorkspaceAuditRepository{} }

// List -- dipakai list ber-paginasi DAN ekspor CSV (limit besar, offset 0),
// pola PERSIS GroupAuditRepository.List.
func (r *WorkspaceAuditRepository) List(ctx context.Context, exec db.Executor, f *WorkspaceAuditLogFilter) (entries []WorkspaceAuditLogEntry, total int, err error) {
	where, args := f.buildWhere()
	countSQL := `SELECT count(*) FROM audit_logs al WHERE ` + where
	if err := exec.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repository.WorkspaceAuditRepository.List: count: %w", err)
	}

	listArgs := append(append([]any{}, args...), f.Limit, f.Offset)
	listSQL := fmt.Sprintf(`
		SELECT al.id, al.actor_id, u.email, u.display_name, al.actor_role,
		       al.action, (%s) AS type, al.entity_type, al.entity_id, %s AS target_name,
		       al.actor_ip::text, al.metadata->>'request_path',
		       al.state_before, al.state_after, al.metadata, al.logged_at
		FROM audit_logs al
		LEFT JOIN users u ON u.id = al.actor_id
		LEFT JOIN users tu ON al.entity_type IN ('workspace_member', 'project_member') AND tu.id = al.entity_id
		LEFT JOIN user_invitations tui ON al.entity_type = 'user_invitation' AND tui.id = al.entity_id
		LEFT JOIN projects tp ON al.entity_type = 'project' AND tp.id = al.entity_id
		LEFT JOIN workspaces tw ON al.entity_type = 'workspace' AND tw.id = al.entity_id
		LEFT JOIN automation_rules tar ON al.entity_type = 'automation_rule' AND tar.id = al.entity_id
		LEFT JOIN custom_statuses tcs ON al.entity_type = 'custom_status' AND tcs.id = al.entity_id
		LEFT JOIN task_attachments tta ON al.entity_type = 'task_attachment' AND tta.id = al.entity_id
		LEFT JOIN tasks ttk ON al.entity_type = 'task' AND ttk.id = al.entity_id
		LEFT JOIN sprints tsp ON al.entity_type = 'sprint' AND tsp.id = al.entity_id
		WHERE %s
		ORDER BY al.logged_at DESC
		LIMIT $%d OFFSET $%d
	`, actionTypeCase, targetNameExpr, where, len(listArgs)-1, len(listArgs))
	rows, err := exec.Query(ctx, listSQL, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("repository.WorkspaceAuditRepository.List: query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var e WorkspaceAuditLogEntry
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorEmail, &e.ActorDisplayName, &e.ActorRole,
			&e.Action, &e.Type, &e.EntityType, &e.EntityID, &e.TargetName,
			&e.ActorIP, &e.RequestPath, &e.StateBefore, &e.StateAfter, &e.Metadata, &e.LoggedAt); err != nil {
			return nil, 0, fmt.Errorf("repository.WorkspaceAuditRepository.List: scan: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repository.WorkspaceAuditRepository.List: %w", err)
	}
	return entries, total, nil
}

// ListActors -- opsi dropdown "AKTOR", dari SELURUH histori workspace ini
// (bukan cuma halaman aktif). `al.actor_role NOT IN (...)` (2026-09-21,
// IG-89, dikonfirmasi user "aktor GA dihilangkan dari pilihan aktor di
// AW. AW hanya bisa melihat AW, DV, PM, editor, approver, viewer") --
// BLOCKLIST, bukan allowlist `= 'member'`: query empiris ke data live
// menemukan audit_logs.actor_role TIDAK konsisten -- sebagian jalur
// insert menulis literal platform_role JWT ('member'), sebagian menulis
// LANGSUNG workspace_role granular ('admin_workspace', dst -- kemungkinan
// jalur lama sebelum konvensi claims.PlatformRole seragam), dan banyak
// baris lama actor_role NULL (gap terpisah, tidak diperbaiki di sini, di
// luar cakupan permintaan ini). Allowlist `= 'member'` SALAH dicoba lebih
// dulu -- diam-diam menghilangkan aktor workspace asli yang actor_role-nya
// bukan persis 'member'. Blocklist 3 role platform-level inilah yang
// benar: group_admin/platform_admin/executive beraksi di workspace ini
// lewat context-switch/bypass, bukan keanggotaan asli. `actor_role IS NULL
// OR ... NOT IN (...)` WAJIB, bukan cuma `NOT IN` -- jebakan logika
// tiga-nilai SQL, `NULL NOT IN (...)` selalu UNKNOWN (bukan TRUE) sehingga
// baris actor_role NULL diam-diam ikut tersingkir kalau ditulis polos
// (ditemukan lewat verifikasi live: 4 aktor workspace asli hilang dari
// dropdown padahal seharusnya tetap muncul).
func (r *WorkspaceAuditRepository) ListActors(ctx context.Context, exec db.Executor, workspaceID string) ([]WorkspaceAuditActor, error) {
	rows, err := exec.Query(ctx, `
		SELECT DISTINCT u.id, u.display_name
		FROM audit_logs al
		JOIN users u ON u.id = al.actor_id
		WHERE `+fmt.Sprintf(workspaceScopeClause, 1, 1)+` AND (al.actor_role IS NULL OR al.actor_role NOT IN ('platform_admin', 'group_admin', 'executive'))
		ORDER BY u.display_name
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repository.WorkspaceAuditRepository.ListActors: %w", err)
	}
	defer rows.Close()

	list := make([]WorkspaceAuditActor, 0)
	for rows.Next() {
		var a WorkspaceAuditActor
		if err := rows.Scan(&a.ID, &a.Name); err != nil {
			return nil, fmt.Errorf("repository.WorkspaceAuditRepository.ListActors: scan: %w", err)
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// ListProjectActors -- opsi dropdown AKTOR untuk cakupan project: seluruh histori
// project ini, role platform-level (GA/PA/eksekutif yang beraksi lewat bypass)
// tidak ikut -- aturan sama ListActors (IG-89).
func (r *WorkspaceAuditRepository) ListProjectActors(ctx context.Context, exec db.Executor, projectID string) ([]WorkspaceAuditActor, error) {
	rows, err := exec.Query(ctx, `
		SELECT DISTINCT u.id, u.display_name
		FROM audit_logs al
		JOIN users u ON u.id = al.actor_id
		WHERE `+projectScope(1)+` AND (al.actor_role IS NULL OR al.actor_role NOT IN ('platform_admin', 'group_admin', 'executive'))
		ORDER BY u.display_name
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("repository.WorkspaceAuditRepository.ListProjectActors: %w", err)
	}
	defer rows.Close()

	list := make([]WorkspaceAuditActor, 0)
	for rows.Next() {
		var a WorkspaceAuditActor
		if err := rows.Scan(&a.ID, &a.Name); err != nil {
			return nil, fmt.Errorf("repository.WorkspaceAuditRepository.ListProjectActors: scan: %w", err)
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// ProjectInfo -- workspace + nama project (gerbang otorisasi + snapshot nama).
func (r *WorkspaceAuditRepository) ProjectInfo(ctx context.Context, exec db.Executor, projectID string) (workspaceID, name string, err error) {
	err = exec.QueryRow(ctx, `SELECT workspace_id, name FROM projects WHERE id = $1`, projectID).Scan(&workspaceID, &name)
	if err != nil {
		return "", "", fmt.Errorf("repository.WorkspaceAuditRepository.ProjectInfo: %w", domain.ErrProjectNotFound)
	}
	return workspaceID, name, nil
}

// LogExport -- audit ekspor CSV Audit Trail (desain "AW Audit Trail": "Aksi ekspor
// ini juga dicatat"). Action berakhiran .report_downloaded -> otomatis ACCESS
// (actionTypeCase). projectID kosong = ekspor audit workspace.
func (r *WorkspaceAuditRepository) LogExport(ctx context.Context, exec db.Executor, actorID, actorRole, workspaceID, projectID, name string, rows int) error {
	after := map[string]any{"rows": rows, "format": "csv"}
	if projectID != "" {
		after["scope"] = "project"
		return insertProjectAudit(ctx, exec, actorID, actorRole, "audit.report_downloaded", projectID, workspaceID, nil, after, map[string]any{"name": name})
	}
	after["scope"] = "workspace"
	meta := map[string]any{"workspace_id": workspaceID}
	if name != "" {
		meta["name"] = name // kosong -> nama workspace diresolve live-JOIN (workspace soft-delete)
	}
	return writeAuditLog(ctx, exec, "audit_logs", actorID, actorRole, "audit.report_downloaded", "workspace", &workspaceID, meta, nil, after)
}
