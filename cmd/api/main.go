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
	// AppDatabaseURL (prodo_app_user, S2-10) -- BUKAN DatabaseURL (prodo
	// superuser, hanya untuk migrate CLI/seed). Superuser selalu bypass
	// RLS apapun policy-nya, jadi runtime app WAJIB connect sebagai role
	// non-superuser supaya RLS_DESIGN.md benar-benar berlaku.
	pool, err := db.NewPool(ctx, cfg.AppDatabaseURL, cfg)
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
	workspaceMemberRepo := repository.NewWorkspaceMemberRepository()
	invitationRepo := repository.NewInvitationRepository()
	organizationRepo := repository.NewOrganizationRepository()
	groupRepo := repository.NewGroupRepository()
	projectMemberRepo := repository.NewProjectMemberRepository()

	accountSvc := service.NewAccountService(accountRepo, kcAdmin, logger)
	emailSvc := service.NewEmailService(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPFrom, cfg.SMTPUser, cfg.SMTPPass)
	mfaSvc := service.NewMFAService(mfaRepo)
	activationSvc := service.NewActivationService(accountRepo, kcAdmin, mfaSvc, logger)
	sessionSvc := service.NewSessionService(sessionRepo, rdb)
	authSvc := service.NewAuthService(accountRepo, oidcClient, kcAdmin, mfaSvc, sessionSvc, emailSvc, logger)
	rbacSvc := service.NewRBACService(workspaceMemberRepo, rdb)
	invitationSvc := service.NewInvitationService(invitationRepo, emailSvc, kcAdmin, accountRepo, rbacSvc, logger, cfg.AppBaseURL)
	organizationSvc := service.NewOrganizationService(organizationRepo)
	workspaceRepo := repository.NewWorkspaceRepository()
	workspaceSvc := service.NewWorkspaceService(workspaceRepo, organizationSvc, rbacSvc)
	groupSvc := service.NewGroupService(groupRepo, organizationSvc)
	projectMemberSvc := service.NewProjectMemberService(projectMemberRepo, organizationSvc, rbacSvc)

	// JWTAuth butuh sessionSvc (S1-28: cek revoked/idle-timeout di setiap
	// request terautentikasi) -- makanya dipasang setelah sessionSvc, bukan
	// di awal seperti sebelum S1-27/28.
	jwtAuth, err := middleware.JWTAuth(cfg, sessionSvc)
	if err != nil {
		return fmt.Errorf("setup JWT auth middleware: %w", err)
	}

	groupAdminHandler := handler.NewGroupAdminHandler(accountSvc, emailSvc, cfg.AppBaseURL, logger)
	platformSecurityHandler := handler.NewPlatformSecurityHandler(accountSvc, logger)
	authHandler := handler.NewAuthHandler(activationSvc, authSvc, logger)
	sessionHandler := handler.NewSessionHandler(accountSvc, sessionSvc, logger)
	workspaceHandler := handler.NewWorkspaceHandler(rbacSvc, workspaceSvc, logger)
	invitationHandler := handler.NewInvitationHandler(invitationSvc, accountSvc, pool, logger)
	organizationHandler := handler.NewOrganizationHandler(organizationSvc, logger)
	groupHandler := handler.NewGroupHandler(groupSvc, logger)
	projectMemberHandler := handler.NewProjectMemberHandler(projectMemberSvc, logger)

	v1 := app.Group("/api/v1")
	v1.Get("/platform/group-admins", jwtAuth, middleware.RequirePlatformAdmin(), groupAdminHandler.List)
	v1.Post("/platform/group-admins", jwtAuth, middleware.RequirePlatformAdmin(), groupAdminHandler.Create)
	v1.Post("/platform/group-admins/:id/resend-activation", jwtAuth, middleware.RequirePlatformAdmin(), groupAdminHandler.ResendActivation)
	// S4P-02, US-067.
	v1.Put("/platform/group-admins/:id/suspend", jwtAuth, middleware.RequirePlatformAdmin(), groupAdminHandler.Suspend)
	v1.Put("/platform/group-admins/:id/reactivate", jwtAuth, middleware.RequirePlatformAdmin(), groupAdminHandler.Reactivate)
	// S4P-18, US-070. Session timeout global (semua akun PA); IP allowlist
	// self-service (per akun PA sendiri, lihat komentar PlatformSecurityHandler).
	v1.Get("/platform/security-settings", jwtAuth, middleware.RequirePlatformAdmin(), platformSecurityHandler.Get)
	v1.Put("/platform/security-settings/session-timeout", jwtAuth, middleware.RequirePlatformAdmin(), platformSecurityHandler.UpdateSessionTimeout)
	v1.Post("/platform/security-settings/ip-allowlist", jwtAuth, middleware.RequirePlatformAdmin(), platformSecurityHandler.AddIPAllowlist)
	v1.Delete("/platform/security-settings/ip-allowlist/:id", jwtAuth, middleware.RequirePlatformAdmin(), platformSecurityHandler.DeleteIPAllowlist)
	v1.Post("/auth/activate", authHandler.Activate)
	v1.Post("/auth/activate/mfa-verify", authHandler.VerifyMFA)
	v1.Post("/auth/login", authHandler.Login)
	v1.Post("/auth/platform/mfa-setup/verify", authHandler.CompletePlatformAdminMFASetup) // S4P-14/19
	v1.Get("/auth/sessions", jwtAuth, sessionHandler.List)
	v1.Delete("/auth/sessions/:jti", jwtAuth, sessionHandler.Revoke)
	v1.Delete("/auth/sessions", jwtAuth, sessionHandler.RevokeAll)
	// S3-40 (implementation_gaps.md IG-01): gerbang kasar PA/GA di sini
	// (RequirePlatformRole cuma cek klaim, tanpa query DB); scoping halus
	// GA ke org target ada di handler (middleware.RequireGroupAdminInOrg).
	requireSessionAdmin := middleware.RequirePlatformRole(accountSvc, "platform_admin", "group_admin")
	v1.Get("/admin/users/:userId/sessions", jwtAuth, requireSessionAdmin, sessionHandler.ListForUser)
	v1.Post("/admin/users/:userId/sessions/revoke-all", jwtAuth, requireSessionAdmin, sessionHandler.RevokeAllForUser)
	// ⚠️ S2-04/09 gap: otorisasi "GA atau AW only" cuma sebagian -- lihat
	// komentar middleware.RequireRole (sama gap dengan S1-30/35, IG-01:
	// scoping Group Admin butuh data organisasi yang belum lengkap).
	// dbCtx (S2-10/11): membuka transaksi request-scoped + suntik session
	// variable RLS SEBELUM RequireRole (yang query workspace_members, kini
	// ber-RLS) berjalan. Cuma dipasang di route /workspaces/... untuk
	// sekarang -- lihat komentar middleware.DBContextMiddleware.
	dbCtx := middleware.DBContextMiddleware(pool, accountSvc)
	v1.Put("/workspaces/:wsId/members/:userId/role", jwtAuth, dbCtx, middleware.RequireRole(accountSvc, rbacSvc, "admin_workspace"), workspaceHandler.UpdateMemberRole)
	// S3-15, US-009. Param :userId (bukan :account_id seperti wording asli
	// task, konsisten koreksi S2-01/04). Otorisasi sama seperti UpdateMemberRole.
	v1.Delete("/workspaces/:wsId/members/:userId", jwtAuth, dbCtx, middleware.RequireRole(accountSvc, rbacSvc, "admin_workspace"), workspaceHandler.RemoveMember)
	// S3-10/11, US-008. RequireRole admin_workspace -- AW mengelola
	// workspace-nya sendiri, PA/GA-of-org bypass (konsisten RLS
	// workspaces_update, S3-42).
	v1.Put("/workspaces/:wsId", jwtAuth, dbCtx, middleware.RequireRole(accountSvc, rbacSvc, "admin_workspace"), workspaceHandler.Update)
	v1.Put("/workspaces/:wsId/deactivate", jwtAuth, dbCtx, middleware.RequireRole(accountSvc, rbacSvc, "admin_workspace"), workspaceHandler.Deactivate)
	v1.Put("/workspaces/:wsId/reactivate", jwtAuth, dbCtx, middleware.RequireRole(accountSvc, rbacSvc, "admin_workspace"), workspaceHandler.Reactivate)
	// ⚠️ S2-07/08 prasyarat, dimajukan dari S3-14 (implementation_gaps.md
	// IG-09) -- lihat komentar WorkspaceHandler.ListMembers. Semua 5 role
	// workspace boleh lihat daftar member workspace mereka sendiri.
	v1.Get("/workspaces/:wsId/members", jwtAuth, dbCtx, middleware.RequireRole(accountSvc, rbacSvc, "admin_workspace", "project_manager", "editor", "approver", "viewer"), workspaceHandler.ListMembers)
	// S2-19/21/22, US-006. AcceptInvitation (S2-20) SENGAJA tanpa jwtAuth/
	// dbCtx -- lihat komentar handler.InvitationHandler.AcceptInvitation.
	v1.Post("/workspaces/:wsId/invitations", jwtAuth, dbCtx, middleware.RequireRole(accountSvc, rbacSvc, "admin_workspace"), invitationHandler.CreateInvitations)
	// GET .../invitations: prasyarat minimal S2-28 (daftar undangan
	// pending di FE), belum pernah dijadwalkan sebagai task backend
	// terpisah -- lihat implementation_gaps.md IG-09.
	v1.Get("/workspaces/:wsId/invitations", jwtAuth, dbCtx, middleware.RequireRole(accountSvc, rbacSvc, "admin_workspace"), invitationHandler.ListPendingInvitations)
	v1.Post("/auth/invitations/accept", invitationHandler.AcceptInvitation)
	v1.Delete("/workspaces/:wsId/invitations/:invId", jwtAuth, dbCtx, middleware.RequireRole(accountSvc, rbacSvc, "admin_workspace"), invitationHandler.CancelInvitation)
	v1.Post("/workspaces/:wsId/invitations/:invId/resend", jwtAuth, dbCtx, middleware.RequireRole(accountSvc, rbacSvc, "admin_workspace"), invitationHandler.ResendInvitation)

	// S3-02/03/04, US-007. RequirePlatformRole cuma gerbang kasar (PA atau
	// GA lolos); scoping GA ke grup target ada di OrganizationService --
	// lihat komentar OrganizationHandler. organizations BELUM di-RLS
	// (S3-42 menyusul), jadi TIDAK pakai dbCtx/DBContextMiddleware seperti
	// route /workspaces/....
	// organizations kena RLS sejak S3-42 -- dbCtx WAJIB sebelum requireOrgAdmin
	// (RequirePlatformRole) supaya session variable app.current_user_id/
	// app.current_platform_role sudah tersuntik saat OrganizationRepository
	// query lewat exec. Beda dari S3-02..06 (sebelum S3-42) yang belum pakai
	// dbCtx sama sekali karena organizations belum ber-RLS saat itu.
	requireOrgAdmin := middleware.RequirePlatformRole(accountSvc, "platform_admin", "group_admin")
	v1.Get("/organizations", jwtAuth, dbCtx, requireOrgAdmin, organizationHandler.List)
	v1.Post("/organizations", jwtAuth, dbCtx, requireOrgAdmin, organizationHandler.Create)
	v1.Put("/organizations/:id", jwtAuth, dbCtx, requireOrgAdmin, organizationHandler.Update)
	v1.Put("/organizations/:id/deactivate", jwtAuth, dbCtx, requireOrgAdmin, organizationHandler.Deactivate)
	v1.Put("/organizations/:id/reactivate", jwtAuth, dbCtx, requireOrgAdmin, organizationHandler.Reactivate)
	v1.Delete("/organizations/:id", jwtAuth, dbCtx, requireOrgAdmin, organizationHandler.Delete)
	v1.Get("/organizations/:id/summary", jwtAuth, dbCtx, requireOrgAdmin, organizationHandler.Summary)
	// S3-30/34, US-010/US-011.
	v1.Put("/organizations/:id/settings", jwtAuth, dbCtx, requireOrgAdmin, organizationHandler.UpdateSettings)
	v1.Put("/organizations/:id/storage-quota", jwtAuth, dbCtx, requireOrgAdmin, organizationHandler.UpdateStorageQuota)
	// S3-09, US-008. Otorisasi sama seperti organizations (reuse
	// OrganizationService.AuthorizeOrgAccess via WorkspaceService).
	v1.Post("/organizations/:orgId/workspaces", jwtAuth, dbCtx, requireOrgAdmin, workspaceHandler.CreateWorkspace)
	// S3-13 prasyarat (implementation_gaps.md IG-17) -- list dan delete
	// workspace level-org, PA/GA saja (bukan admin_workspace, konsisten
	// RLS workspaces_delete yang tidak punya cabang workspace_member).
	v1.Get("/organizations/:orgId/workspaces", jwtAuth, dbCtx, requireOrgAdmin, workspaceHandler.List)
	v1.Delete("/workspaces/:wsId", jwtAuth, dbCtx, requireOrgAdmin, workspaceHandler.Delete)
	// S3-20, US-009b. TANPA requireOrgAdmin/RequireRole -- target scope-nya
	// groupID (bukan :wsId/:orgId), dan aktor sah (Project Manager)
	// platform_role-nya "member" biasa. Otorisasi penuh di GroupService.
	v1.Get("/groups/:groupId/accounts/search", jwtAuth, dbCtx, groupHandler.SearchAccounts)
	// S3-21/22/23/24, US-009b (implementation_gaps.md IG-17, forward-pull
	// projects/project_members). TANPA middleware role -- target scope-nya
	// projectID, aktor sah (AW/PM) platform_role-nya "member" biasa.
	// Otorisasi penuh di ProjectMemberService.
	v1.Get("/projects/:id/members", jwtAuth, dbCtx, projectMemberHandler.ListMembers)
	v1.Post("/projects/:id/members", jwtAuth, dbCtx, projectMemberHandler.AddMember)
	v1.Put("/projects/:id/members/:userId/role", jwtAuth, dbCtx, projectMemberHandler.UpdateMemberRole)
	v1.Delete("/projects/:id/members/:userId", jwtAuth, dbCtx, projectMemberHandler.RemoveMember)
	// S3-25/27, US-009c. GA/PA saja (bukan PM seperti S3-20) -- GA sudah
	// punya visibility penuh lintas org lewat RLS pm_select.
	v1.Get("/groups/:groupId/cross-org-memberships", jwtAuth, dbCtx, requireOrgAdmin, projectMemberHandler.ListCrossOrgMemberships)

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
