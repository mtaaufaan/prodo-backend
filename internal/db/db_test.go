package db

import (
	"context"
	"testing"

	"github.com/mtaaufaan/prodo-backend/config"
)

func TestNewPool_InvalidURL(t *testing.T) {
	cfg := &config.Config{DatabaseURL: "not-a-valid-url"}
	if _, err := NewPool(context.Background(), cfg); err == nil {
		t.Fatal("expected error for invalid DATABASE_URL, got nil")
	}
}
