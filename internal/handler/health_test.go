package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/mtaaufaan/prodo-backend/internal/handler"
)

func TestHealth(t *testing.T) {
	app := fiber.New()
	app.Get("/health", handler.Health)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/health", http.NoBody))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
	if body.Version != handler.Version {
		t.Errorf("version = %q, want %q", body.Version, handler.Version)
	}
}
