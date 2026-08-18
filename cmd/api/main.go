package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/mtaaufaan/prodo-backend/config"
	"github.com/mtaaufaan/prodo-backend/internal/cache"
	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/handler"
)

// main adalah entry point aplikasi PRODO backend.
func main() {
	if err := run(); err != nil {
		log.Fatalf("FATAL: %v", err)
	}
}

// run menjalankan aplikasi dan mengembalikan error alih-alih memanggil
// log.Fatal/os.Exit langsung -- supaya defer (Close koneksi DB/Redis) selalu
// sempat jalan sebelum proses berhenti.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("membaca konfigurasi: %w", err)
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("konek ke database: %w", err)
	}
	defer pool.Close()

	rdb, err := cache.New(ctx, cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("konek ke Redis: %w", err)
	}
	defer rdb.Close() //nolint:errcheck // best-effort close on shutdown

	app := fiber.New()
	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: cfg.CORSAllowOrigins,
	}))
	// Rate-limit placeholder: generous default so it doesn't get in the way
	// during S0/S1 development. Real per-endpoint limits (login lockout,
	// upload throttling, etc.) come from docs/API_CONTRACT.md in a later
	// sprint -- this just proves the middleware is wired up.
	app.Use(limiter.New(limiter.Config{
		Max:        1000,
		Expiration: 1 * time.Minute,
	}))

	app.Get("/health", handler.Health)

	// TODO S0-21: Asynq worker + scheduler
	// TODO S0-22: Zap structured logging + request ID + OTEL trace injection

	serverErr := make(chan error, 1)
	go func() {
		addr := fmt.Sprintf(":%d", cfg.ServerPort)
		fmt.Printf("PRODO Backend starting — env=%s addr=%s\n", cfg.AppEnv, addr)
		serverErr <- app.Listen(addr)
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	case <-quit:
	}

	fmt.Println("PRODO Backend shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		log.Printf("WARN: shutdown error: %v", err)
	}
	return nil
}
