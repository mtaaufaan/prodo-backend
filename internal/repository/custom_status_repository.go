// Package repository -- CustomStatusRepository (Task Management Core
// Phase 1, forward-pull; CRUD template workspace S4W-05, US-020/021). 5
// status sistem di-seed otomatis saat workspace dibuat
// (WorkspaceRepository.Create). CRUD status PROJECT-level (PM, US-019)
// tetap scope terpisah, belum dibangun -- scope_type='project' tidak
// pernah ada baris di codebase ini sampai itu dibangun.
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type CustomStatus struct {
	ID                       string
	ScopeType                string
	ScopeID                  string
	Name                     string
	ColorToken               *string
	Position                 int
	IsSystem                 bool
	IsUndefined              bool
	RequireStartConfirmation bool
	CreatedAt                time.Time
	// TaskCount (S4W-05, US-021 AC "menyebutkan jumlah task yang saat ini
	// menggunakan status tersebut") -- cuma terisi lewat ListForWorkspace/
	// Get (subquery COUNT), 0 default untuk hasil Create (status baru pasti
	// belum dipakai task manapun).
	TaskCount int
}

type CustomStatusRepository struct{}

func NewCustomStatusRepository() *CustomStatusRepository {
	return &CustomStatusRepository{}
}

const customStatusSelectColumns = `id, scope_type, scope_id, name, color_token, position, is_system, is_undefined, require_start_confirmation, created_at`

// customStatusSelectWithCount -- dipakai ListForWorkspace/Get (tampilan
// panel Kelola AW Custom Status butuh jumlah task terdampak sebelum
// undefine, US-021) -- subquery COUNT per baris, bukan JOIN+GROUP BY,
// supaya query List tetap 1 baris per status walau task-nya banyak.
const customStatusSelectWithCount = `cs.id, cs.scope_type, cs.scope_id, cs.name, cs.color_token, cs.position, cs.is_system, cs.is_undefined, cs.require_start_confirmation, cs.created_at,
	(SELECT COUNT(*) FROM tasks t WHERE t.status_id = cs.id AND t.deleted_at IS NULL)`

func scanCustomStatus(row interface{ Scan(dest ...any) error }) (*CustomStatus, error) {
	var s CustomStatus
	if err := row.Scan(&s.ID, &s.ScopeType, &s.ScopeID, &s.Name, &s.ColorToken, &s.Position, &s.IsSystem, &s.IsUndefined, &s.RequireStartConfirmation, &s.CreatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

func scanCustomStatusWithCount(row interface{ Scan(dest ...any) error }) (*CustomStatus, error) {
	var s CustomStatus
	if err := row.Scan(&s.ID, &s.ScopeType, &s.ScopeID, &s.Name, &s.ColorToken, &s.Position, &s.IsSystem, &s.IsUndefined, &s.RequireStartConfirmation, &s.CreatedAt, &s.TaskCount); err != nil {
		return nil, err
	}
	return &s, nil
}

// ListForWorkspace -- status board level workspace (project-scoped status
// custom belum dibangun Phase 1, lihat komentar package).
func (r *CustomStatusRepository) ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]CustomStatus, error) {
	rows, err := exec.Query(ctx, `
		SELECT `+customStatusSelectWithCount+`
		FROM custom_statuses cs
		WHERE cs.scope_type = 'workspace' AND cs.scope_id = $1
		ORDER BY cs.position ASC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListForWorkspace: %w", err)
	}
	defer rows.Close()

	list := make([]CustomStatus, 0)
	for rows.Next() {
		s, err := scanCustomStatusWithCount(rows)
		if err != nil {
			return nil, fmt.Errorf("repository.ListForWorkspace: scan: %w", err)
		}
		list = append(list, *s)
	}
	return list, rows.Err()
}

// NameExists (S4W-05, US-020 AC "tidak boleh sama dengan status yang sudah
// ada") -- unik per workspace, name sudah di-uppercase di service sebelum
// dipanggil. excludeID kosong saat Create (belum ada ID untuk dikecualikan).
func (r *CustomStatusRepository) NameExists(ctx context.Context, exec db.Executor, workspaceID, name, excludeID string) (bool, error) {
	// excludeArg -- id kolom uuid, Create memanggil ini dengan excludeID
	// kosong (belum ada ID untuk dikecualikan). Kirim string kosong
	// langsung sebagai parameter $3 (yang inferensi tipenya ikut jadi uuid
	// lewat perbandingan id != $3) ditolak Postgres "invalid input syntax
	// for type uuid" (22P02) -- sama root cause dengan ProjectRepository.
	// Update.newPMArg. any(nil) supaya pgx mem-bind SQL NULL, dicek via
	// $3::uuid IS NULL di query.
	var excludeArg any
	if excludeID != "" {
		excludeArg = excludeID
	}
	var exists bool
	if err := exec.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM custom_statuses WHERE scope_type = 'workspace' AND scope_id = $1 AND name = $2 AND ($3::uuid IS NULL OR id != $3::uuid))
	`, workspaceID, name, excludeArg).Scan(&exists); err != nil {
		return false, fmt.Errorf("repository.NameExists: %w", err)
	}
	return exists, nil
}

