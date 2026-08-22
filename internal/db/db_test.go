package db

import (
	"context"
	"testing"

	"github.com/mtaaufaan/prodo-backend/config"
)

func TestNewPool_InvalidURL(t *testing.T) {
	cfg := &config.Config{}
	if _, err := NewPool(context.Background(), "not-a-valid-url", cfg); err == nil {
		t.Fatal("expected error for invalid connection string, got nil")
	}
}
