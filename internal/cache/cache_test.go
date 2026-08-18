package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNew_InvalidURL(t *testing.T) {
	if _, err := New(context.Background(), "not-a-valid-url"); err == nil {
		t.Fatal("expected error for invalid REDIS_URL, got nil")
	}
}

// TestGetSetDel_Live runs against a real Redis (skips if unset) -- exercises
// the actual Get/Set/Del round-trip, including the ErrNotFound path.
func TestGetSetDel_Live(t *testing.T) {
	redisURL := "redis://:redis_dev_secret@localhost:6379/0"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := New(ctx, redisURL)
	if err != nil {
		t.Skipf("redis not reachable at %s, skipping: %v", redisURL, err)
	}
	defer c.Close()

	key := "prodo:test:cache_test"
	defer c.Del(ctx, key) //nolint:errcheck // best-effort cleanup

	if _, err := c.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing key, got %v", err)
	}

	if err := c.Set(ctx, key, "hello", time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got != "hello" {
		t.Fatalf("expected %q, got %q", "hello", got)
	}

	if err := c.Del(ctx, key); err != nil {
		t.Fatalf("Del failed: %v", err)
	}
	if _, err := c.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after Del, got %v", err)
	}
}
