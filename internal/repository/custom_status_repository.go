// Package repository -- CustomStatusRepository (Task Management Core
// Phase 1, forward-pull; CRUD template workspace S4W-05, US-020/021; CRUD
// project-scope Track S5B, US-019). 5 status sistem di-seed otomatis saat
// workspace dibuat (WorkspaceRepository.Create); SETIAP project mendapat
// salinan independen dari status workspace-nya SAAT project dibuat
// (ProjectService.Create -> CloneForProject di bawah, project existing
// di-backfill migrasi 20261103090000) -- "berdiri sendiri" (PM Custom
// Status.dc.html): perubahan warna/urutan/toggle di satu project TIDAK
// mempengaruhi project lain ataupun template workspace asalnya.
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
	// RequirePic -- false: pindah KE status ini tidak menanyakan/menetapkan PIC
	// (status akhir seperti DONE/CANCELED); PIC fase sebelumnya dinonaktifkan.
	RequirePic bool
	CreatedAt  time.Time
	// TaskCount (S4W-05, US-021 AC "menyebutkan jumlah task yang saat ini
	// menggunakan status tersebut") -- cuma terisi lewat ListForScope/
	// Get (subquery COUNT), 0 default untuk hasil Create (status baru pasti
	// belum dipakai task manapun).
	TaskCount int
}

type CustomStatusRepository struct{}

func NewCustomStatusRepository() *CustomStatusRepository {
	return &CustomStatusRepository{}
}

const customStatusSelectColumns = `id, scope_type, scope_id, name, color_token, position, is_system, is_undefined, require_start_confirmation, require_pic, created_at`

// customStatusSelectWithCount -- dipakai ListForScope/Get (tampilan panel
// Kelola Custom Status AW/PM butuh jumlah task terdampak sebelum undefine,
// US-021) -- subquery COUNT per baris, bukan JOIN+GROUP BY, supaya query
// List tetap 1 baris per status walau task-nya banyak.
const customStatusSelectWithCount = `cs.id, cs.scope_type, cs.scope_id, cs.name, cs.color_token, cs.position, cs.is_system, cs.is_undefined, cs.require_start_confirmation, cs.require_pic, cs.created_at,
	(SELECT COUNT(*) FROM tasks t WHERE t.status_id = cs.id AND t.deleted_at IS NULL)`

