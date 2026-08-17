package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/mtaaufaan/prodo-backend/config"
)

// main adalah entry point aplikasi PRODO backend.
// Implementasi Fiber app, middleware stack, route registration, dan health endpoint
// akan ditambahkan di S0-18. File ini hanya menjadi scaffold awal yang valid secara Go.
func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("FATAL: gagal membaca konfigurasi: %v", err)
	}

	fmt.Printf("PRODO Backend starting — env=%s port=%d\n", cfg.AppEnv, cfg.ServerPort)

	// TODO S0-18: Bootstrap Fiber app + middleware stack + /health endpoint
	// TODO S0-19: Database connection pool (pgx v5) + golang-migrate runner
	// TODO S0-20: Redis client (go-redis) + cache abstraction
	// TODO S0-21: Asynq worker + scheduler
	// TODO S0-22: Zap structured logging + request ID + OTEL trace injection

	// Graceful shutdown: tunggu SIGTERM atau SIGINT
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	fmt.Println("PRODO Backend shutting down...")
}
// H2 CI verification 2026-08-17T04:26:24Z