// Create (S4W-05, US-020) -- tambah status kustom baru ke template
// workspace pada posisi tertentu (1-indexed dari FE, "POSISI URUTAN" AW Add
// Status.dc.html), menggeser status lain yang posisinya >= itu supaya
// urutan tetap konsisten (tidak ada celah/tabrakan). is_system selalu
// FALSE -- status sistem cuma ada lewat seed WorkspaceRepository.Create.
func (r *CustomStatusRepository) Create(ctx context.Context, exec db.Executor, workspaceID, name, colorToken string, position int, actorID, actorRole string) (*CustomStatus, error) {
	if _, err := exec.Exec(ctx, `
		UPDATE custom_statuses SET position = position + 1 WHERE scope_type = 'workspace' AND scope_id = $1 AND position >= $2
	`, workspaceID, position); err != nil {
		return nil, fmt.Errorf("repository.Create: geser posisi: %w", err)
	}
	row := exec.QueryRow(ctx, `
		INSERT INTO custom_statuses (scope_type, scope_id, name, color_token, position, is_system, created_by)
		VALUES ('workspace', $1, $2, $3, $4, FALSE, $5)
		RETURNING `+customStatusSelectColumns+`
	`, workspaceID, name, colorToken, position, actorID)
	s, err := scanCustomStatus(row)
	if err != nil {
		return nil, fmt.Errorf("repository.Create: %w", err)
	}
	if err := insertCustomStatusAudit(ctx, exec, actorID, actorRole, "custom_status.created", s.ID, workspaceID, nil,
		map[string]any{"name": name, "color_token": colorToken, "position": position}); err != nil {
		return nil, fmt.Errorf("repository.Create: audit: %w", err)
	}
	return s, nil
}

// UpdateNameColor (S4W-05, panel "KELOLA STATUS TEMPLATE" -> SIMPAN
// PERUBAHAN) -- name diabaikan/dikunci di layer service untuk status
// sistem (lihat CustomStatusService.UpdateNameColor), repo ini percaya
// nilai yang dikirim sudah benar.
func (r *CustomStatusRepository) UpdateNameColor(ctx context.Context, exec db.Executor, statusID, name, colorToken, actorID, actorRole string) error {
	old, err := r.Get(ctx, exec, statusID)
	if err != nil {
		return err
	}
	tag, err := exec.Exec(ctx, `UPDATE custom_statuses SET name = $2, color_token = $3, updated_at = NOW() WHERE id = $1`, statusID, name, colorToken)
	if err != nil {
		return fmt.Errorf("repository.UpdateNameColor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.UpdateNameColor: %w", domain.ErrCustomStatusNotFound)
	}
	oldColor := ""
	if old.ColorToken != nil {
		oldColor = *old.ColorToken
	}
	if err := insertCustomStatusAudit(ctx, exec, actorID, actorRole, "custom_status.updated", statusID, old.ScopeID,
		map[string]any{"name": old.Name, "color_token": oldColor}, map[string]any{"name": name, "color_token": colorToken}); err != nil {
		return fmt.Errorf("repository.UpdateNameColor: audit: %w", err)
	}
	return nil
}

