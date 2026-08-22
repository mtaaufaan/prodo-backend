package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

type stubUserResolver struct {
	userID string
	err    error
}

func (s *stubUserResolver) ResolveActorUserID(_ context.Context, _ string) (string, error) {
	return s.userID, s.err
}

type stubWorkspaceRoleChecker struct {
	role string
	err  error
}

func (s *stubWorkspaceRoleChecker) GetMemberRole(_ context.Context, _ db.Executor, _, _ string) (string, error) {
	return s.role, s.err
}

// stubExecutor -- db.Executor palsu, cukup untuk lolos type assertion di
// DBTxFromContext (RequireRole sendiri tidak pernah benar-benar
// menjalankan query lewatnya di test ini -- itu tugas stubWorkspaceRoleChecker).
type stubExecutor struct{}

func (stubExecutor) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (stubExecutor) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (stubExecutor) QueryRow(context.Context, string, ...any) pgx.Row        { return nil }

func withDBTx() fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Locals(dbTxLocalsKey, db.Executor(stubExecutor{}))
		return c.Next()
	}
}

// withClaims menyuntik Claims ke locals sebelum RequireRole -- meniru apa
// yang JWTAuth lakukan setelah verifikasi token sungguhan (di luar scope
// test ini, JWKS verification butuh Keycloak nyata).
func withClaims(platformRole string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Locals(claimsLocalsKey, &Claims{
			RegisteredClaims: jwt.RegisteredClaims{Subject: "kc-sub-1"},
			PlatformRole:     platformRole,
		})
		return c.Next()
	}
}

func newTestAppWithRequireRole(users UserResolver, checker WorkspaceRoleChecker, platformRole string, allowedRoles ...string) *fiber.App {
	app := fiber.New()
	app.Get("/workspaces/:wsId/x", withClaims(platformRole), withDBTx(), RequireRole(users, checker, allowedRoles...), func(c *fiber.Ctx) error {
		userID, role, ok := ActorFromContext(c)
		if !ok {
			return c.Status(fiber.StatusInternalServerError).SendString("actor not in context")
		}
		return c.JSON(fiber.Map{"user_id": userID, "role": role})
	})
	return app
}

func TestRequireRole_PlatformAdmin_Bypasses(t *testing.T) {
	app := newTestAppWithRequireRole(&stubUserResolver{userID: "user-1"}, &stubWorkspaceRoleChecker{role: "viewer"}, "platform_admin", "admin_workspace")

	resp, err := app.Test(httptest.NewRequest("GET", "/workspaces/ws-1/x", http.NoBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // close error on a read-only test response is not actionable
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("status = %d, want 200 (platform_admin harus bypass meski checker bilang viewer)", resp.StatusCode)
	}
}

func TestRequireRole_AllowedWorkspaceRole_Passes(t *testing.T) {
	app := newTestAppWithRequireRole(&stubUserResolver{userID: "user-1"}, &stubWorkspaceRoleChecker{role: "admin_workspace"}, "member", "admin_workspace")

	resp, err := app.Test(httptest.NewRequest("GET", "/workspaces/ws-1/x", http.NoBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // close error on a read-only test response is not actionable
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("status = %d, want 200 (role admin_workspace ada di allowed list)", resp.StatusCode)
	}
}

func TestRequireRole_DisallowedWorkspaceRole_Forbidden(t *testing.T) {
	app := newTestAppWithRequireRole(&stubUserResolver{userID: "user-1"}, &stubWorkspaceRoleChecker{role: "editor"}, "member", "admin_workspace")

	resp, err := app.Test(httptest.NewRequest("GET", "/workspaces/ws-1/x", http.NoBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // close error on a read-only test response is not actionable
	if resp.StatusCode != fiber.StatusForbidden {
		t.Errorf("status = %d, want 403 (editor bukan admin_workspace)", resp.StatusCode)
	}
}

func TestRequireRole_NotAMember_Forbidden(t *testing.T) {
	app := newTestAppWithRequireRole(&stubUserResolver{userID: "user-1"}, &stubWorkspaceRoleChecker{role: ""}, "member", "admin_workspace")

	resp, err := app.Test(httptest.NewRequest("GET", "/workspaces/ws-1/x", http.NoBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // close error on a read-only test response is not actionable
	if resp.StatusCode != fiber.StatusForbidden {
		t.Errorf("status = %d, want 403 (bukan member sama sekali)", resp.StatusCode)
	}
}

func TestRequireRole_MissingDBTx_InternalError(t *testing.T) {
	app := fiber.New()
	app.Get("/workspaces/:wsId/x", withClaims("member"), RequireRole(&stubUserResolver{userID: "user-1"}, &stubWorkspaceRoleChecker{role: "editor"}, "admin_workspace"), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/workspaces/ws-1/x", http.NoBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // close error on a read-only test response is not actionable
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (DBContextMiddleware belum dipasang sebelum RequireRole)", resp.StatusCode)
	}
}

func TestRequireRole_NoClaims_Unauthorized(t *testing.T) {
	app := fiber.New()
	app.Get("/workspaces/:wsId/x", RequireRole(&stubUserResolver{}, &stubWorkspaceRoleChecker{}, "admin_workspace"), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/workspaces/ws-1/x", http.NoBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // close error on a read-only test response is not actionable
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (tanpa claims sama sekali)", resp.StatusCode)
	}
}
