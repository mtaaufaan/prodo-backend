// Package db menyediakan connection pool PostgreSQL (pgx v5) untuk seluruh
// repository layer. Konfigurasi pool (max/min conns, lifetime) dibaca dari
// config.Config -- lihat DB_MAX_CONNS/DB_MIN_CONNS/dst di .env.example.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mtaaufaan/prodo-backend/config"
)

// NewPool membuat pgxpool.Pool dari connString, lalu ping untuk memastikan
// koneksi benar-benar hidup sebelum dikembalikan ke caller. connString
// terpisah dari cfg (bukan selalu cfg.DatabaseURL) karena runtime app pool
// (S2-10) connect sebagai prodo_app_user -- role non-superuser yang benar-
// benar kena RLS -- BUKAN sebagai prodo (superuser dari DATABASE_URL) yang
// dipakai migrate CLI/seed dan selalu bypass RLS.
func NewPool(ctx context.Context, connString string, cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("parse database connection string: %w", err)
	}

	poolCfg.MaxConns = cfg.DBMaxConns
	poolCfg.MinConns = cfg.DBMinConns
	poolCfg.MaxConnLifetime = cfg.DBMaxConnLife
	poolCfg.MaxConnIdleTime = cfg.DBMaxConnIdle

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}
