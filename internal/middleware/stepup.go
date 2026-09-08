package middleware

import (
	"context"

	"github.com/gofiber/fiber/v2"
)

// StepUpChecker -- interface didefinisikan di consumer, diimplementasikan
// service.StepUpService.
type StepUpChecker interface {
	HasValidStepUp(ctx context.Context, jti string) (bool, error)
}

// RequireStepUp (S16-04, forward-pull Track S4G) menggerbangi aksi
// destruktif yang butuh verifikasi OTP ulang: session (jti) harus sudah
// pernah lolos POST /auth/step-up dalam 15 menit terakhir, kalau tidak ->
// 403 STEP_UP_REQUIRED. FE menangkap kode ini, tampilkan dialog OTP, retry
// request original setelah verifikasi sukses. Harus dipasang SETELAH
// JWTAuth (butuh Claims di locals).
func RequireStepUp(checker StepUpChecker) fiber.Handler {
	return func(c *fiber.Ctx) error {
		claims, ok := ClaimsFromContext(c)
		if !ok {
			return unauthorized(c, "INVALID_CREDENTIALS", "Token tidak ditemukan")
		}

		valid, err := checker.HasValidStepUp(c.Context(), claims.ID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "gagal memverifikasi step-up")
		}
		if !valid {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": fiber.Map{"code": "STEP_UP_REQUIRED", "message": "Verifikasi ulang (OTP) diperlukan untuk aksi ini"},
			})
		}
		return c.Next()
	}
}
