package handler

import "github.com/gofiber/fiber/v2"

// Version is the backend release version, embedded at build time in later
// sprints (S0-10's ldflags -X). Hardcoded for S0 since there's no release
// process yet.
const Version = "0.1.0"

// Health responds with basic liveness info for GET /health.
func Health(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":  "ok",
		"version": Version,
	})
}
