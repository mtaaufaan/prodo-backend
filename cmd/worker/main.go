package main

import (
	"context"
	"log"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/config"
	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/service"
	"github.com/mtaaufaan/prodo-backend/internal/worker"
)

// main adalah entry point proses worker Asynq (S0-21) -- terpisah dari
// cmd/api supaya proses HTTP dan proses background job bisa di-scale
// independen. Monitoring via Asynqmon, lihat
// infra/docker-compose.observability.yml.
func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("FATAL: gagal membaca konfigurasi: %v", err)
	}

	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("FATAL: setup zap logger: %v", err)
	}
	defer logger.Sync() //nolint:errcheck // flush error on shutdown is not actionable

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.AppDatabaseURL, cfg)
	if err != nil {
		log.Fatalf("FATAL: konek ke database: %v", err)
	}
	defer pool.Close()

	emailer := service.NewEmailService(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPFrom, cfg.SMTPUser, cfg.SMTPPass)

	redisOpt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		log.Fatalf("FATAL: REDIS_URL tidak valid untuk Asynq: %v", err)
	}

	// StorageQuotaCheckJob (S4G-08, Track S4G) -- job periodik PERTAMA di
	// codebase ini, dijalankan tiap jam. Scheduler.Start() non-blocking
	// (jalan di goroutine cron internal asynq) -- proses tetap blok di
	// srv.Run(mux) di bawah, sama seperti sebelumnya.
	scheduler := asynq.NewScheduler(redisOpt, nil)
	if _, err := scheduler.Register("@every 1h", asynq.NewTask(worker.TypeStorageQuotaCheck, nil)); err != nil {
		log.Fatalf("FATAL: gagal daftar jadwal StorageQuotaCheck: %v", err)
	}
	if err := scheduler.Start(); err != nil {
		log.Fatalf("FATAL: gagal start scheduler: %v", err)
	}
	defer scheduler.Shutdown()

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: cfg.AsynqConcurrency,
	})

	log.Printf("PRODO Worker starting — env=%s concurrency=%d\n", cfg.AppEnv, cfg.AsynqConcurrency)
	if err := srv.Run(worker.NewMux(pool, emailer, logger)); err != nil {
		log.Fatalf("FATAL: worker error: %v", err)
	}
}
