// RetentionNotifyJob (Data Retention, Track S4G) -- notifikasi H-60/H-80
// sejak organisasi dinonaktifkan (desain "GA Data Retention.dc.html":
// retensi TETAP 90 hari untuk organisasi nonaktif, TIDAK bisa diubah GA --
// beda dari organizations.retention_days yang berlaku untuk workspace/
// project soft-delete). Reuse pola PERSIS StorageQuotaCheckJob (dedup lewat
// tabel notifications, RLS bypass platform_admin) -- beda satu hal: dedup
// di sini "sekali SELAMANYA per ambang" (bukan sekali per hari), sesuai AC
// eksplisit "sekali per ambang" di sprint_backlog.md.
package worker

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

const TypeRetentionNotify = "retention:notify"

const (
	retentionTotalDays = 90
	retentionWarnDay   = 60
	retentionFinalDay  = 80
)

type deactivatedOrg struct {
	ID       string
	Name     string
	GroupID  string
	DaysGone int
}

type RetentionNotifyHandler struct {
	pool    *pgxpool.Pool
	emailer *service.EmailService
	logger  *zap.Logger
}

func NewRetentionNotifyHandler(pool *pgxpool.Pool, emailer *service.EmailService, logger *zap.Logger) *RetentionNotifyHandler {
	return &RetentionNotifyHandler{pool: pool, emailer: emailer, logger: logger}
}

// Handle -- untuk setiap organisasi nonaktif, hitung hari sejak
// dinonaktifkan, kirim notif in-app+email ke Group Admin pemilik grup pada
// H-60 dan H-80 (SEKALI per ambang, dedup lewat EXISTS tanpa batas tanggal
// -- beda dari StorageQuotaCheckJob yang dedup per-hari). Trusted
// background process TANPA actor sungguhan -- pola sama
// StorageQuotaCheckHandler.
func (h *RetentionNotifyHandler) Handle(ctx context.Context, _ *asynq.Task) error {
	tx, err := db.SetRLSContext(ctx, h.pool, "", "platform_admin")
	if err != nil {
		return fmt.Errorf("worker.RetentionNotify: setup transaksi: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // read-only per-org lalu commit eksplisit tiap baris di bawah

	rows, err := tx.Query(ctx, `
		SELECT id, name, group_id, EXTRACT(day FROM NOW() - deactivated_at)::int
		FROM organizations
		WHERE deactivated_at IS NOT NULL
	`)
	if err != nil {
		return fmt.Errorf("worker.RetentionNotify: query organizations: %w", err)
	}
	var orgs []deactivatedOrg
	for rows.Next() {
		var o deactivatedOrg
		if err := rows.Scan(&o.ID, &o.Name, &o.GroupID, &o.DaysGone); err != nil {
			rows.Close()
			return fmt.Errorf("worker.RetentionNotify: scan: %w", err)
		}
		orgs = append(orgs, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("worker.RetentionNotify: rows: %w", err)
	}

	for _, o := range orgs {
		notifType := ""
		switch {
		case o.DaysGone >= retentionFinalDay:
			notifType = "retention_warning_80"
		case o.DaysGone >= retentionWarnDay:
			notifType = "retention_warning_60"
		default:
			continue
		}

		var alreadySent bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM notifications
				WHERE entity_type = 'organization' AND entity_id = $1 AND type = $2
			)
		`, o.ID, notifType).Scan(&alreadySent); err != nil {
			h.logger.Error("gagal cek dedup notifikasi retensi", zap.String("org_id", o.ID), zap.Error(err))
			continue
		}
		if alreadySent {
			continue
		}

		gaRows, err := tx.Query(ctx, `
			SELECT u.id, u.email, u.display_name
			FROM group_admin_assignments gaa
			JOIN users u ON u.id = gaa.user_id
			WHERE gaa.group_id = $1
		`, o.GroupID)
		if err != nil {
			h.logger.Error("gagal ambil daftar Group Admin", zap.String("org_id", o.ID), zap.Error(err))
			continue
		}
		type ga struct{ id, email, name string }
		var admins []ga
		for gaRows.Next() {
			var a ga
			if err := gaRows.Scan(&a.id, &a.email, &a.name); err != nil {
				gaRows.Close()
				h.logger.Error("gagal scan Group Admin", zap.Error(err))
				continue
			}
			admins = append(admins, a)
		}
		gaRows.Close()

		daysUntilPurge := retentionTotalDays - o.DaysGone
		title := fmt.Sprintf("Peringatan Retensi Data — H-%d", o.DaysGone)
		body := fmt.Sprintf("Organisasi %s sudah %d hari dinonaktifkan. Data operasional dijadwalkan dihapus permanen dalam %d hari lagi.", o.Name, o.DaysGone, daysUntilPurge)

		for _, a := range admins {
			if _, err := tx.Exec(ctx, `
				INSERT INTO notifications (user_id, actor_id, type, entity_type, entity_id, title, body)
				VALUES ($1, NULL, $2, 'organization', $3, $4, $5)
			`, a.id, notifType, o.ID, title, body); err != nil {
				h.logger.Error("gagal insert notifikasi retensi", zap.String("org_id", o.ID), zap.String("user_id", a.id), zap.Error(err))
				continue
			}
			if err := h.emailer.SendRetentionWarningEmail(ctx, a.email, a.name, o.Name, o.DaysGone, daysUntilPurge); err != nil {
				h.logger.Error("gagal kirim email peringatan retensi", zap.String("org_id", o.ID), zap.String("email", a.email), zap.Error(err))
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("worker.RetentionNotify: commit: %w", err)
	}
	return nil
}