// Move (S4W-05, tombol ▲▼ "URUT") -- tukar posisi dengan tetangga
// (direction -1 = naik, +1 = turun), berlaku untuk status apa pun
// TERMASUK sistem (cuma nama+undefine yang dikunci untuk sistem, urutan
// tetap bisa diatur AW -- sesuai AW Custom Status.dc.html). No-op (bukan
// error) kalau sudah di ujung daftar -- dicek di service lewat ListForWorkspace.
func (r *CustomStatusRepository) Move(ctx context.Context, exec db.Executor, workspaceID, statusID string, direction int, actorID, actorRole string) error {
	list, err := r.ListForWorkspace(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("repository.Move: %w", err)
	}
	idx := -1
	for i := range list {
		if list[i].ID == statusID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("repository.Move: %w", domain.ErrCustomStatusNotFound)
	}
	neighbor := idx + direction
	if neighbor < 0 || neighbor >= len(list) {
		return nil
	}
	a, b := list[idx], list[neighbor]
	if _, err := exec.Exec(ctx, `UPDATE custom_statuses SET position = $2, updated_at = NOW() WHERE id = $1`, a.ID, b.Position); err != nil {
		return fmt.Errorf("repository.Move: %w", err)
	}
	if _, err := exec.Exec(ctx, `UPDATE custom_statuses SET position = $2, updated_at = NOW() WHERE id = $1`, b.ID, a.Position); err != nil {
		return fmt.Errorf("repository.Move: %w", err)
	}
	if err := insertCustomStatusAudit(ctx, exec, actorID, actorRole, "custom_status.reordered", a.ID, workspaceID,
		map[string]any{"position": a.Position}, map[string]any{"position": b.Position}); err != nil {
		return fmt.Errorf("repository.Move: audit: %w", err)
	}
	return nil
}

// SetUndefined (S4W-05, US-021) -- toggle is_undefined. undefined=true
// ("JADIKAN UNDEFINED"): berhenti disalin ke project baru, task yang sudah
// memakainya tidak berubah (project_statuses tidak disentuh sama sekali).
// undefined=false ("PULIHKAN KE TEMPLATE"): aktif kembali. Dampak ke rule
// automation (US-021 AC "rule ... otomatis inactive") SENGAJA TIDAK
// ditangani di sini -- tabel automation_rules belum ada sampai S4W-09/H11
// (lihat implementation_gaps.md IG-78), akan disambung begitu tabel itu ada.
func (r *CustomStatusRepository) SetUndefined(ctx context.Context, exec db.Executor, statusID string, undefined bool, actorID, actorRole string) error {
	old, err := r.Get(ctx, exec, statusID)
	if err != nil {
		return err
	}
	tag, err := exec.Exec(ctx, `UPDATE custom_statuses SET is_undefined = $2, updated_at = NOW() WHERE id = $1`, statusID, undefined)
	if err != nil {
		return fmt.Errorf("repository.SetUndefined: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetUndefined: %w", domain.ErrCustomStatusNotFound)
	}
	action := "custom_status.undefined"
	if !undefined {
		action = "custom_status.restored"
	}
	if err := insertCustomStatusAudit(ctx, exec, actorID, actorRole, action, statusID, old.ScopeID,
		map[string]any{"is_undefined": old.IsUndefined}, map[string]any{"is_undefined": undefined}); err != nil {
		return fmt.Errorf("repository.SetUndefined: audit: %w", err)
	}
	return nil
}

