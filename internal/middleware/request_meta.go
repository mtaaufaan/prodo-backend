package middleware

import (
	"github.com/gofiber/fiber/v2"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

// RequestMeta menyuntikkan IP dan "METHOD scheme://host/path" request aktif
// ke fasthttp.RequestCtx (via SetUserValue) supaya layer repository yang
// jauh dari HTTP (logAudit dkk. di account_repository.go) bisa membacanya
// lewat ctx.Value tanpa mengubah signature setiap fungsi repository/service
// di sepanjang rantai panggilan (2026-08-29, permintaan user: audit trail
// perlu info asal request). BaseURL (bukan cuma path) disertakan supaya
// entry audit trail bisa dibedakan berasal dari dev atau production kalau
// log-nya suatu saat dikumpulkan lintas environment. Dipasang paling awal
// (app.Use) supaya berlaku untuk SEMUA route, termasuk yang menulis
// audit_logs (bukan cuma platform_audit_logs).
func RequestMeta() fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Context().SetUserValue(domain.RequestMetaKey, domain.RequestMeta{
			IP:   c.IP(),
			Path: c.Method() + " " + c.BaseURL() + c.Path(),
		})
		return c.Next()
	}
}
