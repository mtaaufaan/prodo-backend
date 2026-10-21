package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

// TaskCommentRepository -- komentar, follower, dan catatan mention task (S6E-04, Sprint 6
// "Editor Console", IG-135; US-030/US-031/US-033). Aturan bisnis (siapa boleh, jendela edit,
// cooldown mention) ada di service.TaskCommentService; repository ini hanya SQL.
type TaskCommentRepository struct{}

func NewTaskCommentRepository() *TaskCommentRepository { return &TaskCommentRepository{} }

// commentContent -- teks biasa -> dokumen TipTap minimal (DATABASE_SCHEMA.md §5.20).
func commentContent(text string) ([]byte, error) {
	doc := map[string]any{
		"type":    "doc",
		"content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text}}}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("commentContent: %w", err)
	}
	return b, nil
}

// commentTextExpr -- kebalikan commentContent di SQL.
const commentTextExpr = `COALESCE(c.content #>> '{content,0,content,0,text}', '')`

// TaskCommentContext -- identitas task + konfigurasi cooldown workspace/project.
type TaskCommentContext struct {
	TaskID        string
	TaskCode      string
	TaskTitle     string
	ProjectID     string
	WorkspaceID   string
	CooldownMin   int
	DigestEnabled bool
}

// GetTaskContext -- ErrTaskNotFound bila task tidak ada atau sudah dihapus.
func (r *TaskCommentRepository) GetTaskContext(ctx context.Context, exec db.Executor, taskID string) (*TaskCommentContext, error) {
	var c TaskCommentContext
	err := exec.QueryRow(ctx, `
		SELECT t.id, COALESCE(t.task_code, ''), t.title, t.project_id, p.workspace_id,
		       COALESCE(p.mention_cooldown_minutes, w.mention_cooldown_minutes)::int, w.mention_digest_enabled
		FROM tasks t
		JOIN projects p ON p.id = t.project_id
		JOIN workspaces w ON w.id = p.workspace_id
		WHERE t.id = $1 AND t.deleted_at IS NULL
	`, taskID).Scan(&c.TaskID, &c.TaskCode, &c.TaskTitle, &c.ProjectID, &c.WorkspaceID, &c.CooldownMin, &c.DigestEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("repository.GetTaskContext: %w", domain.ErrTaskNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("repository.GetTaskContext: %w", err)
	}
	return &c, nil
}

// ActorProjectRoles -- peran user di project: role workspace (kosong bila bukan anggota),
// role project-scoped (kosong bila bukan project_members), dan apakah PM project ini.
type ActorProjectRoles struct {
	WorkspaceRole string
	ProjectRole   string
	IsPM          bool
}

func (r *TaskCommentRepository) ActorRoles(ctx context.Context, exec db.Executor, projectID, userID string) (ActorProjectRoles, error) {
	var a ActorProjectRoles
	err := exec.QueryRow(ctx, `
		SELECT COALESCE(wm.role::text, ''), COALESCE(pm.role::text, ''),
		       EXISTS (SELECT 1 FROM project_managers pmg WHERE pmg.project_id = p.id AND pmg.user_id = $2)
		FROM projects p
		LEFT JOIN workspace_members wm ON wm.workspace_id = p.workspace_id AND wm.user_id = $2
		LEFT JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = $2
		WHERE p.id = $1
	`, projectID, userID).Scan(&a.WorkspaceRole, &a.ProjectRole, &a.IsPM)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, fmt.Errorf("repository.ActorRoles: %w", domain.ErrProjectNotFound)
	}
	if err != nil {
		return a, fmt.Errorf("repository.ActorRoles: %w", err)
	}
	return a, nil
}

// CommentRecord -- satu baris komentar (aktif, belum dihapus).
type CommentRecord struct {
	ID         string
	TaskID     string
	UserID     string
	AuthorName string
	Text       string
	IsEdited   bool
	EditedAt   *time.Time
	CreatedAt  time.Time
	// Locked -- jendela edit 3 menit sudah lewat, diukur waktu DB (server), bukan klien.
	Locked bool
}

const commentSelect = `
	SELECT c.id, c.task_id, c.user_id, COALESCE(u.display_name, ''), ` + commentTextExpr + `,
	       c.is_edited, c.edited_at, c.created_at, NOW() > c.edit_locked_at
	FROM task_comments c
	JOIN users u ON u.id = c.user_id
`

