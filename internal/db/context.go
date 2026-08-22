package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Executor -- subset pgxpool.Pool/pgx.Tx yang cukup untuk query repository
// (pola sama dengan execer di account_repository.go, diperluas Query/
// QueryRow). Repository yang menyentuh tabel ber-RLS (S2-10) menerima
// Executor sebagai PARAMETER per-panggilan, bukan field struct -- executor
// sebenarnya adalah transaksi request-scoped dari SetRLSContext
// (RLS_DESIGN.md §5), bukan pool langsung, supaya session variable
// app.current_user_id/app.current_platform_role (SET LOCAL, hanya berlaku
// dalam transaksi yang sama) benar-benar terpasang saat query itu berjalan.
type Executor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// SetRLSContext membuka transaksi baru di pool dan menyuntikkan session
// variable RLS (RLS_DESIGN.md §3/§5) via set_config(..., true) = SET
// LOCAL, supaya nilainya otomatis reset saat transaksi selesai (aman
// untuk connection pool -- lihat RLS_DESIGN.md §10.2). Caller WAJIB
// Commit/Rollback tx yang dikembalikan.
//
// app.current_org_id/current_group_id/current_org_ids SENGAJA tidak
// disuntik di sini -- klaim JWT untuk itu belum ada (Keycloak protocol
// mapper belum dikonfigurasi), jadi policy RLS yang dibuat di S2-10 juga
// sengaja tidak bergantung padanya (lihat implementation_gaps.md IG-10).
func SetRLSContext(ctx context.Context, pool *pgxpool.Pool, userID, platformRole string) (pgx.Tx, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("db.SetRLSContext: begin tx: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		SELECT set_config('app.current_user_id', $1, true),
		       set_config('app.current_platform_role', $2, true)
	`, userID, platformRole); err != nil {
		tx.Rollback(ctx) //nolint:errcheck // sudah error, rollback best-effort
		return nil, fmt.Errorf("db.SetRLSContext: set session vars: %w", err)
	}
	return tx, nil
}
