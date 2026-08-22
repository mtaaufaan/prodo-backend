package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	sentryfiber "github.com/getsentry/sentry-go/fiber"
	"github.com/gofiber/contrib/otelfiber/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/config"
	"github.com/mtaaufaan/prodo-backend/internal/cache"
	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/handler"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
	"github.com/mtaaufaan/prodo-backend/internal/telemetry"
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

	logger, err := zap.NewProduction()
	if err != nil {
		return fmt.Errorf("setup zap logger: %w", err)
	}
	defer logger.Sync() //nolint:errcheck // flush error on shutdown is not actionable

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

	// OTEL trace exporter -- opsional untuk dev lokal (kosong = tidak
	// terhubung ke otel-collector, span tetap dibuat tapi tidak dikirim ke
	// mana pun). Lihat infra/docker-compose.observability.yml (S0-15).
	if cfg.OTELEndpoint != "" {
		shutdownTracer, err := telemetry.InitTracer(ctx, "prodo-backend", cfg.OTELEndpoint)
		if err != nil {
			return fmt.Errorf("setup OTEL tracer: %w", err)
		}
		defer shutdownTracer(context.Background()) //nolint:errcheck // best-effort on shutdown
	}

	// Sentry/GlitchTip -- opsional untuk dev lokal (kosong = tidak aktif).
	// Lihat infra/docker-compose.observability.yml (S0-17, GlitchTip API-compatible).
	if cfg.SentryDSN != "" {
		if err := sentry.Init(sentry.ClientOptions{
			Dsn:         cfg.SentryDSN,
			Environment: cfg.AppEnv,
		}); err != nil {
			return fmt.Errorf("setup Sentry/GlitchTip: %w", err)
		}
		defer sentry.Flush(2 * time.Second)
	}

	app := fiber.New()
	app.Use(recover.New())
	app.Use(sentryfiber.New(sentryfiber.Options{Repanic: true}))
	app.Use(otelfiber.Middleware())
	app.Use(middleware.RequestID())
	app.Use(middleware.Logging(logger))
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

	// S1-05/06: identity & Group Admin onboarding (US-073).
	kcAdmin, err := keycloak.NewAdminClient(cfg.KeycloakIssuer, cfg.KeycloakAdminClientID, cfg.KeycloakAdminClientSecret)
	if err != nil {
		return fmt.Errorf("setup Keycloak admin client: %w", err)
	}

	oidcClient, err := keycloak.NewOIDCClient(cfg.KeycloakIssuer, cfg.KeycloakWebClientID)
	if err != nil {
		return fmt.Errorf("setup Keycloak OIDC client: %w", err)
	}

	accountRepo := repository.NewAccountRepository(pool)
	mfaRepo, err := repository.NewMFARepository(pool, cfg.MFAEncryptionKey)
	if err != nil {
		return fmt.Errorf("setup MFA repository: %w", err)
	}
	sessionRepo := repository.NewSessionRepository(pool)
	workspaceMemberRepo := repository.NewWorkspaceMemberRepository(pool)

	accountSvc := service.NewAccountService(accountRepo, kcAdmin, logger)
	emailSvc := service.NewEmailService(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPFrom, cfg.SMTPUser, cfg.SMTPPass)
	mfaSvc := service.NewMFAService(mfaRepo)
	activationSvc := service.NewActivationService(accountRepo, kcAdmin, mfaSvc, logger)
	sessionSvc := service.NewSessionService(sessionRepo, rdb)
	authSvc := service.NewAuthService(accountRepo, oidcClient, mfaSvc, sessionSvc, logger)
	rbacSvc := service.NewRBACService(workspaceMemberRepo, rdb)

	// JWTAuth butuh sessionSvc (S1-28: cek revoked/idle-timeout di setiap
	// request terautentikasi) -- makanya dipasang setelah sessionSvc, bukan
	// di awal seperti sebelum S1-27/28.
	jwtAuth, err := middleware.JWTAuth(cfg, sessionSvc)
	if err != nil {
		return fmt.Errorf("setup JWT auth middleware: %w", err)
	}

	groupAdminHandler := handler.NewGroupAdminHandler(accountSvc, emailSvc, cfg.AppBaseURL, logger)
	authHandler := handler.NewAuthHandler(activationSvc, authSvc, logger)
	sessionHandler := handler.NewSessionHandler(accountSvc, sessionSvc, logger)
	workspaceHandler := handler.NewWorkspaceHandler(rbacSvc, logger)

	v1 := app.Group("/api/v1")
	v1.Get("/platform/group-admins", jwtAuth, middleware.RequirePlatformAdmin(), groupAdminHandler.List)
	v1.Post("/platform/group-admins", jwtAuth, middleware.RequirePlatformAdmin(), groupAdminHandler.Create)
	v1.Post("/platform/group-admins/:id/resend-activation", jwtAuth, middleware.RequirePlatformAdmin(), groupAdminHandler.ResendActivation)
	v1.Post("/auth/activate", authHandler.Activate)
	v1.Post("/auth/activate/mfa-verify", authHandler.VerifyMFA)
	v1.Post("/auth/login", authHandler.Login)
	v1.Get("/auth/sessions", jwtAuth, sessionHandler.List)
	v1.Delete("/auth/sessions/:jti", jwtAuth, sessionHandler.Revoke)
	v1.Delete("/auth/sessions", jwtAuth, sessionHandler.RevokeAll)
	// ⚠️ S1-30/35 gap: dibatasi Platform-Admin-only, bukan "group_admin"/
	// "Group Admin dalam organisasinya sendiri" sesuai wording
	// sprint_backlog.md/API_CONTRACT.md -- lihat komentar
	// SessionHandler.ListForUser/RevokeAllForUser.
	v1.Get("/admin/users/:userId/sessions", jwtAuth, middleware.RequirePlatformAdmin(), sessionHandler.ListForUser)
	v1.Post("/admin/users/:userId/sessions/revoke-all", jwtAuth, middleware.RequirePlatformAdmin(), sessionHandler.RevokeAllForUser)
	// ⚠️ S2-04/09 gap: otorisasi "GA atau AW only" cuma sebagian -- lihat
	// komentar middleware.RequireRole (sama gap dengan S1-30/35, IG-01:
	// scoping Group Admin butuh data organisasi yang belum lengkap).
	v1.Put("/workspaces/:wsId/members/:userId/role", jwtAuth, middleware.RequireRole(accountSvc, rbacSvc, "admin_workspace"), workspaceHandler.UpdateMemberRole)
	// ⚠️ S2-07/08 prasyarat, dimajukan dari S3-14 (implementation_gaps.md
	// IG-09) -- lihat komentar WorkspaceHandler.ListMembers. Semua 5 role
	// workspace boleh lihat daftar member workspace mereka sendiri.
	v1.Get("/workspaces/:wsId/members", jwtAuth, middleware.RequireRole(accountSvc, rbacSvc, "admin_workspace", "project_manager", "editor", "approver", "viewer"), workspaceHandler.ListMembers)

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
