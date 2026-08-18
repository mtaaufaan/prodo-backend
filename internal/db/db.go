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

// NewPool membuat pgxpool.Pool dari DATABASE_URL, lalu ping untuk memastikan
// koneksi benar-benar hidup sebelum dikembalikan ke caller.
func NewPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
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
