// MentionDigestHandler (S6E-04, IG-135, US-033) -- job Asynq per menit: mention yang diakumulasi
// selama cooldown dikirim sebagai SATU notifikasi ringkasan per user begitu jendelanya habis.
// Seluruh logika ada di TaskCommentRepository.DeliverDueDigests; handler hanya membungkus transaksi
// trusted-background (pola RuleDueDateCheckHandler).
package worker

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

const TypeMentionDigest = "mention:digest"

type mentionDigestDeliverer interface {
	DeliverDueDigests(ctx context.Context, exec db.Executor) (int, error)
}

type MentionDigestHandler struct {
	pool   *pgxpool.Pool
	digest mentionDigestDeliverer
	logger *zap.Logger
}

func NewMentionDigestHandler(pool *pgxpool.Pool, digest mentionDigestDeliverer, logger *zap.Logger) *MentionDigestHandler {
	return &MentionDigestHandler{pool: pool, digest: digest, logger: logger}
}

// Handle -- proses tanpa actor (bypass "platform_admin" untuk scan lintas workspace).
func (h *MentionDigestHandler) Handle(ctx context.Context, _ *asynq.Task) error {
	tx, err := db.SetRLSContext(ctx, h.pool, "", "platform_admin")
	if err != nil {
		return fmt.Errorf("worker.MentionDigest: setup transaksi: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // commit eksplisit di bawah kalau sukses

	n, err := h.digest.DeliverDueDigests(ctx, tx)
	if err != nil {
		return fmt.Errorf("worker.MentionDigest: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("worker.MentionDigest: commit: %w", err)
	}
	if n > 0 {
		h.logger.Info("mention digest terkirim", zap.Int("notifikasi", n))
	}
	return nil
}
