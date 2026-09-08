// CSVImportJob (Import Data, S4G-15/16/17/18, Track S4G) -- job Asynq
// PERTAMA di codebase ini yang di-enqueue dengan payload dari HTTP handler
// (StorageQuotaCheck/RetentionNotify sebelumnya cron-only, payload nil).
// Menulis sungguhan ke users/workspace_members/user_invitations dengan
// memanggil InvitationService.CreateBulkInvitations SATU EMAIL per baris
// (reuse penuh S2-23, termasuk SAVEPOINT internalnya) -- baris CSV bisa
// beda workspace+role per baris, beda dari CreateBulkInvitations yang
// didesain untuk satu workspace+role per panggilan.
package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

const TypeCSVImportExecute = "csv_import:execute"

type CSVImportPayload struct {
	ImportID string `json:"import_id"`
}

func NewCSVImportTask(importID string) (*asynq.Task, error) {
	payload, err := json.Marshal(CSVImportPayload{ImportID: importID})
	if err != nil {
		return nil, fmt.Errorf("worker.NewCSVImportTask: %w", err)
	}
	return asynq.NewTask(TypeCSVImportExecute, payload), nil
}

type CSVImportHandler struct {
	pool        *pgxpool.Pool
	repo        *repository.CSVImportRepository
	invitations *service.InvitationService
	logger      *zap.Logger
}

func NewCSVImportHandler(pool *pgxpool.Pool, repo *repository.CSVImportRepository, invitations *service.InvitationService, logger *zap.Logger) *CSVImportHandler {
	return &CSVImportHandler{pool: pool, repo: repo, invitations: invitations, logger: logger}
}

// Handle -- proses tanpa sesi JWT sungguhan (trusted background process),
// pola sama StorageQuotaCheckHandler/RetentionNotifyHandler: RLS bypass
// lewat konteks platform_admin. actorID/actorRole ASLI (imported_by GA yang
// memicu import) dipakai untuk audit trail AssignRole/CreateInvitation --
// bypass RLS TIDAK berarti audit trail salah catat aktor.
func (h *CSVImportHandler) Handle(ctx context.Context, task *asynq.Task) error {
	var p CSVImportPayload
	if err := json.Unmarshal(task.Payload(), &p); err != nil {
		return fmt.Errorf("worker.CSVImportExecute: decode payload: %w", err)
	}

	tx, err := db.SetRLSContext(ctx, h.pool, "", "platform_admin")
	if err != nil {
		return fmt.Errorf("worker.CSVImportExecute: setup transaksi: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // commit eksplisit di akhir

	imp, err := h.repo.Get(ctx, tx, p.ImportID)
	if err != nil {
		return fmt.Errorf("worker.CSVImportExecute: ambil import %s: %w", p.ImportID, err)
	}
	if err := h.repo.MarkRunning(ctx, tx, p.ImportID); err != nil {
		return fmt.Errorf("worker.CSVImportExecute: %w", err)
	}

	var rows []service.MemberImportRow
	if err := json.Unmarshal(imp.RowResults, &rows); err != nil {
		_ = h.repo.MarkFailed(ctx, tx, p.ImportID)
		_ = tx.Commit(ctx)
		return fmt.Errorf("worker.CSVImportExecute: decode row_results: %w", err)
	}

	_, inviterName, err := h.repo.GetUserContact(ctx, tx, imp.ImportedBy)
	if err != nil {
		h.logger.Warn("gagal ambil nama actor import, pakai fallback", zap.String("import_id", p.ImportID), zap.Error(err))
		inviterName = "Group Admin"
	}

	successCount, failedCount := 0, 0
	for i := range rows {
		row := &rows[i]
		if row.Status != "valid" && row.Status != "existing" {
			continue // sudah 'skipped' saat dry-run, tidak diproses
		}

		result, err := h.invitations.CreateBulkInvitations(ctx, tx, []string{row.Email}, row.WorkspaceID, row.Role, imp.ImportedBy, imp.ActorRole, row.WorkspaceName, inviterName)
		if err != nil || len(result.Errors) > 0 {
			row.Status = "skipped"
			if msg, ok := result.Errors[row.Email]; ok {
				row.Reason = msg
			} else if err != nil {
				row.Reason = err.Error()
			} else {
				row.Reason = "Gagal diproses."
			}
			failedCount++
			continue
		}
		successCount++
	}

	rowResults, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("worker.CSVImportExecute: encode hasil akhir: %w", err)
	}
	if err := h.repo.Complete(ctx, tx, p.ImportID, successCount, failedCount, rowResults); err != nil {
		return fmt.Errorf("worker.CSVImportExecute: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("worker.CSVImportExecute: commit: %w", err)
	}
	return nil
}
