package main

import (
	"log"

	"github.com/hibiken/asynq"

	"github.com/mtaaufaan/prodo-backend/config"
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

	redisOpt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		log.Fatalf("FATAL: REDIS_URL tidak valid untuk Asynq: %v", err)
	}

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: cfg.AsynqConcurrency,
	})

	log.Printf("PRODO Worker starting — env=%s concurrency=%d\n", cfg.AppEnv, cfg.AsynqConcurrency)
	if err := srv.Run(worker.NewMux()); err != nil {
		log.Fatalf("FATAL: worker error: %v", err)
	}
}
