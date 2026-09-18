// Package repository -- TaskAttachmentRepository (H20-22, S4W-19/20/21,
// EPIC 10 Attachment Management, US-064/064b/065/066). Cakupan HANYA
// attachment di deskripsi task (`comment_id` selalu NULL) -- attachment di
// komentar (bagian lain US-064) tidak bisa dibangun, `task_comments`
// (EPIC 5 Collaboration) belum ada sama sekali. US-064c (attachment di
// Version History task) juga di luar cakupan, dependency-nya (version
// history task) belum ada.
//
// Retensi: `deleted_at` + `purge_scheduled_at` -- pola PERSIS
// workspaces/projects/organizations (migrasi 20261023090000). TIDAK ada
// job Asynq yang mengeksekusi purge fisik begitu purge_scheduled_at
// lewat -- konsisten gap yang sudah diterima di semua tabel
// purge_scheduled_at lain (implementation_gaps.md IG-60), bukan gap baru.
// "Hapus permanen" (AW-only) menyetel purge_scheduled_at = deleted_at
// (jendela pulih nol) DAN service memanggil StorageService.Delete
// seketika -- beda dari hapus biasa (retensi) yang mempertahankan objek
// MinIO supaya restore masih mungkin selama purge_scheduled_at > NOW().
package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type TaskAttachment struct {
	ID               string
	TaskID           string
	CommentID        *string
	UploaderID       string
	UploaderName     string
	UploaderEmail    string
	OriginalName     string
	DisplayName      string
	StorageKey       string
	MimeType         string
	SizeBytes        int64
	IsImage          bool
	CreatedAt        time.Time
	DeletedAt        *time.Time
	PurgeScheduledAt *time.Time

	// Kolom join, hanya terisi lewat ListForWorkspace (grid "AW Documents").
	TaskCode      *string
	TaskTitle     string
	TaskDeletedAt *time.Time
	ProjectID     string
	ProjectName   string
	SprintName    *string
}

// Status turunan (bukan kolom) -- dipakai FE untuk badge AKTIF/ORPHAN/DIHAPUS.
func (a *TaskAttachment) Status() string {
	if a.DeletedAt != nil {
		return "deleted"
	}
	if a.TaskDeletedAt != nil {
		return "orphan"
	}
	return "active"
}

// AttachmentFilter -- field kosong/nol berarti tidak difilter, pola sama
// TaskFilter (task_repository.go).
type AttachmentFilter struct {
	ProjectID  string
	Status     string // "" (aktif & orphan, default) | "active" | "orphan" | "deleted" | "all"
	Ext        string
	UploaderID string
	Sort       string // "" (terbesar, default) | "smallest" | "newest" | "oldest" | "name"
}

type ProjectUsage struct {
	ProjectID   string
	ProjectName string
	Bytes       int64
	FileCount   int
}


type TaskAttachmentRepository struct{}

func NewTaskAttachmentRepository() *TaskAttachmentRepository { return &TaskAttachmentRepository{} }

func (r *TaskAttachmentRepository) Create(ctx context.Context, exec db.Executor, taskID, uploaderID, originalName, displayName, storageKey, mimeType string, sizeBytes int64, isImage bool, actorRole, workspaceID string) (*TaskAttachment, error) {
	var a TaskAttachment
	a.TaskID, a.UploaderID, a.OriginalName, a.DisplayName, a.StorageKey, a.MimeType, a.SizeBytes, a.IsImage = taskID, uploaderID, originalName, displayName, storageKey, mimeType, sizeBytes, isImage
	err := exec.QueryRow(ctx, `
		INSERT INTO task_attachments (task_id, uploader_id, original_name, display_name, storage_key, mime_type, size_bytes, is_image)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at
	`, taskID, uploaderID, originalName, displayName, storageKey, mimeType, sizeBytes, isImage).Scan(&a.ID, &a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("repository.Create: %w", err)
	}
	if err := insertAttachmentAudit(ctx, exec, uploaderID, actorRole, "attachment.uploaded", a.ID, workspaceID, map[string]any{"name": displayName, "size_bytes": sizeBytes}); err != nil {
		return nil, fmt.Errorf("repository.Create: %w", err)
	}
	return &a, nil
}

