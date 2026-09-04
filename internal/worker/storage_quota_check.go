// StorageQuotaCheckJob (S4G-08, Track S4G) -- job Asynq PERTAMA di codebase
// ini (S0-21 skeleton belum pernah diisi task sungguhan). Dijalankan
// terjadwal tiap jam (cmd/worker/main.go, asynq.Scheduler), reuse
// organizations.storage_used_mb/storage_quota_bytes yang SUDAH ADA sebagai
// angka statis (S3-32/34, lihat implementation_gaps.md IG-19) -- BUKAN
// dihitung real-time dari file sungguhan (fitur upload belum ada).
//
// "Blokir upload di 100%" dari wording task asli SENGAJA TIDAK
// diimplementasikan -- tidak ada endpoint upload/attachment sama sekali
// untuk diblokir (dikonfirmasi user, konsisten IG-19). Job ini cuma
// mengirim notifikasi.
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

const TypeStorageQuotaCheck = "storage:quota_check"

// storageQuotaOrg -- satu baris organisasi aktif dengan kuota > 0.
type storageQuotaOrg struct {
	ID        string
	Name      string
	GroupID   string
	QuotaByte int64
	UsedByte  int64
}

// StorageQuotaCheckHandler menampung dependency job (pool DB + email) --
// method Handle didaftarkan ke asynq.ServeMux via mux.HandleFunc.
type StorageQuotaCheckHandler struct {
	pool    *pgxpool.Pool
	emailer *service.EmailService
	logger  *zap.Logger
}

func NewStorageQuotaCheckHandler(pool *pgxpool.Pool, emailer *service.EmailService, logger *zap.Logger) *StorageQuotaCheckHandler {
	return &StorageQuotaCheckHandler{pool: pool, emailer: emailer, logger: logger}
}

// Handle -- untuk setiap organisasi aktif berkuota, hitung persentase
// pemakaian, kirim notif in-app+email ke Group Admin pemilik grup di ambang
// 80%/95% (deduped 1x/hari per ambang lewat cek tabel notifications --
// bukan kolom/tabel baru, cukup query WHERE created_at::date = CURRENT_DATE).
// Trusted background process TANPA actor sungguhan -- pakai
// db.SetRLSContext(..., "", "platform_admin") sama pola
// InvitationService.AcceptInvitation (bypass RLS, satu-satunya cara scan
// SEMUA organisasi lintas grup dari proses tanpa sesi JWT).
func (h *StorageQuotaCheckHandler) Handle(ctx context.Context, _ *asynq.Task) error {
	tx, err := db.SetRLSContext(ctx, h.pool, "", "platform_admin")
	if err != nil {
		return fmt.Errorf("worker.StorageQuotaCheck: setup transaksi: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // read-only per-org lalu commit eksplisit tiap baris di bawah

	rows, err := tx.Query(ctx, `
		SELECT id, name, group_id, storage_quota_bytes, storage_used_mb * 1024 * 1024
		FROM organizations
		WHERE deactivated_at IS NULL AND storage_quota_bytes > 0
	`)
	if err != nil {
		return fmt.Errorf("worker.StorageQuotaCheck: query organizations: %w", err)
	}
	var orgs []storageQuotaOrg
	for rows.Next() {
		var o storageQuotaOrg
		if err := rows.Scan(&o.ID, &o.Name, &o.GroupID, &o.QuotaByte, &o.UsedByte); err != nil {
			rows.Close()
			return fmt.Errorf("worker.StorageQuotaCheck: scan: %w", err)
		}
		orgs = append(orgs, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("worker.StorageQuotaCheck: rows: %w", err)
	}

	for _, o := range orgs {
		pct := float64(o.UsedByte) / float64(o.QuotaByte) * 100
		notifType, level := "", ""
		switch {
		case pct >= 95:
			notifType, level = "storage_warning_95", "KRITIS"
		case pct >= 80:
			notifType, level = "storage_warning_80", "PERINGATAN"
		default:
			continue
		}

		var alreadySent bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM notifications
				WHERE entity_type = 'organization' AND entity_id = $1 AND type = $2
				  AND created_at::date = CURRENT_DATE
			)
		`, o.ID, notifType).Scan(&alreadySent); err != nil {
			h.logger.Error("gagal cek dedup notifikasi kuota", zap.String("org_id", o.ID), zap.Error(err))
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

		usedGB := float64(o.UsedByte) / (1024 * 1024 * 1024)
		quotaGB := float64(o.QuotaByte) / (1024 * 1024 * 1024)
		title := fmt.Sprintf("Peringatan Kuota Storage — %s", level)
		body := fmt.Sprintf("Organisasi %s sudah memakai %.0f%% dari kuota (%.1f/%.0f GB). Tambah kuota di menu Storage & Kuota.", o.Name, pct, usedGB, quotaGB)

		for _, a := range admins {
			if _, err := tx.Exec(ctx, `
				INSERT INTO notifications (user_id, actor_id, type, entity_type, entity_id, title, body)
				VALUES ($1, NULL, $2, 'organization', $3, $4, $5)
			`, a.id, notifType, o.ID, title, body); err != nil {
				h.logger.Error("gagal insert notifikasi kuota", zap.String("org_id", o.ID), zap.String("user_id", a.id), zap.Error(err))
				continue
			}
			if err := h.emailer.SendStorageQuotaWarningEmail(ctx, a.email, a.name, o.Name, pct, usedGB, quotaGB, level); err != nil {
				h.logger.Error("gagal kirim email peringatan kuota", zap.String("org_id", o.ID), zap.String("email", a.email), zap.Error(err))
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("worker.StorageQuotaCheck: commit: %w", err)
	}
	return nil
}
