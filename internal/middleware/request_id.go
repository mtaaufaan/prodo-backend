package middleware

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const RequestIDHeader = "X-Request-ID"
const RequestIDLocalsKey = "request_id"

// RequestID menghasilkan ID unik per request (atau pakai yang sudah ada di
// header X-Request-ID kalau caller sudah menyediakan, mis. dari edge proxy),
// disimpan di c.Locals supaya bisa diambil middleware lain (logging, dsb.)
// dan dikembalikan sebagai response header untuk korelasi client-side.
func RequestID() fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Get(RequestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		c.Locals(RequestIDLocalsKey, id)
		c.Set(RequestIDHeader, id)
		return c.Next()
	}
}