// Get mengembalikan satu attachment TANPA join (dipakai mutasi -- cuma
// butuh baris intinya untuk cek kepemilikan/status).
func (r *TaskAttachmentRepository) Get(ctx context.Context, exec db.Executor, id string) (*TaskAttachment, error) {
	var a TaskAttachment
	err := exec.QueryRow(ctx, `
		SELECT id, task_id, comment_id, uploader_id, original_name, display_name, storage_key, mime_type, size_bytes, is_image, created_at, deleted_at, purge_scheduled_at
		FROM task_attachments WHERE id = $1
	`, id).Scan(&a.ID, &a.TaskID, &a.CommentID, &a.UploaderID, &a.OriginalName, &a.DisplayName, &a.StorageKey, &a.MimeType, &a.SizeBytes, &a.IsImage, &a.CreatedAt, &a.DeletedAt, &a.PurgeScheduledAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.Get: %w", domain.ErrAttachmentNotFound)
		}
		return nil, fmt.Errorf("repository.Get: %w", err)
	}
	return &a, nil
}

// ListForTask -- tab "Lampiran" Task Detail, aktif saja.
func (r *TaskAttachmentRepository) ListForTask(ctx context.Context, exec db.Executor, taskID string) ([]TaskAttachment, error) {
	rows, err := exec.Query(ctx, `
		SELECT ta.id, ta.task_id, ta.uploader_id, u.display_name, u.email, ta.original_name, ta.display_name, ta.storage_key, ta.mime_type, ta.size_bytes, ta.is_image, ta.created_at, ta.deleted_at, ta.purge_scheduled_at
		FROM task_attachments ta
		JOIN users u ON u.id = ta.uploader_id
		WHERE ta.task_id = $1 AND ta.deleted_at IS NULL
		ORDER BY ta.created_at DESC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListForTask: %w", err)
	}
	defer rows.Close()

	list := make([]TaskAttachment, 0)
	for rows.Next() {
		var a TaskAttachment
		if err := rows.Scan(&a.ID, &a.TaskID, &a.UploaderID, &a.UploaderName, &a.UploaderEmail, &a.OriginalName, &a.DisplayName, &a.StorageKey, &a.MimeType, &a.SizeBytes, &a.IsImage, &a.CreatedAt, &a.DeletedAt, &a.PurgeScheduledAt); err != nil {
			return nil, fmt.Errorf("repository.ListForTask: scan: %w", err)
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// ListForWorkspace -- grid "AW Documents", lintas SELURUH workspace (RLS
// mengizinkan siapa pun anggota workspace membaca, otorisasi AW-only
// ditegakkan di service). ponytail: filter project/uploader/status
// didorong ke SQL (murah, terindeks via workspace_id), tapi filter
// ekstensi + urutan + paginasi dilakukan di Go setelah fetch dibatasi
// 5000 baris -- dataset attachment per workspace realistis masih kecil;
// upgrade ke SQL penuh (OFFSET/LIMIT + ekstensi via kolom generated) kalau
// suatu workspace benar-benar punya ribuan lampiran aktif.
func (r *TaskAttachmentRepository) ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string, f *AttachmentFilter) (rows []TaskAttachment, total int, err error) {
	sqlRows, err := exec.Query(ctx, `
		SELECT ta.id, ta.task_id, ta.uploader_id, u.display_name, u.email, ta.original_name, ta.display_name, ta.storage_key, ta.mime_type, ta.size_bytes, ta.is_image, ta.created_at, ta.deleted_at, ta.purge_scheduled_at,
		       t.task_code, t.title, t.deleted_at, p.id, p.name, s.name
		FROM task_attachments ta
		JOIN tasks t ON t.id = ta.task_id
		JOIN projects p ON p.id = t.project_id
		LEFT JOIN sprints s ON s.id = t.sprint_id
		JOIN users u ON u.id = ta.uploader_id
		WHERE p.workspace_id = $1
		  AND ($2 = '' OR p.id::text = $2)
		  AND ($3 = '' OR ta.uploader_id::text = $3)
		ORDER BY ta.created_at DESC
		LIMIT 5000
	`, workspaceID, f.ProjectID, f.UploaderID)
	if err != nil {
		return nil, 0, fmt.Errorf("repository.ListForWorkspace: %w", err)
	}
	all := make([]TaskAttachment, 0)
	for sqlRows.Next() {
		var a TaskAttachment
		if err := sqlRows.Scan(&a.ID, &a.TaskID, &a.UploaderID, &a.UploaderName, &a.UploaderEmail, &a.OriginalName, &a.DisplayName, &a.StorageKey, &a.MimeType, &a.SizeBytes, &a.IsImage, &a.CreatedAt, &a.DeletedAt, &a.PurgeScheduledAt,
			&a.TaskCode, &a.TaskTitle, &a.TaskDeletedAt, &a.ProjectID, &a.ProjectName, &a.SprintName); err != nil {
			sqlRows.Close()
			return nil, 0, fmt.Errorf("repository.ListForWorkspace: scan: %w", err)
		}
		all = append(all, a)
	}
	sqlRows.Close()
	if err := sqlRows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repository.ListForWorkspace: %w", err)
	}

	filtered := make([]TaskAttachment, 0, len(all))
	for i := range all {
		a := &all[i]
		switch f.Status {
		case "active":
			if a.Status() != "active" {
				continue
			}
		case "orphan":
			if a.Status() != "orphan" {
				continue
			}
		case "deleted":
			if a.Status() != "deleted" {
				continue
			}
		case "all":
			// tanpa filter
		default: // "" -- default desain "Aktif & orphan"
			if a.Status() == "deleted" {
				continue
			}
		}
		if f.Ext != "" && fileExt(a.OriginalName) != strings.ToLower(f.Ext) {
			continue
		}
		filtered = append(filtered, *a)
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		switch f.Sort {
		case "smallest":
			return filtered[i].SizeBytes < filtered[j].SizeBytes
		case "newest":
			return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
		case "oldest":
			return filtered[i].CreatedAt.Before(filtered[j].CreatedAt)
		case "name":
			return strings.ToLower(filtered[i].DisplayName) < strings.ToLower(filtered[j].DisplayName)
		default: // "" -- default desain "Terbesar"
			return filtered[i].SizeBytes > filtered[j].SizeBytes
		}
	})

	// Paginasi TIDAK dilakukan di sini -- pola konsisten seluruh FE codebase
	// ini (WorkspaceMembersPage/AwWebhookPage/AwRuleAutomationPage dkk):
	// backend mengirim SELURUH hasil filter+urut, FE yang memotong per
	// halaman ("Grid 1 pagination pattern"). Konsisten juga karena
	// apiClient FE meng-unwrap `response.data.data` secara otomatis --
	// field meta paginasi terpisah tidak pernah benar-benar sampai ke
	// caller manapun di codebase ini.
	return filtered, len(filtered), nil
}

func fileExt(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx < 0 || idx == len(name)-1 {
		return ""
	}
	return strings.ToLower(name[idx+1:])
}

// Rename -- US-064b, "nama fisik di storage tetap, hanya nama tampil".
func (r *TaskAttachmentRepository) Rename(ctx context.Context, exec db.Executor, id, displayName, actorID, actorRole, workspaceID string, before *TaskAttachment) error {
	tag, err := exec.Exec(ctx, `UPDATE task_attachments SET display_name = $2 WHERE id = $1 AND deleted_at IS NULL`, id, displayName)
	if err != nil {
		return fmt.Errorf("repository.Rename: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Rename: %w", domain.ErrAttachmentNotFound)
	}
	if err := insertAttachmentAudit(ctx, exec, actorID, actorRole, "attachment.renamed", id, workspaceID,
		map[string]any{"before": before.DisplayName, "after": displayName}); err != nil {
		return fmt.Errorf("repository.Rename: %w", err)
	}
	return nil
}

// SoftDelete -- mode "retensi" (default): objek MinIO TETAP ada, restore
// mungkin selama purgeAt > NOW().
func (r *TaskAttachmentRepository) SoftDelete(ctx context.Context, exec db.Executor, id string, purgeAt time.Time, actorID, actorRole, workspaceID string, before *TaskAttachment) error {
	tag, err := exec.Exec(ctx, `UPDATE task_attachments SET deleted_at = NOW(), purge_scheduled_at = $2 WHERE id = $1 AND deleted_at IS NULL`, id, purgeAt)
	if err != nil {
		return fmt.Errorf("repository.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SoftDelete: %w", domain.ErrAttachmentNotFound)
	}
	if err := insertAttachmentAudit(ctx, exec, actorID, actorRole, "attachment.deleted", id, workspaceID,
		map[string]any{"name": before.DisplayName, "size_bytes": before.SizeBytes, "mode": "retensi", "purge_scheduled_at": purgeAt}); err != nil {
		return fmt.Errorf("repository.SoftDelete: %w", err)
	}
	return nil
}

// PermanentDelete (AW-only) -- purge_scheduled_at = deleted_at (jendela
// pulih nol). Objek MinIO dihapus oleh SERVICE (StorageService.Delete)
// SETELAH baris ini sukses -- repository tidak tahu apa-apa soal storage.
func (r *TaskAttachmentRepository) PermanentDelete(ctx context.Context, exec db.Executor, id, actorID, actorRole, workspaceID string, before *TaskAttachment) error {
	// purge_scheduled_at = deleted_at PAKAI literal NOW() yang sama, BUKAN
	// referensi kolom deleted_at -- dalam satu UPDATE, SET kanan merujuk
	// nilai baris SEBELUM update (deleted_at lama = NULL, dijamin WHERE di
	// bawah), jadi "= deleted_at" akan selalu tersimpan NULL kalau ditulis
	// begitu (bug yang sempat lolos sampai ketahuan verifikasi live).
	tag, err := exec.Exec(ctx, `UPDATE task_attachments SET deleted_at = NOW(), purge_scheduled_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("repository.PermanentDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.PermanentDelete: %w", domain.ErrAttachmentNotFound)
	}
	if err := insertAttachmentAudit(ctx, exec, actorID, actorRole, "attachment.deleted_permanent", id, workspaceID,
		map[string]any{"name": before.DisplayName, "size_bytes": before.SizeBytes, "mode": "permanen"}); err != nil {
		return fmt.Errorf("repository.PermanentDelete: %w", err)
	}
	return nil
}

