// Package worker holds Asynq task handlers.
package worker

import (
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// NewMux membuat ServeMux Asynq -- task StorageQuotaCheck (S4G-08, Track
// S4G) adalah handler pertama yang benar-benar diisi (skeleton S0-21
// sebelumnya kosong).
func NewMux(pool *pgxpool.Pool, emailer *service.EmailService, csvImport *CSVImportHandler, webhookDelivery *WebhookDeliveryHandler, logger *zap.Logger) *asynq.ServeMux {
	mux := asynq.NewServeMux()
	quotaHandler := NewStorageQuotaCheckHandler(pool, emailer, logger)
	mux.HandleFunc(TypeStorageQuotaCheck, quotaHandler.Handle)
	retentionHandler := NewRetentionNotifyHandler(pool, emailer, logger)
	mux.HandleFunc(TypeRetentionNotify, retentionHandler.Handle)
	mux.HandleFunc(TypeCSVImportExecute, csvImport.Handle)
	mux.HandleFunc(TypeWebhookDelivery, webhookDelivery.Handle)
	return mux
}
