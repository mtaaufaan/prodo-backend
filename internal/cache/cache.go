// Package cache menyediakan abstraksi key-value cache di atas Redis
// (go-redis v9). Interface Cache sengaja minim (Get/Set/Del) -- cukup untuk
// kebutuhan S0 seperti JWT revocation blacklist (lihat docs/DATABASE_SCHEMA.md
// §8.3); operasi Redis lain (pub/sub, sorted set, dll.) ditambahkan langsung
// via *redis.Client kalau nanti benar-benar dibutuhkan.
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrNotFound dikembalikan Get saat key tidak ada di cache.
var ErrNotFound = errors.New("cache: key not found")

// Cache adalah abstraksi key-value cache.
type Cache interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Del(ctx context.Context, key string) error
	Close() error
}

// redisCache adalah implementasi Cache berbasis Redis.
type redisCache struct {
	client *redis.Client
}

// New membuat client Redis dari REDIS_URL dan mem-verifikasi koneksi via PING
// sebelum dikembalikan ke caller.
func New(ctx context.Context, redisURL string) (Cache, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}

	client := redis.NewClient(opt)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &redisCache{client: client}, nil
}

func (c *redisCache) Get(ctx context.Context, key string) (string, error) {
	val, err := c.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("cache get %q: %w", key, err)
	}
	return val, nil
}

func (c *redisCache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := c.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("cache set %q: %w", key, err)
	}
	return nil
}

func (c *redisCache) Del(ctx context.Context, key string) error {
	if err := c.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("cache del %q: %w", key, err)
	}
	return nil
}

func (c *redisCache) Close() error {
	return c.client.Close()
}
