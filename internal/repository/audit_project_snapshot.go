package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

// Snapshot identitas + project untuk audit trail level PROJECT (implementation_gaps.md
// IG-130). Aturan standar audit trail (IG-26/IG-29): identitas target dan kolom
// cakupan (project_id) WAJIB ditulis ke metadata SAAT mencatat -- bukan hanya
// diresolusi lewat live JOIN saat dibaca, karena entitas bisa hilang (task/sprint/
// lampiran yang di-purge) dan baris lama lalu tak bisa dikaitkan ke project lagi.
// Hanya berlaku maju: baris lama tidak di-backfill (audit immutable).
//
// Semua fungsi mengembalikan map kosong (bukan error) bila baris tidak ditemukan --
// mis. entitas yang dihapus tepat sebelum audit ditulis -- pemanggil boleh
// menimpa dengan nilai yang sudah diketahuinya.

func scanOptional(row pgx.Row, dest ...any) (found bool, err error) {
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func auditSnapshotTask(ctx context.Context, exec db.Executor, taskID string) (map[string]any, error) {
	var projectID, code, title string
	found, err := scanOptional(exec.QueryRow(ctx, `SELECT project_id::text, COALESCE(task_code, ''), title FROM tasks WHERE id = $1`, taskID), &projectID, &code, &title)
	if err != nil {
		return nil, fmt.Errorf("auditSnapshotTask: %w", err)
	}
	if !found {
		return map[string]any{}, nil
	}
	return map[string]any{"project_id": projectID, "task_code": code, "name": title}, nil
}

func auditSnapshotSprint(ctx context.Context, exec db.Executor, sprintID string) (map[string]any, error) {
	var projectID, name string
	found, err := scanOptional(exec.QueryRow(ctx, `SELECT project_id::text, name FROM sprints WHERE id = $1`, sprintID), &projectID, &name)
	if err != nil {
		return nil, fmt.Errorf("auditSnapshotSprint: %w", err)
	}
	if !found {
		return map[string]any{}, nil
	}
	return map[string]any{"project_id": projectID, "name": name}, nil
}

// auditSnapshotStatus -- project_id hanya untuk status scope 'project'; status
// scope workspace tidak termasuk audit project mana pun.
func auditSnapshotStatus(ctx context.Context, exec db.Executor, statusID string) (map[string]any, error) {
	var scopeType, scopeID, name string
	found, err := scanOptional(exec.QueryRow(ctx, `SELECT scope_type, scope_id::text, name FROM custom_statuses WHERE id = $1`, statusID), &scopeType, &scopeID, &name)
	if err != nil {
		return nil, fmt.Errorf("auditSnapshotStatus: %w", err)
	}
	if !found {
		return map[string]any{}, nil
	}
	m := map[string]any{"name": name}
	if scopeType == "project" {
		m["project_id"] = scopeID
	}
	return m, nil
}

func auditSnapshotRule(ctx context.Context, exec db.Executor, ruleID string) (map[string]any, error) {
	var scopeType, scopeID string
	found, err := scanOptional(exec.QueryRow(ctx, `SELECT scope_type, scope_id::text FROM automation_rules WHERE id = $1`, ruleID), &scopeType, &scopeID)
	if err != nil {
		return nil, fmt.Errorf("auditSnapshotRule: %w", err)
	}
	if !found || scopeType != "project" {
		return map[string]any{}, nil
	}
	return map[string]any{"project_id": scopeID}, nil
}

func auditSnapshotAttachment(ctx context.Context, exec db.Executor, attachmentID string) (map[string]any, error) {
	var projectID string
	found, err := scanOptional(exec.QueryRow(ctx, `
		SELECT t.project_id::text FROM task_attachments ta JOIN tasks t ON t.id = ta.task_id WHERE ta.id = $1
	`, attachmentID), &projectID)
	if err != nil {
		return nil, fmt.Errorf("auditSnapshotAttachment: %w", err)
	}
	if !found {
		return map[string]any{}, nil
	}
	return map[string]any{"project_id": projectID}, nil
}

// auditUserName -- nama tampilan user untuk snapshot (PIC ditambah/dihapus/diserahkan):
// id mentah tidak terbaca di audit trail. Kosong bila user tidak ditemukan.
func auditUserName(ctx context.Context, exec db.Executor, userID string) (string, error) {
	var name string
	found, err := scanOptional(exec.QueryRow(ctx, `SELECT display_name FROM users WHERE id = $1`, userID), &name)
	if err != nil {
		return "", fmt.Errorf("auditUserName: %w", err)
	}
	if !found {
		return "", nil
	}
	return name, nil
}

// mergeMeta -- salin src ke dst (src menimpa).
func mergeMeta(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