// Restore -- hanya boleh selama purge_scheduled_at > NOW() (baris
// "permanen" punya purge_scheduled_at = deleted_at, sudah lewat begitu
// dicek ulang, jadi otomatis tidak lolos WHERE ini).
func (r *TaskAttachmentRepository) Restore(ctx context.Context, exec db.Executor, id, actorID, actorRole, workspaceID string, before *TaskAttachment) error {
	tag, err := exec.Exec(ctx, `
		UPDATE task_attachments SET deleted_at = NULL, purge_scheduled_at = NULL
		WHERE id = $1 AND deleted_at IS NOT NULL AND purge_scheduled_at > NOW()
	`, id)
	if err != nil {
		return fmt.Errorf("repository.Restore: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if before.DeletedAt == nil {
			return fmt.Errorf("repository.Restore: %w", domain.ErrAttachmentNotDeleted)
		}
		return fmt.Errorf("repository.Restore: %w", domain.ErrAttachmentAlreadyPurged)
	}
	if err := insertAttachmentAudit(ctx, exec, actorID, actorRole, "attachment.restored", id, workspaceID,
		map[string]any{"name": before.DisplayName}); err != nil {
		return fmt.Errorf("repository.Restore: %w", err)
	}
	return nil
}

// OrgUsageBytes -- total lampiran AKTIF (deleted_at IS NULL) lintas
// SELURUH workspace organisasi ini. Dipakai gate kuota (US-066) dan
// OrganizationRepository.RefreshStorageUsedMB.
func (r *TaskAttachmentRepository) OrgUsageBytes(ctx context.Context, exec db.Executor, orgID string) (int64, error) {
	var totalBytes int64
	err := exec.QueryRow(ctx, `
		SELECT COALESCE(SUM(ta.size_bytes), 0)
		FROM task_attachments ta
		JOIN tasks t ON t.id = ta.task_id
		JOIN projects p ON p.id = t.project_id
		JOIN workspaces w ON w.id = p.workspace_id
		WHERE w.org_id = $1 AND ta.deleted_at IS NULL
	`, orgID).Scan(&totalBytes)
	if err != nil {
		return 0, fmt.Errorf("repository.OrgUsageBytes: %w", err)
	}
	return totalBytes, nil
}

