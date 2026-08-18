package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestRequestID_GeneratesWhenAbsent(t *testing.T) {
	app := fiber.New()
	app.Use(RequestID())
	app.Get("/", func(c *fiber.Ctx) error {
		id, _ := c.Locals(RequestIDLocalsKey).(string)
		if id == "" {
			t.Error("expected request_id to be set in locals")
		}
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", http.NoBody)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // close error on a read-only test response is not actionable
	if resp.Header.Get(RequestIDHeader) == "" {
		t.Error("expected X-Request-ID response header to be set")
	}
}

func TestRequestID_PropagatesExisting(t *testing.T) {
	app := fiber.New()
	app.Use(RequestID())
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.Header.Set(RequestIDHeader, "test-fixed-id")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // close error on a read-only test response is not actionable
	if got := resp.Header.Get(RequestIDHeader); got != "test-fixed-id" {
		t.Errorf("expected propagated request_id %q, got %q", "test-fixed-id", got)
	}
}
