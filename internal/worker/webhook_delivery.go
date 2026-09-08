// WebhookDeliveryJob (Webhook, Track S4G, desain "GA Webhook.dc.html") --
// reuse mekanisme retry NATIF Asynq (asynq.MaxRetry + RetryDelayFunc di
// cmd/worker/main.go) alih-alih kolom next_retry_at manual: MaxRetry(3)
// berarti 1 percobaan awal + 3 retry = 4 percobaan total, delay dihitung
// RetryDelayFunc per retryCount (0->1mnt, 1->5mnt, 2->15mnt) -- persis
// "RETRY OTOMATIS 3x EXPONENTIAL BACKOFF (1,5,15 MENIT)" di desain.
// Percobaan terakhir (retryCount == MaxRetry) yang tetap gagal memicu
// WebhookService.NotifyExhausted lalu Handle mengembalikan nil (BUKAN error)
// supaya Asynq tidak mencoba lagi -- exhaustion sudah ditangani di sini.
package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

const TypeWebhookDelivery = "webhook:deliver"

// WebhookMaxRetry -- lihat komentar package. Dipakai cmd/worker/main.go
// (asynq.MaxRetry saat enqueue DAN RetryDelayFunc) supaya satu-satunya
// sumber kebenaran angka retry ada di sini.
const WebhookMaxRetry = 3

type WebhookDeliveryPayload struct {
	WebhookID string          `json:"webhook_id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

func NewWebhookDeliveryTask(webhookID, eventType string, payload []byte) (*asynq.Task, error) {
	p, err := json.Marshal(WebhookDeliveryPayload{WebhookID: webhookID, EventType: eventType, Payload: payload})
	if err != nil {
		return nil, fmt.Errorf("worker.NewWebhookDeliveryTask: %w", err)
	}
	return asynq.NewTask(TypeWebhookDelivery, p, asynq.MaxRetry(WebhookMaxRetry)), nil
}

type WebhookDeliveryHandler struct {
	pool     *pgxpool.Pool
	webhooks *service.WebhookService
	logger   *zap.Logger
}

func NewWebhookDeliveryHandler(pool *pgxpool.Pool, webhooks *service.WebhookService, logger *zap.Logger) *WebhookDeliveryHandler {
	return &WebhookDeliveryHandler{pool: pool, webhooks: webhooks, logger: logger}
}

// Handle -- trusted background process, pola sama CSVImportHandler/
// RetentionNotifyHandler: RLS bypass lewat konteks platform_admin.
func (h *WebhookDeliveryHandler) Handle(ctx context.Context, task *asynq.Task) error {
	var p WebhookDeliveryPayload
	if err := json.Unmarshal(task.Payload(), &p); err != nil {
		return fmt.Errorf("worker.WebhookDelivery: decode payload: %w", err)
	}

	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	attemptNumber := retryCount + 1

	tx, err := db.SetRLSContext(ctx, h.pool, "", "platform_admin")
	if err != nil {
		return fmt.Errorf("worker.WebhookDelivery: setup transaksi: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // commit eksplisit di akhir

	delivered, sendErr := h.webhooks.DeliverAttempt(ctx, tx, p.WebhookID, p.EventType, p.Payload, attemptNumber)
	if sendErr != nil {
		// Kegagalan infrastruktur (decrypt/webhook sudah dihapus dkk, bukan
		// kegagalan HTTP) -- baris delivery belum tentu tercatat, tidak ada
		// yang perlu di-commit. Biarkan Asynq retry sesuai jadwal normal.
		return fmt.Errorf("worker.WebhookDelivery: %w", sendErr)
	}

	if delivered || retryCount < maxRetry {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("worker.WebhookDelivery: commit: %w", err)
		}
		if delivered {
			return nil
		}
		// Belum exhausted -- kembalikan error supaya Asynq menjadwalkan
		// retry berikutnya (RetryDelayFunc di cmd/worker/main.go).
		return fmt.Errorf("worker.WebhookDelivery: percobaan %d gagal, dijadwalkan ulang", attemptNumber)
	}

	// retryCount == maxRetry DAN tetap gagal -- percobaan terakhir, exhausted.
	if err := h.webhooks.NotifyExhausted(ctx, tx, p.WebhookID, p.EventType); err != nil {
		h.logger.Error("gagal kirim notifikasi kegagalan webhook", zap.String("webhook_id", p.WebhookID), zap.Error(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("worker.WebhookDelivery: commit setelah exhausted: %w", err)
	}
	return nil
}
