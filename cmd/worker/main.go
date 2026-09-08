package main

import (
	"context"
	"fmt"
	"log"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/config"
	"github.com/mtaaufaan/prodo-backend/internal/cache"
	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
	"github.com/mtaaufaan/prodo-backend/internal/worker"
)

// main adalah entry point proses worker Asynq (S0-21) -- terpisah dari
// cmd/api supaya proses HTTP dan proses background job bisa di-scale
// independen. Monitoring via Asynqmon, lihat
// infra/docker-compose.observability.yml.
func main() {
	if err := run(); err != nil {
		log.Fatalf("FATAL: %v", err)
	}
}

// run mengembalikan error alih-alih memanggil log.Fatal langsung -- sama
// pola cmd/api/main.go, supaya defer (Close pool/logger, Shutdown
// scheduler) selalu sempat jalan sebelum proses berhenti (gocritic
// exitAfterDefer: log.Fatal di dalam fungsi yang punya defer pending
// membuat defer itu TIDAK PERNAH jalan sama sekali).
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("membaca konfigurasi: %w", err)
	}

	logger, err := zap.NewProduction()
	if err != nil {
		return fmt.Errorf("setup zap logger: %w", err)
	}
	defer logger.Sync() //nolint:errcheck // flush error on shutdown is not actionable

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.AppDatabaseURL, cfg)
	if err != nil {
		return fmt.Errorf("konek ke database: %w", err)
	}
	defer pool.Close()

	emailer := service.NewEmailService(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPFrom, cfg.SMTPUser, cfg.SMTPPass)

	redisOpt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("REDIS_URL tidak valid untuk Asynq: %w", err)
	}

	// Import Data (S4G-15/16/17/18, Track S4G): CSVImportJob menulis
	// undangan/role lewat InvitationService.CreateBulkInvitations (reuse
	// PENUH S2-23) -- butuh graf dependency yang sama seperti cmd/api,
	// PERTAMA KALI proses worker perlu Keycloak+Redis (sebelumnya cukup
	// pool+emailer saja untuk StorageQuotaCheck/RetentionNotify).
	rdb, err := cache.New(ctx, cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("konek ke Redis: %w", err)
	}
	defer rdb.Close() //nolint:errcheck // best-effort close on shutdown

	kcAdmin, err := keycloak.NewAdminClient(cfg.KeycloakIssuer, cfg.KeycloakAdminClientID, cfg.KeycloakAdminClientSecret)
	if err != nil {
		return fmt.Errorf("setup Keycloak admin client: %w", err)
	}

	accountRepo := repository.NewAccountRepository(pool)
	invitationRepo := repository.NewInvitationRepository()
	workspaceMemberRepo := repository.NewWorkspaceMemberRepository()
	csvImportRepo := repository.NewCSVImportRepository()
	rbacSvc := service.NewRBACService(workspaceMemberRepo, rdb)
	invitationSvc := service.NewInvitationService(invitationRepo, emailer, kcAdmin, accountRepo, rbacSvc, logger, cfg.AppBaseURL)
	csvImportHandlerDeps := worker.NewCSVImportHandler(pool, csvImportRepo, invitationSvc, logger)

	// StorageQuotaCheckJob (S4G-08, Track S4G) -- job periodik PERTAMA di
	// codebase ini, dijalankan tiap jam. Scheduler.Start() non-blocking
	// (jalan di goroutine cron internal asynq) -- proses tetap blok di
	// srv.Run(mux) di bawah, sama seperti sebelumnya.
	scheduler := asynq.NewScheduler(redisOpt, nil)
	if _, err := scheduler.Register("@every 1h", asynq.NewTask(worker.TypeStorageQuotaCheck, nil)); err != nil {
		return fmt.Errorf("daftar jadwal StorageQuotaCheck: %w", err)
	}
	// RetentionNotifyJob (Data Retention, Track S4G) -- harian, cukup untuk
	// notifikasi H-60/H-80 (granularitas hari, bukan jam).
	if _, err := scheduler.Register("@every 24h", asynq.NewTask(worker.TypeRetentionNotify, nil)); err != nil {
		return fmt.Errorf("daftar jadwal RetentionNotify: %w", err)
	}
	if err := scheduler.Start(); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}
	defer scheduler.Shutdown()

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: cfg.AsynqConcurrency,
	})

	log.Printf("PRODO Worker starting — env=%s concurrency=%d\n", cfg.AppEnv, cfg.AsynqConcurrency)
	if err := srv.Run(worker.NewMux(pool, emailer, csvImportHandlerDeps, logger)); err != nil {
		return fmt.Errorf("worker error: %w", err)
	}
	return nil
}
