package handler_test

// S2-12: boundary test 401/403 per role untuk endpoint workspace member
// (US-003, RBAC — Batasan Akses per Role). Dibangun dengan middleware
// REAL (middleware.RequireRole) + handler REAL (handler.WorkspaceHandler)
// + service REAL (service.RBACService), cuma repository/cache paling
// bawah yang di-stub -- supaya benar-benar menguji jalur otorisasi
// end-to-end di level route, bukan cuma logika RequireRole terisolasi
// (sudah dicakup internal/middleware/rbac_test.go) atau RBACService
// terisolasi (internal/service/rbac_test.go).

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/cache"
	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/handler"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// claimsLocalsKey/dbTxLocalsKey HARUS sama persis dengan konstanta
// unexported di internal/middleware/auth.go (claimsLocalsKey) dan
// db_context.go (dbTxLocalsKey) -- test ini di package handler_test,
// tidak bisa import konstanta itu langsung. Kalau nilainya berubah, test
// ini gagal keras (401 di semua kasus), bukan gagal diam-diam.
const (
	claimsLocalsKey = "prodo_claims"
	dbTxLocalsKey   = "prodo_db_tx"
)

const (
	testWorkspaceID = "ws-1"
	testMemberID    = "member-1"
)

type stubUserResolver struct{ userID string }

func (s stubUserResolver) ResolveActorUserID(context.Context, string) (string, error) {
	return s.userID, nil
}

type stubExecutor struct{}

func (stubExecutor) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (stubExecutor) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (stubExecutor) QueryRow(context.Context, string, ...any) pgx.Row        { return nil }

// stubMemberRepo -- persis satu member (testMemberID) dengan role
// tertentu di testWorkspaceID; user lain dianggap bukan member.
type stubMemberRepo struct{ role string }

func (r stubMemberRepo) GetRole(_ context.Context, _ db.Executor, _, userID string) (string, error) {
	if userID == testMemberID && r.role != "" {
		return r.role, nil
	}
	return "", pgx.ErrNoRows
}

func (stubMemberRepo) AssignRole(context.Context, db.Executor, string, string, string, *string, string, string, map[string]string, map[string]string, string, string) error {
	return nil
}

func (r stubMemberRepo) ListMembers(context.Context, db.Executor, string) ([]repository.Member, error) {
	return []repository.Member{{UserID: testMemberID, Role: r.role}}, nil
}

// GetWorkspaceOrgID -- S3-41. Tidak ada test di file ini yang menguji jalur
// Group Admin (itu ditest di internal/middleware/rbac_test.go), jadi cukup
// string kosong -- tidak akan pernah match claims.ProdoOrgIDs manapun.
func (stubMemberRepo) GetWorkspaceOrgID(context.Context, db.Executor, string) (string, error) {
	return "", nil
}

func (stubMemberRepo) RemoveMember(context.Context, db.Executor, string, string, string, string) error {
	return nil
}

type noopCache struct{}

func (noopCache) Get(context.Context, string) (string, error)              { return "", cache.ErrNotFound }
func (noopCache) Set(context.Context, string, string, time.Duration) error { return nil }
func (noopCache) Del(context.Context, string) error                        { return nil }
func (noopCache) Close() error                                             { return nil }

// newTestApp membangun app dengan route persis seperti cmd/api/main.go
// (jwtAuth+dbCtx diganti stub yang setara -- injeksi claims + db.Executor
// langsung ke Locals, tanpa JWT/Postgres sungguhan).
func newTestApp(actorUserID, platformRole string, repo stubMemberRepo) *fiber.App {
	rbacSvc := service.NewRBACService(repo, noopCache{})
	h := handler.NewWorkspaceHandler(rbacSvc, nil, zap.NewNop())
	users := stubUserResolver{userID: actorUserID}

	injectContext := func(hasClaims bool) fiber.Handler {
		return func(c *fiber.Ctx) error {
			if hasClaims {
				c.Locals(claimsLocalsKey, &middleware.Claims{PlatformRole: platformRole})
			}
			c.Locals(dbTxLocalsKey, db.Executor(stubExecutor{}))
			return c.Next()
		}
	}

	app := fiber.New()
	app.Put("/workspaces/:wsId/members/:userId/role",
		injectContext(actorUserID != ""),
		middleware.RequireRole(users, rbacSvc, "admin_workspace"),
		h.UpdateMemberRole)
	app.Get("/workspaces/:wsId/members",
		injectContext(actorUserID != ""),
		middleware.RequireRole(users, rbacSvc, "admin_workspace", "project_manager", "editor", "approver", "viewer"),
		h.ListMembers)
	return app
}

func TestRBAC_UpdateMemberRole_BoundaryPerRole(t *testing.T) {
	tests := []struct {
		name         string
		actorUserID  string
		platformRole string
		memberRole   string
		wantStatus   int
	}{
		{"platform_admin bypass, bukan member sama sekali", "someone", "platform_admin", "", fiber.StatusOK},
		{"admin_workspace diizinkan", testMemberID, "member", "admin_workspace", fiber.StatusOK},
		{"editor ditolak", testMemberID, "member", "editor", fiber.StatusForbidden},
		{"viewer ditolak", testMemberID, "member", "viewer", fiber.StatusForbidden},
		{"bukan member ditolak", "orang-lain", "member", "", fiber.StatusForbidden},
		{"tanpa claims -- unauthorized", "", "", "", fiber.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newTestApp(tt.actorUserID, tt.platformRole, stubMemberRepo{role: tt.memberRole})
			body := bytes.NewBufferString(`{"role":"editor"}`)
			req := httptest.NewRequest(http.MethodPut, "/workspaces/"+testWorkspaceID+"/members/"+testMemberID+"/role", body)
			req.Header.Set("Content-Type", "application/json")

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			defer resp.Body.Close() //nolint:errcheck // close error on a read-only test response is not actionable

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

func TestRBAC_ListMembers_BoundaryPerRole(t *testing.T) {
	tests := []struct {
		name         string
		actorUserID  string
		platformRole string
		memberRole   string
		wantStatus   int
	}{
		{"platform_admin bypass", "someone", "platform_admin", "", fiber.StatusOK},
		{"viewer -- role terendah tetap diizinkan", testMemberID, "member", "viewer", fiber.StatusOK},
		{"admin_workspace diizinkan", testMemberID, "member", "admin_workspace", fiber.StatusOK},
		{"bukan member ditolak", "orang-lain", "member", "", fiber.StatusForbidden},
		{"tanpa claims -- unauthorized", "", "", "", fiber.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newTestApp(tt.actorUserID, tt.platformRole, stubMemberRepo{role: tt.memberRole})
			req := httptest.NewRequest(http.MethodGet, "/workspaces/"+testWorkspaceID+"/members", http.NoBody)

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			defer resp.Body.Close() //nolint:errcheck // close error on a read-only test response is not actionable

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}
