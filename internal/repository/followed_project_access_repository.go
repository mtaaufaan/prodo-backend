package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

// FollowedProjectAccessRepository -- S6E-02 (Sprint 6 "Editor Console", IG-133):
// "project yang diikuti" Editor = anggota project (project_members), ATAU
// assignee, ATAU PIC aktif di salah satu task project itu. Dipakai daftar
// `GET /me/projects` dan gerbang baca `middleware.RequireFollowedProjectAccess`.
// Bukan berbasis RLS: RLS mengizinkan SEMUA anggota workspace membaca project
// workspace-nya (prodo_is_workspace_member_of_project) -- gerbang ini lebih
// sempit dan hanya berlaku untuk role workspace `editor` dan `approver` (S7A-02).
type FollowedProjectAccessRepository struct{}

func NewFollowedProjectAccessRepository() *FollowedProjectAccessRepository {
	return &FollowedProjectAccessRepository{}
}

// followedPredicate -- $1 = user id, alias `p` = projects.
const followedPredicate = `(
	EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = p.id AND pm.user_id = $1)
	OR EXISTS (SELECT 1 FROM tasks t JOIN task_assignees ta ON ta.task_id = t.id
	           WHERE t.project_id = p.id AND t.deleted_at IS NULL AND ta.user_id = $1)
	OR EXISTS (SELECT 1 FROM tasks t JOIN task_pic_phases tpp ON tpp.task_id = t.id
	           WHERE t.project_id = p.id AND t.deleted_at IS NULL AND tpp.user_id = $1 AND tpp.is_active)
)`

// FollowedProject -- satu project yang diikuti user.
type FollowedProject struct {
	ID          string
	WorkspaceID string
	Name        string
	Code        string
}

// ListFollowedProjects -- project non-arsip di workspaceID yang diikuti userID.
func (r *FollowedProjectAccessRepository) ListFollowedProjects(ctx context.Context, exec db.Executor, workspaceID, userID string) ([]FollowedProject, error) {
	rows, err := exec.Query(ctx, `
		SELECT p.id, p.workspace_id, p.name, p.code
		FROM projects p
		WHERE p.workspace_id = $2 AND p.deleted_at IS NULL AND NOT p.is_archived
		  AND `+followedPredicate+`
		ORDER BY p.name
	`, userID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListFollowedProjects: %w", err)
	}
	defer rows.Close()

	list := make([]FollowedProject, 0)
	for rows.Next() {
		var p FollowedProject
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.Name, &p.Code); err != nil {
			return nil, fmt.Errorf("repository.ListFollowedProjects: scan: %w", err)
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListFollowedProjects: rows: %w", err)
	}
	return list, nil
}

// FollowedProjectAccess -- restricted true kalau user punya role workspace `editor`
// atau `approver` di workspace pemilik project; followed = ikut project (lihat
// followedPredicate). Project tidak ditemukan -> (false, false, nil): gerbang
// meneruskan, handler yang menjawab 404.
func (r *FollowedProjectAccessRepository) FollowedProjectAccess(ctx context.Context, exec db.Executor, projectID, userID string) (restricted, followed bool, err error) {
	err = exec.QueryRow(ctx, `
		SELECT COALESCE(wm.role::text IN ('editor', 'approver'), FALSE), `+followedPredicate+`
		FROM projects p
		LEFT JOIN workspace_members wm ON wm.workspace_id = p.workspace_id AND wm.user_id = $1
		WHERE p.id = $2
	`, userID, projectID).Scan(&restricted, &followed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("repository.FollowedProjectAccess: %w", err)
	}
	return restricted, followed, nil
}

// FollowedTaskAccess -- sama seperti FollowedProjectAccess, project diturunkan dari task.
func (r *FollowedProjectAccessRepository) FollowedTaskAccess(ctx context.Context, exec db.Executor, taskID, userID string) (restricted, followed bool, err error) {
	err = exec.QueryRow(ctx, `
		SELECT COALESCE(wm.role::text IN ('editor', 'approver'), FALSE), `+followedPredicate+`
		FROM tasks tk
		JOIN projects p ON p.id = tk.project_id
		LEFT JOIN workspace_members wm ON wm.workspace_id = p.workspace_id AND wm.user_id = $1
		WHERE tk.id = $2
	`, userID, taskID).Scan(&restricted, &followed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("repository.FollowedTaskAccess: %w", err)
	}
	return restricted, followed, nil
}
