// RuleDueDateCheckJob (S4W-11, Track S4W) -- job Asynq harian untuk trigger
// rule "due_date_approaching" (US-049). Handler tipis, seluruh logika
// (scan rule aktif, cari task jatuh tempo, dedup, jalankan action) ada di
// RuleService.RunDueDateCheck -- reuse PENUH, tidak diduplikasi di sini,
// sama pola StorageQuotaCheckHandler/RetentionNotifyHandler yang cuma
// membungkus transaksi trusted-background.
package worker

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

const TypeRuleDueDateCheck = "rule:due_date_check"

// ruleDueDateRunner -- reuse RuleService.RunDueDateCheck.
type ruleDueDateRunner interface {
	RunDueDateCheck(ctx context.Context, exec db.Executor) error
}

type RuleDueDateCheckHandler struct {
	pool  *pgxpool.Pool
	rules ruleDueDateRunner
}

func NewRuleDueDateCheckHandler(pool *pgxpool.Pool, rules *service.RuleService) *RuleDueDateCheckHandler {
	return &RuleDueDateCheckHandler{pool: pool, rules: rules}
}

// Handle -- trusted background process TANPA actor sungguhan, pola sama
// StorageQuotaCheckHandler (db.SetRLSContext bypass "platform_admin",
// satu-satunya cara scan lintas workspace dari proses tanpa sesi JWT).
func (h *RuleDueDateCheckHandler) Handle(ctx context.Context, _ *asynq.Task) error {
	tx, err := db.SetRLSContext(ctx, h.pool, "", "platform_admin")
	if err != nil {
		return fmt.Errorf("worker.RuleDueDateCheck: setup transaksi: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // commit eksplisit di bawah kalau sukses

	if err := h.rules.RunDueDateCheck(ctx, tx); err != nil {
		return fmt.Errorf("worker.RuleDueDateCheck: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("worker.RuleDueDateCheck: commit: %w", err)
	}
	return nil
}
