// Package repository -- RetentionRepository (Data Retention, Track S4G,
// desain "GA Data Retention.dc.html"). Tab "Jadwal Penghapusan" gabungan
// EMPAT sumber lintas seluruh grup: organisasi dinonaktifkan (retensi
// TETAP 90 hari, kebijakan platform, TIDAK bisa diubah GA -- lihat desain
// "DATA OPERASIONAL 90 hari"), organisasi/workspace/project soft-deleted
// (retensi EDITABLE lewat organizations.retention_days, per organisasi
// pemiliknya). Restore keempatnya reuse endpoint yang SUDAH ADA
// (organization.Reactivate, organization.Restore, workspace.Restore,
// project.Restore) -- repository ini CUMA baca.
//
// organization.Restore (kind 'org_deleted') ditambahkan 2026-09-12 --
// sebelumnya DELETE /organizations/:id hard-delete, tidak pernah muncul di
// jadwal ini sama sekali (ditemukan user via pengujian live: "hard delete
// diganti dengan soft delete, persis seperti pada penghapusan workspace").
// Kind 'org' (deactivated) dan 'org_deleted' (soft-deleted) SENGAJA
// dipisah, bukan digabung jadi satu kind 'org' -- keduanya orthogonal
// (satu organisasi bisa dinonaktifkan SEKALIGUS dihapus) dan punya retensi
// beda (90 hari tetap vs retention_days editable), FE butuh membedakan
// aksi restore (Reactivate vs Restore) per baris.
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

// orgDeactivationRetentionDays -- kebijakan platform TETAP (desain "GA Data
// Retention.dc.html": "DATA OPERASIONAL 90 hari", "tidak dapat diubah"),
// BEDA dari organizations.retention_days yang berlaku untuk workspace/
// project soft-delete.
const orgDeactivationRetentionDays = 90

type RetentionScheduleItem struct {
	Kind      string // "org" | "org_deleted" | "workspace" | "project"
	ItemID    string
	ItemName  string
	OrgName   string
	EventAt   time.Time // deactivated_at / deleted_at
	TotalDays int
	PurgeAt   time.Time
	DaysLeft  int
}

type RetentionRepository struct{}

func NewRetentionRepository() *RetentionRepository {
	return &RetentionRepository{}
}

// ListSchedule mengembalikan seluruh item terjadwal hapus dalam satu grup,
// urut sisa hari (paling mendesak dulu) -- sama urutan desain.
func (r *RetentionRepository) ListSchedule(ctx context.Context, exec db.Executor, groupID string) ([]RetentionScheduleItem, error) {
	rows, err := exec.Query(ctx, `
		SELECT kind, item_id, item_name, org_name, event_at, total_days, purge_at FROM (
			SELECT 'org' AS kind, o.id AS item_id, o.name AS item_name, o.name AS org_name,
			       o.deactivated_at AS event_at, $2::int AS total_days,
			       o.deactivated_at + ($2::text || ' days')::interval AS purge_at
			FROM organizations o
			WHERE o.group_id = $1 AND o.deactivated_at IS NOT NULL

			UNION ALL

			SELECT 'org_deleted' AS kind, o.id AS item_id, o.name AS item_name, o.name AS org_name,
			       o.deleted_at AS event_at, o.retention_days AS total_days,
			       o.purge_scheduled_at AS purge_at
			FROM organizations o
			WHERE o.group_id = $1 AND o.deleted_at IS NOT NULL

			UNION ALL

			SELECT 'workspace', w.id, w.name, o.name,
			       w.deleted_at, o.retention_days,
			       w.purge_scheduled_at
			FROM workspaces w JOIN organizations o ON o.id = w.org_id
			WHERE o.group_id = $1 AND w.deleted_at IS NOT NULL

			UNION ALL

			SELECT 'project', p.id, p.name, o.name,
			       p.deleted_at, o.retention_days,
			       p.purge_scheduled_at
			FROM projects p
			JOIN workspaces w ON w.id = p.workspace_id
			JOIN organizations o ON o.id = w.org_id
			WHERE o.group_id = $1 AND p.deleted_at IS NOT NULL
		) t
		ORDER BY purge_at ASC
	`, groupID, orgDeactivationRetentionDays)
	if err != nil {
		return nil, fmt.Errorf("repository.ListSchedule: %w", err)
	}
	defer rows.Close()

	now := time.Now()
	list := make([]RetentionScheduleItem, 0)
	for rows.Next() {
		var it RetentionScheduleItem
		if err := rows.Scan(&it.Kind, &it.ItemID, &it.ItemName, &it.OrgName, &it.EventAt, &it.TotalDays, &it.PurgeAt); err != nil {
			return nil, fmt.Errorf("repository.ListSchedule: scan: %w", err)
		}
		it.DaysLeft = int(it.PurgeAt.Sub(now).Hours() / 24)
		list = append(list, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListSchedule: %w", err)
	}
	return list, nil
}

// RetentionExport -- satu baris retention_exports (§ migrasi
// 20260917100100). Payload cuma metadata yang sungguhan ada di DB --
// implementation_gaps.md dicatat terpisah untuk folder /data (task) dan
// /attachments yang tidak bisa diisi (tabel belum ada).
type RetentionExport struct {
	ID        string
	GroupID   string
	Kind      string
	ItemName  string
	Payload   []byte
	ExpiresAt time.Time
}

// CreateExport menyimpan permintaan ekspor + token hash-nya (raw token
// dikirim lewat email, TIDAK disimpan -- pola sama user_invitations).
func (r *RetentionRepository) CreateExport(ctx context.Context, exec db.Executor, groupID, kind, itemName string, payload []byte, tokenHash, requestedBy string, expiresAt time.Time) (string, error) {
	var id string
	err := exec.QueryRow(ctx, `
		INSERT INTO retention_exports (group_id, kind, item_name, payload, token_hash, requested_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, groupID, kind, itemName, payload, tokenHash, requestedBy, expiresAt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("repository.CreateExport: %w", err)
	}
	return id, nil
}

// GetUserContact -- email+nama untuk email hasil ekspor (RetentionService.
// RequestExport). Query langsung tanpa lewat AccountService supaya tidak
// menambah dependensi silang untuk satu SELECT sederhana.
func (r *RetentionRepository) GetUserContact(ctx context.Context, exec db.Executor, userID string) (email, displayName string, err error) {
	if err := exec.QueryRow(ctx, `SELECT email, display_name FROM users WHERE id = $1`, userID).Scan(&email, &displayName); err != nil {
		return "", "", fmt.Errorf("repository.GetUserContact: %w", err)
	}
	return email, displayName, nil
}

// FindExportByTokenHash -- dipakai rute unduhan publik GET
// /retention-exports/:token (tanpa sesi JWT, lihat handler). Token
// kedaluwarsa dianggap tidak ditemukan (pesan sama seperti token salah,
// tidak membocorkan bedanya).
func (r *RetentionRepository) FindExportByTokenHash(ctx context.Context, exec db.Executor, tokenHash string) (*RetentionExport, error) {
	var e RetentionExport
	err := exec.QueryRow(ctx, `
		SELECT id, group_id, kind, item_name, payload, expires_at
		FROM retention_exports
		WHERE token_hash = $1 AND expires_at > NOW()
	`, tokenHash).Scan(&e.ID, &e.GroupID, &e.Kind, &e.ItemName, &e.Payload, &e.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.FindExportByTokenHash: %w", domain.ErrRetentionExportNotFound)
		}
		return nil, fmt.Errorf("repository.FindExportByTokenHash: %w", err)
	}
	return &e, nil
}
