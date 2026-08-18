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
	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/handler"
)

// main adalah entry point aplikasi PRODO backend.
func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("FATAL: gagal membaca konfigurasi: %v", err)
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg)
	if err != nil {
		log.Fatalf("FATAL: gagal konek ke database: %v", err)
	}
	defer pool.Close()

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

	// TODO S0-20: Redis client (go-redis) + cache abstraction
	// TODO S0-21: Asynq worker + scheduler
	// TODO S0-22: Zap structured logging + request ID + OTEL trace injection

	go func() {
		addr := fmt.Sprintf(":%d", cfg.ServerPort)
		fmt.Printf("PRODO Backend starting — env=%s addr=%s\n", cfg.AppEnv, addr)
		if err := app.Listen(addr); err != nil {
			log.Fatalf("FATAL: server error: %v", err)
		}
	}()

	// Graceful shutdown: tunggu SIGTERM atau SIGINT
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	fmt.Println("PRODO Backend shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := app.ShutdownWithContext(ctx); err != nil {
		log.Printf("WARN: shutdown error: %v", err)
	}
}