func scanCustomStatus(row interface{ Scan(dest ...any) error }) (*CustomStatus, error) {
	var s CustomStatus
	if err := row.Scan(&s.ID, &s.ScopeType, &s.ScopeID, &s.Name, &s.ColorToken, &s.Position, &s.IsSystem, &s.IsUndefined, &s.RequireStartConfirmation, &s.RequirePic, &s.CreatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

func scanCustomStatusWithCount(row interface{ Scan(dest ...any) error }) (*CustomStatus, error) {
	var s CustomStatus
	if err := row.Scan(&s.ID, &s.ScopeType, &s.ScopeID, &s.Name, &s.ColorToken, &s.Position, &s.IsSystem, &s.IsUndefined, &s.RequireStartConfirmation, &s.RequirePic, &s.CreatedAt, &s.TaskCount); err != nil {
		return nil, err
	}
	return &s, nil
}

// ListForScope -- status board level workspace ATAU project (Track S5B),
// scopeType salah satu "workspace"/"project", scopeID workspace_id/project_id
// yang sesuai.
func (r *CustomStatusRepository) ListForScope(ctx context.Context, exec db.Executor, scopeType, scopeID string) ([]CustomStatus, error) {
	rows, err := exec.Query(ctx, `
		SELECT `+customStatusSelectWithCount+`
		FROM custom_statuses cs
		WHERE cs.scope_type = $1 AND cs.scope_id = $2
		ORDER BY cs.position ASC
	`, scopeType, scopeID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListForScope: %w", err)
	}
	defer rows.Close()

	list := make([]CustomStatus, 0)
	for rows.Next() {
		s, err := scanCustomStatusWithCount(rows)
		if err != nil {
			return nil, fmt.Errorf("repository.ListForScope: scan: %w", err)
		}
		list = append(list, *s)
	}
	return list, rows.Err()
}

// NameExists (S4W-05, US-020 AC "tidak boleh sama dengan status yang sudah
// ada") -- unik per scope (workspace ATAU project), name sudah di-uppercase
// di service sebelum dipanggil. excludeID kosong saat Create (belum ada ID
// untuk dikecualikan).
func (r *CustomStatusRepository) NameExists(ctx context.Context, exec db.Executor, scopeType, scopeID, name, excludeID string) (bool, error) {
	// excludeArg -- id kolom uuid, Create memanggil ini dengan excludeID
	// kosong (belum ada ID untuk dikecualikan). Kirim string kosong
	// langsung sebagai parameter $4 (yang inferensi tipenya ikut jadi uuid
	// lewat perbandingan id != $4) ditolak Postgres "invalid input syntax
	// for type uuid" (22P02) -- sama root cause dengan ProjectRepository.
	// Update.newPMArg. any(nil) supaya pgx mem-bind SQL NULL, dicek via
	// $4::uuid IS NULL di query.
	var excludeArg any
	if excludeID != "" {
		excludeArg = excludeID
	}
	var exists bool
	if err := exec.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM custom_statuses WHERE scope_type = $1 AND scope_id = $2 AND name = $3 AND ($4::uuid IS NULL OR id != $4::uuid))
	`, scopeType, scopeID, name, excludeArg).Scan(&exists); err != nil {
		return false, fmt.Errorf("repository.NameExists: %w", err)
	}
	return exists, nil
}

// Create (S4W-05/S5B, US-020/US-019) -- tambah status kustom baru ke
// scope tertentu pada posisi tertentu (1-indexed dari FE, "POSISI URUTAN"
// AW/PM Add Status.dc.html), menggeser status lain yang posisinya >= itu
// supaya urutan tetap konsisten (tidak ada celah/tabrakan). is_system
// selalu FALSE -- status sistem cuma ada lewat seed WorkspaceRepository.
// Create/CloneForProject. workspaceID dipakai audit trail SAJA (workspace
// pemilik project ini kalau scopeType='project', BUKAN scopeID -- lihat
// CustomStatusService.resolveWorkspaceID).
func (r *CustomStatusRepository) Create(ctx context.Context, exec db.Executor, scopeType, scopeID, name, colorToken string, position int, actorID, actorRole, workspaceID string) (*CustomStatus, error) {
	if _, err := exec.Exec(ctx, `
		UPDATE custom_statuses SET position = position + 1 WHERE scope_type = $1 AND scope_id = $2 AND position >= $3
	`, scopeType, scopeID, position); err != nil {
		return nil, fmt.Errorf("repository.Create: geser posisi: %w", err)
	}
	row := exec.QueryRow(ctx, `
		INSERT INTO custom_statuses (scope_type, scope_id, name, color_token, position, is_system, created_by)
		VALUES ($1, $2, $3, $4, $5, FALSE, $6)
		RETURNING `+customStatusSelectColumns+`
	`, scopeType, scopeID, name, colorToken, position, actorID)
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

// CloneForProject (Track S5B, US-019) -- dipanggil ProjectService.Create
// SETELAH project baru dibuat: menyalin status AKTIF (non-UNDEFINED)
// workspace pemilik project ini SAAT INI ke scope_type='project' milik
// project baru. Pola PERSIS migrasi 20261103090000 (backfill project
// existing) -- disatukan lewat SQL yang sama supaya tidak ada drift antara
// jalur migrasi dan jalur runtime. Tidak ada audit (bukan aksi user,
// inisialisasi sistem -- pola sama seed status sistem WorkspaceRepository.
// Create).
func (r *CustomStatusRepository) CloneForProject(ctx context.Context, exec db.Executor, workspaceID, projectID string) error {
	if _, err := exec.Exec(ctx, `
		INSERT INTO custom_statuses (scope_type, scope_id, name, color_token, position, is_system, require_start_confirmation, require_pic, created_by)
		SELECT 'project', $2, ws.name, ws.color_token, ws.position, ws.is_system, ws.require_start_confirmation, ws.require_pic, ws.created_by
		FROM custom_statuses ws
		WHERE ws.scope_type = 'workspace' AND ws.scope_id = $1 AND ws.is_undefined = FALSE
	`, workspaceID, projectID); err != nil {
		return fmt.Errorf("repository.CloneForProject: %w", err)
	}
	return nil
}

// UpdateNameColor (S4W-05, panel "KELOLA STATUS TEMPLATE" -> SIMPAN
// PERUBAHAN) -- name diabaikan/dikunci di layer service untuk status
// sistem (lihat CustomStatusService.UpdateNameColor), repo ini percaya
// nilai yang dikirim sudah benar. workspaceID -- lihat komentar Create.
func (r *CustomStatusRepository) UpdateNameColor(ctx context.Context, exec db.Executor, statusID, name, colorToken, actorID, actorRole, workspaceID string) error {
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
	if err := insertCustomStatusAudit(ctx, exec, actorID, actorRole, "custom_status.updated", statusID, workspaceID,
		map[string]any{"name": old.Name, "color_token": oldColor}, map[string]any{"name": name, "color_token": colorToken}); err != nil {
		return fmt.Errorf("repository.UpdateNameColor: audit: %w", err)
	}
	return nil
}

// Move (S4W-05/S5B, tombol ▲▼ "URUT") -- tukar posisi dengan tetangga
// (direction -1 = naik, +1 = turun), berlaku untuk status apa pun
// TERMASUK sistem (cuma nama+undefine yang dikunci untuk sistem, urutan
// tetap bisa diatur AW/PM -- sesuai AW/PM Custom Status.dc.html). No-op
// (bukan error) kalau sudah di ujung daftar -- dicek di service lewat
// ListForScope. workspaceID -- lihat komentar Create.
func (r *CustomStatusRepository) Move(ctx context.Context, exec db.Executor, scopeType, scopeID, statusID string, direction int, actorID, actorRole, workspaceID string) error {
	list, err := r.ListForScope(ctx, exec, scopeType, scopeID)
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
// ("JADIKAN UNDEFINED"): berhenti disalin ke project baru (kalau workspace)
// atau berhenti dipakai project ini (kalau project), task yang sudah
// memakainya tidak berubah. undefined=false ("PULIHKAN"): aktif kembali.
// workspaceID -- lihat komentar Create.
func (r *CustomStatusRepository) SetUndefined(ctx context.Context, exec db.Executor, statusID string, undefined bool, actorID, actorRole, workspaceID string) error {
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
	if err := insertCustomStatusAudit(ctx, exec, actorID, actorRole, action, statusID, workspaceID,
		map[string]any{"is_undefined": old.IsUndefined}, map[string]any{"is_undefined": undefined}); err != nil {
		return fmt.Errorf("repository.SetUndefined: audit: %w", err)
	}
	return nil
}

// GetBacklogStatus -- status default task baru (S4-13 AC: "status default
// BACKLOG"), diresolve dari scope (project sejak Track S5B -- setiap
// project sudah punya salinan sendiri lewat CloneForProject/migrasi
// backfill, TIDAK lagi lewat scope workspace).
func (r *CustomStatusRepository) GetBacklogStatus(ctx context.Context, exec db.Executor, scopeType, scopeID string) (*CustomStatus, error) {
	row := exec.QueryRow(ctx, `
		SELECT `+customStatusSelectColumns+`
		FROM custom_statuses
		WHERE scope_type = $1 AND scope_id = $2 AND name = 'BACKLOG'
	`, scopeType, scopeID)
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
// Audit trail (susulan S4W-05) ditambah di sini -- AW/PM Custom Status.dc.html
// eksplisit menjanjikan "konfirmasi mulai ... tercatat di Audit Trail
// workspace", sebelumnya toggle ini TIDAK pernah tercatat sama sekali.
// workspaceID -- lihat komentar Create.
func (r *CustomStatusRepository) SetRequireStartConfirmation(ctx context.Context, exec db.Executor, statusID string, require bool, actorID, actorRole, workspaceID string) error {
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
	if err := insertCustomStatusAudit(ctx, exec, actorID, actorRole, "custom_status.start_confirmation_changed", statusID, workspaceID,
		map[string]any{"require_start_confirmation": old.RequireStartConfirmation}, map[string]any{"require_start_confirmation": require}); err != nil {
		return fmt.Errorf("repository.SetRequireStartConfirmation: audit: %w", err)
	}
	return nil
}

// SetRequirePic -- PUT /statuses/:id/pic-requirement. Pola sama
// SetRequireStartConfirmation (satu parameter per status, diaudit).
func (r *CustomStatusRepository) SetRequirePic(ctx context.Context, exec db.Executor, statusID string, require bool, actorID, actorRole, workspaceID string) error {
	old, err := r.Get(ctx, exec, statusID)
	if err != nil {
		return err
	}
	tag, err := exec.Exec(ctx, `UPDATE custom_statuses SET require_pic = $2, updated_at = NOW() WHERE id = $1`, statusID, require)
	if err != nil {
		return fmt.Errorf("repository.SetRequirePic: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetRequirePic: %w", domain.ErrCustomStatusNotFound)
	}
	if err := insertCustomStatusAudit(ctx, exec, actorID, actorRole, "custom_status.pic_requirement_changed", statusID, workspaceID,
		map[string]any{"require_pic": old.RequirePic}, map[string]any{"require_pic": require}); err != nil {
		return fmt.Errorf("repository.SetRequirePic: audit: %w", err)
	}
	return nil
}

// insertCustomStatusAudit -- pola sama insertProjectAudit, entity_type
// 'custom_status'. workspaceID SELALU workspace nyata pemilik status ini
// (diresolve pemanggil lewat CustomStatusService.resolveWorkspaceID untuk
// status project-scoped, BUKAN scope_id/project_id langsung -- supaya
// Audit Trail Workspace tetap bisa menampilkan baris ini walau scope-nya
// project).
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
