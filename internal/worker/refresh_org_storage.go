// RefreshOrgStorageJob (H20-22, EPIC 10 Attachment Management) -- menutup
// implementation_gaps.md IG-19 ("storage_used_mb selalu statis, tidak
// pernah dihitung dari file sungguhan"). WAJIB proses TRUSTED background
// (bypass RLS "platform_admin") -- RLS `organizations` policy `orgs_update`
// cuma mengizinkan `platform_admin`/`group_admin`, sedangkan proses yang
// memicu refresh ini (upload/hapus permanen attachment) selalu dilakukan
// Admin Workspace lewat transaksi request-nya sendiri, yang TIDAK PERNAH
// lolos UPDATE itu (row organizations kelihatan lewat SELECT karena
// `orgs_select` lebih longgar, tapi UPDATE diam-diam 0 baris tanpa error).
// Gate kuota keras (cek SEBELUM upload) TIDAK bergantung pada kolom ini --
// TaskAttachmentService.Upload menghitung live via OrgUsageBytes (SELECT
// biasa, lolos RLS untuk siapa pun anggota workspace) supaya gate-nya
// selalu akurat walau job ini belum sempat jalan; kolom `storage_used_mb`
// di sini murni untuk dashboard GA + StorageQuotaCheckHandler yang
// TOLERAN sedikit basi (async, biasanya beres dalam hitungan detik).
package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

const TypeRefreshOrgStorage = "attachment:refresh_org_storage"

type RefreshOrgStoragePayload struct {
	OrgID string `json:"org_id"`
}

func NewRefreshOrgStorageTask(orgID string) (*asynq.Task, error) {
	payload, err := json.Marshal(RefreshOrgStoragePayload{OrgID: orgID})
	if err != nil {
		return nil, fmt.Errorf("worker.NewRefreshOrgStorageTask: %w", err)
	}
	return asynq.NewTask(TypeRefreshOrgStorage, payload), nil
}

type RefreshOrgStorageHandler struct {
	pool        *pgxpool.Pool
	attachments *repository.TaskAttachmentRepository
	orgs        *repository.OrganizationRepository
}

func NewRefreshOrgStorageHandler(pool *pgxpool.Pool, attachments *repository.TaskAttachmentRepository, orgs *repository.OrganizationRepository) *RefreshOrgStorageHandler {
	return &RefreshOrgStorageHandler{pool: pool, attachments: attachments, orgs: orgs}
}

func (h *RefreshOrgStorageHandler) Handle(ctx context.Context, t *asynq.Task) error {
	var p RefreshOrgStoragePayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("worker.RefreshOrgStorage: unmarshal payload: %w", err)
	}

	tx, err := db.SetRLSContext(ctx, h.pool, "", "platform_admin")
	if err != nil {
		return fmt.Errorf("worker.RefreshOrgStorage: setup transaksi: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // commit eksplisit di bawah kalau sukses

	used, err := h.attachments.OrgUsageBytes(ctx, tx, p.OrgID)
	if err != nil {
		return fmt.Errorf("worker.RefreshOrgStorage: %w", err)
	}
	if err := h.orgs.RefreshStorageUsedMB(ctx, tx, p.OrgID, used); err != nil {
		return fmt.Errorf("worker.RefreshOrgStorage: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("worker.RefreshOrgStorage: commit: %w", err)
	}
	return nil
}
