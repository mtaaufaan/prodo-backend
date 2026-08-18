package middleware

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Logging mencatat setiap request sebagai satu log JSON terstruktur (zap):
// request_id (dari middleware RequestID), trace_id (dari span OTEL aktif --
// kosong kalau tidak ada span, mis. request yang tidak match route manapun),
// duration_ms, status, method, path. Konvensi zap mengikuti
// docs/coding-conventions.md §3.7 -- selalu structured fields, tidak pernah
// fmt.Println/log.Print.
func Logging(logger *zap.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()

		err := c.Next()

		fields := []zap.Field{
			zap.String("request_id", requestIDFromLocals(c)),
			zap.String("method", c.Method()),
			zap.String("path", c.Path()),
			zap.Int("status", c.Response().StatusCode()),
			zap.Int64("duration_ms", time.Since(start).Milliseconds()),
		}

		if span := trace.SpanContextFromContext(c.UserContext()); span.HasTraceID() {
			fields = append(fields, zap.String("trace_id", span.TraceID().String()))
		}

		if err != nil {
			fields = append(fields, zap.Error(err))
		}

		logger.Info("request", fields...)
		return err
	}
}

func requestIDFromLocals(c *fiber.Ctx) string {
	id, ok := c.Locals(RequestIDLocalsKey).(string)
	if !ok {
		return ""
	}
	return id
}