// GetBacklogStatus -- status default task baru (S4-13 AC: "status default
// BACKLOG"), diresolve dari scope workspace pemilik project.
func (r *CustomStatusRepository) GetBacklogStatus(ctx context.Context, exec db.Executor, workspaceID string) (*CustomStatus, error) {
	row := exec.QueryRow(ctx, `
		SELECT `+customStatusSelectColumns+`
		FROM custom_statuses
		WHERE scope_type = 'workspace' AND scope_id = $1 AND name = 'BACKLOG'
	`, workspaceID)
	s, err := scanCustomStatus(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.GetBacklogStatus: %w", domain.ErrCustomStatusNotFound)
		}
		return nil, fmt.Errorf("repository.GetBacklogStatus: %w", err)
	}
	return s, nil
}

func (r *CustomStatusRepository) Get(ctx context.Context, exec db.Executor, statusID string) (*CustomStatus, error) {
	row := exec.QueryRow(ctx, `SELECT `+customStatusSelectColumns+` FROM custom_statuses WHERE id = $1`, statusID)
	s, err := scanCustomStatus(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.Get: %w", domain.ErrCustomStatusNotFound)
		}
		return nil, fmt.Errorf("repository.Get: %w", err)
	}
	return s, nil
}

// SetRequireStartConfirmation -- PUT /statuses/:id (Phase 4, US-018b/S4-64).
// Audit trail (susulan S4W-05) ditambah di sini -- AW Custom Status.dc.html
// eksplisit menjanjikan "konfirmasi mulai ... tercatat di Audit Trail
// workspace", sebelumnya toggle ini TIDAK pernah tercatat sama sekali.
func (r *CustomStatusRepository) SetRequireStartConfirmation(ctx context.Context, exec db.Executor, statusID string, require bool, actorID, actorRole string) error {
	old, err := r.Get(ctx, exec, statusID)
	if err != nil {
		return err
	}
	tag, err := exec.Exec(ctx, `UPDATE custom_statuses SET require_start_confirmation = $2, updated_at = NOW() WHERE id = $1`, statusID, require)
	if err != nil {
		return fmt.Errorf("repository.SetRequireStartConfirmation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetRequireStartConfirmation: %w", domain.ErrCustomStatusNotFound)
	}
	if err := insertCustomStatusAudit(ctx, exec, actorID, actorRole, "custom_status.start_confirmation_changed", statusID, old.ScopeID,
		map[string]any{"require_start_confirmation": old.RequireStartConfirmation}, map[string]any{"require_start_confirmation": require}); err != nil {
		return fmt.Errorf("repository.SetRequireStartConfirmation: audit: %w", err)
	}
	return nil
}

// insertCustomStatusAudit -- pola sama insertProjectAudit, entity_type
// 'custom_status'. workspaceID di sini SELALU status.ScopeID (status
// project-scoped belum dibangun Phase 1, lihat komentar package -- kalau
// nanti ada, pemanggil wajib resolve workspace pemilik project itu dulu,
// TIDAK bisa asumsikan ScopeID = workspace_id lagi).
func insertCustomStatusAudit(ctx context.Context, exec db.Executor, actorID, actorRole, action, statusID, workspaceID string, stateBefore, stateAfter map[string]any) error {
	ip, path := requestMetaFromContext(ctx)
	metadata := map[string]any{}
	if path != "" {
		metadata["request_path"] = path
	}
	beforeJSON, err := marshalIfNotEmpty(stateBefore)
	if err != nil {
		return fmt.Errorf("insertCustomStatusAudit: encode state_before: %w", err)
	}
	afterJSON, err := marshalIfNotEmpty(stateAfter)
	if err != nil {
		return fmt.Errorf("insertCustomStatusAudit: encode state_after: %w", err)
	}
	metaJSON, err := marshalIfNotEmpty(metadata)
	if err != nil {
		return fmt.Errorf("insertCustomStatusAudit: encode metadata: %w", err)
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, workspace_id, actor_ip, state_before, state_after, metadata)
		VALUES ($1, $2, $3, 'custom_status', $4, $5, $6::inet, $7, $8, $9)
	`, actorID, actorRole, action, statusID, workspaceID, ip, beforeJSON, afterJSON, metaJSON)
	return err
}