func scanComment(row interface{ Scan(dest ...any) error }) (*CommentRecord, error) {
	var c CommentRecord
	if err := row.Scan(&c.ID, &c.TaskID, &c.UserID, &c.AuthorName, &c.Text, &c.IsEdited, &c.EditedAt, &c.CreatedAt, &c.Locked); err != nil {
		return nil, err
	}
	return &c, nil
}

// Create menyimpan komentar baru; edit_locked_at = NOW() + 3 menit (default kolom).
func (r *TaskCommentRepository) Create(ctx context.Context, exec db.Executor, taskID, userID, text string) (*CommentRecord, error) {
	content, err := commentContent(text)
	if err != nil {
		return nil, err
	}
	var id string
	if err := exec.QueryRow(ctx, `
		INSERT INTO task_comments (task_id, user_id, content) VALUES ($1, $2, $3::jsonb) RETURNING id
	`, taskID, userID, content).Scan(&id); err != nil {
		return nil, fmt.Errorf("repository.CreateComment: %w", err)
	}
	return r.Get(ctx, exec, id)
}

// Get -- ErrCommentNotFound untuk komentar yang tidak ada atau soft-deleted.
func (r *TaskCommentRepository) Get(ctx context.Context, exec db.Executor, commentID string) (*CommentRecord, error) {
	c, err := scanComment(exec.QueryRow(ctx, commentSelect+` WHERE c.id = $1 AND c.deleted_at IS NULL`, commentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("repository.GetComment: %w", domain.ErrCommentNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("repository.GetComment: %w", err)
	}
	return c, nil
}

// List -- komentar aktif task, terlama dulu.
func (r *TaskCommentRepository) List(ctx context.Context, exec db.Executor, taskID string) ([]CommentRecord, error) {
	rows, err := exec.Query(ctx, commentSelect+` WHERE c.task_id = $1 AND c.deleted_at IS NULL ORDER BY c.created_at, c.id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListComments: %w", err)
	}
	defer rows.Close()
	list := make([]CommentRecord, 0)
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, fmt.Errorf("repository.ListComments: scan: %w", err)
		}
		list = append(list, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListComments: rows: %w", err)
	}
	return list, nil
}

// UpdateText -- simpan konten lama ke content_before_edit, tandai diedit.
func (r *TaskCommentRepository) UpdateText(ctx context.Context, exec db.Executor, commentID, text string) error {
	content, err := commentContent(text)
	if err != nil {
		return err
	}
	tag, err := exec.Exec(ctx, `
		UPDATE task_comments
		SET content_before_edit = content, content = $2::jsonb, is_edited = TRUE, edited_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, commentID, content)
	if err != nil {
		return fmt.Errorf("repository.UpdateCommentText: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.UpdateCommentText: %w", domain.ErrCommentNotFound)
	}
	return nil
}

// SoftDelete -- komentar tidak pernah dihapus keras (kebijakan soft delete).
func (r *TaskCommentRepository) SoftDelete(ctx context.Context, exec db.Executor, commentID string) error {
	tag, err := exec.Exec(ctx, `UPDATE task_comments SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, commentID)
	if err != nil {
		return fmt.Errorf("repository.SoftDeleteComment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SoftDeleteComment: %w", domain.ErrCommentNotFound)
	}
	return nil
}

// CountForTask -- jumlah komentar aktif (respons POST).
func (r *TaskCommentRepository) CountForTask(ctx context.Context, exec db.Executor, taskID string) (int, error) {
	var n int
	if err := exec.QueryRow(ctx, `SELECT COUNT(*) FROM task_comments WHERE task_id = $1 AND deleted_at IS NULL`, taskID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repository.CountComments: %w", err)
	}
	return n, nil
}

// ---- follower ----

// EnsureFollower -- idempotent; dipakai auto-follow (komentar, mention, penugasan) dari repository lain.
func ensureFollower(ctx context.Context, exec db.Executor, taskID, userID, source string) error {
	if _, err := exec.Exec(ctx, `
		INSERT INTO task_followers (task_id, user_id, source) VALUES ($1, $2, $3) ON CONFLICT (task_id, user_id) DO NOTHING
	`, taskID, userID, source); err != nil {
		return fmt.Errorf("ensureFollower: %w", err)
	}
	return nil
}

func (r *TaskCommentRepository) EnsureFollower(ctx context.Context, exec db.Executor, taskID, userID, source string) error {
	return ensureFollower(ctx, exec, taskID, userID, source)
}

func (r *TaskCommentRepository) Unfollow(ctx context.Context, exec db.Executor, taskID, userID string) error {
	if _, err := exec.Exec(ctx, `DELETE FROM task_followers WHERE task_id = $1 AND user_id = $2`, taskID, userID); err != nil {
		return fmt.Errorf("repository.Unfollow: %w", err)
	}
	return nil
}

// NotifyRecipients -- assignee + PIC aktif + follower task (tanpa duplikat).
func (r *TaskCommentRepository) NotifyRecipients(ctx context.Context, exec db.Executor, taskID string) ([]string, error) {
	rows, err := exec.Query(ctx, `
		SELECT user_id::text FROM task_assignees WHERE task_id = $1
		UNION SELECT user_id::text FROM task_pic_phases WHERE task_id = $1 AND is_active
		UNION SELECT user_id::text FROM task_followers WHERE task_id = $1
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("repository.NotifyRecipients: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("repository.NotifyRecipients: scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ---- mention ----

// MentionCandidate -- pengguna yang boleh di-mention di project (punya akses ke project).
type MentionCandidate struct {
	UserID      string
	DisplayName string
	Email       string
}

// MentionCandidates -- anggota project, PM project, Admin Workspace dan Division Viewer workspace
// (set yang sama dengan daftar kelola member project), ditambah assignee dan PIC aktif task project
// (S7A-08, IG-154): anggota project-scoped hanya melihat baris keanggotaannya sendiri lewat RLS
// (pm_select), tetapi membaca assignee/PIC lewat RLS task -- tanpa tambahan ini mereka hampir tidak
// bisa me-mention siapa pun.
func (r *TaskCommentRepository) MentionCandidates(ctx context.Context, exec db.Executor, projectID string) ([]MentionCandidate, error) {
	rows, err := exec.Query(ctx, `
		SELECT DISTINCT u.id::text, COALESCE(u.display_name, ''), COALESCE(u.email, '')
		FROM users u
		WHERE u.id IN (
			SELECT user_id FROM project_members WHERE project_id = $1
			UNION SELECT user_id FROM project_managers WHERE project_id = $1
			UNION SELECT ta.user_id FROM task_assignees ta JOIN tasks t ON t.id = ta.task_id
			      WHERE t.project_id = $1 AND t.deleted_at IS NULL
			UNION SELECT tpp.user_id FROM task_pic_phases tpp JOIN tasks t ON t.id = tpp.task_id
			      WHERE t.project_id = $1 AND t.deleted_at IS NULL AND tpp.is_active
			UNION SELECT wm.user_id FROM workspace_members wm JOIN projects p ON p.workspace_id = wm.workspace_id
			      WHERE p.id = $1 AND wm.role IN ('admin_workspace', 'division_viewer')
		)
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("repository.MentionCandidates: %w", err)
	}
	defer rows.Close()
	var list []MentionCandidate
	for rows.Next() {
		var c MentionCandidate
		if err := rows.Scan(&c.UserID, &c.DisplayName, &c.Email); err != nil {
			return nil, fmt.Errorf("repository.MentionCandidates: scan: %w", err)
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// ExistingMentionUsers -- user yang sudah punya catatan mention pada komentar ini (edit tidak mengirim ulang).
func (r *TaskCommentRepository) ExistingMentionUsers(ctx context.Context, exec db.Executor, commentID string) (map[string]bool, error) {
	rows, err := exec.Query(ctx, `SELECT user_id::text FROM task_comment_mentions WHERE comment_id = $1`, commentID)
	if err != nil {
		return nil, fmt.Errorf("repository.ExistingMentionUsers: %w", err)
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("repository.ExistingMentionUsers: scan: %w", err)
		}
		set[id] = true
	}
	return set, rows.Err()
}

// MentionBlock -- jendela cooldown yang masih berlaku untuk target: ada mention yang notifikasinya
// sudah terkirim kurang dari cooldown lalu DAN target belum membalas (berkomentar) di workspace itu
// sejak saat itu (PRD §2.3: blokir berakhir saat target membalas atau cooldown habis).
func (r *TaskCommentRepository) MentionBlock(ctx context.Context, exec db.Executor, userID, workspaceID string, cooldownMin int) (blocked bool, windowEnds time.Time, err error) {
	err = exec.QueryRow(ctx, `
		SELECT m.notified_at + make_interval(mins => $3)
		FROM task_comment_mentions m
		JOIN tasks t ON t.id = m.task_id
		JOIN projects p ON p.id = t.project_id
		WHERE m.user_id = $1 AND p.workspace_id = $2 AND m.digest_state = 'sent' AND m.notified_at IS NOT NULL
		  AND m.notified_at > NOW() - make_interval(mins => $3)
		  AND NOT EXISTS (
			SELECT 1 FROM task_comments c
			JOIN tasks t2 ON t2.id = c.task_id
			JOIN projects p2 ON p2.id = t2.project_id
			WHERE c.user_id = $1 AND p2.workspace_id = $2 AND c.deleted_at IS NULL AND c.created_at > m.notified_at
		  )
		ORDER BY m.notified_at DESC LIMIT 1
	`, userID, workspaceID, cooldownMin).Scan(&windowEnds)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, time.Time{}, nil
	}
	if err != nil {
		return false, time.Time{}, fmt.Errorf("repository.MentionBlock: %w", err)
	}
	return true, windowEnds, nil
}

// InsertMention mencatat mention. state 'sent' mengisi notified_at = NOW(); 'pending' menyimpan
// window_ends_at untuk job digest; 'dropped' hanya catatan.
func (r *TaskCommentRepository) InsertMention(ctx context.Context, exec db.Executor, commentID, taskID, userID, actorID, state string, windowEnds *time.Time) error {
	if _, err := exec.Exec(ctx, `
		INSERT INTO task_comment_mentions (comment_id, user_id, task_id, actor_id, digest_state, notified_at, window_ends_at)
		VALUES ($1, $2, $3, $4, $5::text, CASE WHEN $5::text = 'sent' THEN NOW() END, $6)
		ON CONFLICT (comment_id, user_id) DO NOTHING
	`, commentID, userID, taskID, actorID, state, windowEnds); err != nil {
		return fmt.Errorf("repository.InsertMention: %w", err)
	}
	return nil
}

// DeliverDueDigests (job) -- mention 'pending' yang jendelanya habis dikirim sebagai SATU
// notifikasi ringkasan per user. Mengembalikan jumlah notifikasi ringkasan yang dibuat.
func (r *TaskCommentRepository) DeliverDueDigests(ctx context.Context, exec db.Executor) (int, error) {
	rows, err := exec.Query(ctx, `
		WITH due AS (
			UPDATE task_comment_mentions SET digest_state = 'sent', notified_at = NOW()
			WHERE digest_state = 'pending' AND window_ends_at <= NOW()
			RETURNING user_id, task_id
		)
		SELECT user_id::text, COUNT(*)::int FROM due GROUP BY user_id
	`)
	if err != nil {
		return 0, fmt.Errorf("repository.DeliverDueDigests: %w", err)
	}
	type digest struct {
		userID string
		n      int
	}
	var digests []digest
	for rows.Next() {
		var d digest
		if err := rows.Scan(&d.userID, &d.n); err != nil {
			rows.Close()
			return 0, fmt.Errorf("repository.DeliverDueDigests: scan: %w", err)
		}
		digests = append(digests, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("repository.DeliverDueDigests: rows: %w", err)
	}
	for _, d := range digests {
		if err := r.InsertNotification(ctx, exec, d.userID, "", "task_mention_digest", "", "",
			fmt.Sprintf("%d mention baru", d.n), "Anda di-mention beberapa kali selama jeda cooldown -- buka Task Saya (Diikuti) untuk melihatnya."); err != nil {
			return 0, err
		}
	}
	return len(digests), nil
}

// InsertNotification -- notifikasi in-app. actorID/taskID kosong disimpan NULL.
func (r *TaskCommentRepository) InsertNotification(ctx context.Context, exec db.Executor, userID, actorID, typ, entityType, taskID, title, body string) error {
	var entityTypeArg any
	if entityType != "" {
		entityTypeArg = entityType
	}
	if _, err := exec.Exec(ctx, `
		INSERT INTO notifications (user_id, actor_id, type, entity_type, entity_id, title, body)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4, NULLIF($5, '')::uuid, $6, $7)
	`, userID, actorID, typ, entityTypeArg, taskID, title, body); err != nil {
		return fmt.Errorf("repository.InsertNotification: %w", err)
	}
	return nil
}

// AuditComment mencatat comment.created|edited|deleted lewat chokepoint audit task (snapshot
// project_id + kode + judul task, standar IG-130/IG-29). Teks komentar TIDAK dimasukkan ke audit
// (hanya panjang/jumlah mention) -- isi komentar bukan data audit; edit menyimpan teks sebelum/sesudah
// karena itu perubahan skalar yang diminta standar.
func (r *TaskCommentRepository) AuditComment(ctx context.Context, exec db.Executor, actorID, actorRole, action string, c *TaskCommentContext, before, after map[string]any) error {
	return insertTaskAudit(ctx, exec, actorID, actorRole, action, c.TaskID, c.WorkspaceID, before, after)
}