// PerProjectUsage -- breakdown gauge kuota "AW Documents.dc.html", HANYA
// project di workspace ini (bukan seluruh organisasi -- kuota sendiri
// tetap level organisasi, breakdown ini murni supaya AW tahu project mana
// yang paling banyak makan kuota organisasinya).
func (r *TaskAttachmentRepository) PerProjectUsage(ctx context.Context, exec db.Executor, workspaceID string) ([]ProjectUsage, error) {
	rows, err := exec.Query(ctx, `
		SELECT p.id, p.name, COALESCE(SUM(ta.size_bytes), 0), COUNT(ta.id)
		FROM projects p
		LEFT JOIN tasks t ON t.project_id = p.id
		LEFT JOIN task_attachments ta ON ta.task_id = t.id AND ta.deleted_at IS NULL
		WHERE p.workspace_id = $1 AND p.deleted_at IS NULL
		GROUP BY p.id, p.name
		HAVING COALESCE(SUM(ta.size_bytes), 0) > 0
		ORDER BY SUM(ta.size_bytes) DESC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repository.PerProjectUsage: %w", err)
	}
	defer rows.Close()

	list := make([]ProjectUsage, 0)
	for rows.Next() {
		var p ProjectUsage
		if err := rows.Scan(&p.ProjectID, &p.ProjectName, &p.Bytes, &p.FileCount); err != nil {
			return nil, fmt.Errorf("repository.PerProjectUsage: scan: %w", err)
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

// ListGroupAdmins -- pola PERSIS WebhookRepository.GroupAdminContacts/
// worker/storage_quota_check.go, dipakai RequestQuota mengirim notifikasi
// "Minta Tambah Kuota" ke setiap GA pengelola grup organisasi ini. Reuse
// tipe `GroupAdminContact` yang sudah ada (webhook_repository.go) alih-alih
// mendefinisikan ulang -- satu package `repository` yang sama.
func (r *TaskAttachmentRepository) ListGroupAdmins(ctx context.Context, exec db.Executor, groupID string) ([]GroupAdminContact, error) {
	rows, err := exec.Query(ctx, `
		SELECT u.id, u.email, u.display_name
		FROM group_admin_assignments gaa
		JOIN users u ON u.id = gaa.user_id
		WHERE gaa.group_id = $1
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListGroupAdmins: %w", err)
	}
	defer rows.Close()

	list := make([]GroupAdminContact, 0)
	for rows.Next() {
		var g GroupAdminContact
		if err := rows.Scan(&g.ID, &g.Email, &g.Name); err != nil {
			return nil, fmt.Errorf("repository.ListGroupAdmins: scan: %w", err)
		}
		list = append(list, g)
	}
	return list, rows.Err()
}

// LogQuotaRequest -- audit_logs untuk "Minta Tambah Kuota", TIDAK ada
// baris state tersendiri (lihat komentar service.RequestQuota, versi
// minimal notify-only tanpa tabel request).
func (r *TaskAttachmentRepository) LogQuotaRequest(ctx context.Context, exec db.Executor, actorID, actorRole, workspaceID, orgID string, additionalGB int, reason string) error {
	return writeAuditLog(ctx, exec, "audit_logs", actorID, actorRole, "attachment.quota_requested", "organization", &orgID,
		map[string]any{"workspace_id": workspaceID}, nil, map[string]any{"additional_gb": additionalGB, "reason": reason})
}

// insertAttachmentAudit -- reuse writeAuditLog (chokepoint audit_logs),
// pola PERSIS insertRuleAudit/insertWebhookAudit.
func insertAttachmentAudit(ctx context.Context, exec execer, actorID, actorRole, action, attachmentID, workspaceID string, stateAfter map[string]any) error {
	return writeAuditLog(ctx, exec, "audit_logs", actorID, actorRole, action, "task_attachment", &attachmentID,
		map[string]any{"workspace_id": workspaceID}, nil, stateAfter)
}
