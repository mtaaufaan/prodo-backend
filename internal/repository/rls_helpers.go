package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	rlsdb "github.com/mtaaufaan/prodo-backend/internal/db"
)

// withPlatformAdminRLS -- IG-23/IG-10: beberapa query platform-admin JOIN
// ke tabel ber-RLS (organizations/workspace_members untuk org_agg/mem_agg
// di groupAdminSummaryQuery, platform_audit_logs untuk S4P-20/22), tapi
// route /platform/* TIDAK melewati DBContextMiddleware (middleware itu
// baru dipasang di /workspaces/..., lihat komentar db_context.go) --
// akibatnya session variable app.current_platform_role tidak pernah
// ke-set untuk koneksi prodo_app_user (APP_DATABASE_URL, RLS_DESIGN.md
// §5.2) yang benar-benar kena RLS, dan policy SELECT diam-diam
// mengembalikan 0 baris (bukan error) alih-alih 403. Ditemukan lewat
// verifikasi live IG-23 (2026-08-28) dengan organisasi sungguhan berisi
// storage_used_mb, bukan dugaan. Pemanggil sudah digerbangi
// RequirePlatformAdmin() di layer HTTP, jadi aman meng-hardcode
// 'platform_admin' di sini tanpa perlu actorUserID (prodo_is_platform_admin()
// cuma memeriksa role, bukan user_id).
func withPlatformAdminRLS(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := rlsdb.SetRLSContext(ctx, pool, "", "platform_admin")
	if err != nil {
		return fmt.Errorf("withPlatformAdminRLS: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // read-only, rollback cukup
	return fn(tx)
}
