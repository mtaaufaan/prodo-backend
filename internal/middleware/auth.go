package middleware

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"

	"github.com/mtaaufaan/prodo-backend/config"
)

// claimsLocalsKey adalah key Fiber locals tempat Claims tersimpan setelah
// JWTAuth berhasil memverifikasi token.
const claimsLocalsKey = "prodo_claims"

// Claims adalah subset klaim token Keycloak yang dipakai PRODO backend.
// prodo_platform_role datang dari protocol mapper custom di
// infra/keycloak/realm-PRODO.json (attribute Keycloak user, bukan role
// Keycloak native) -- lihat docs/DATABASE_SCHEMA.md §5.1 users.platform_role.
type Claims struct {
	jwt.RegisteredClaims
	Email        string `json:"email"`
	PlatformRole string `json:"prodo_platform_role"`
}

// SessionChecker -- interface didefinisikan di consumer (§3.9), diimplementasikan
// service.SessionService. Dicek di SETIAP request terautentikasi (S1-28):
// Redis blacklist dulu (cepat), baru sliding idle timeout di Postgres --
// lihat docs/DATABASE_SCHEMA.md §5.3.
type SessionChecker interface {
	IsValidSession(ctx context.Context, jti string) (bool, error)
}

// JWTAuth memverifikasi Bearer token terhadap JWKS Keycloak (RS256),
// mengecek sesi (S1-28: revoked/idle-timeout via SessionChecker), dan
// menyimpan klaimnya di Fiber locals untuk dipakai handler/RequirePlatformAdmin
// lewat ClaimsFromContext.
//
// Dimajukan dari S1-16 (aslinya dijadwalkan H6) karena S1-05 butuh gerbang
// otorisasi sekarang -- keputusan dikonfirmasi user, lihat docs/s1-kickoff.html.
//
// ponytail: audience token TIDAK divalidasi ketat terhadap KEYCLOAK_AUDIENCE.
// Realm ini belum meng-assign client role prodo-backend ke user biasa (baru
// realm role), jadi oidc-audience-resolve-mapper belum tentu menyertakan
// "prodo-backend" di klaim aud untuk token user asli. Signature + issuer +
// masa berlaku sudah cukup untuk kepercayaan token di tahap ini (satu
// issuer terpercaya, satu API). Tambah validasi audience ketat kalau nanti
// ada API/audience lain yang perlu dibedakan.
func JWTAuth(cfg *config.Config, sessions SessionChecker) (fiber.Handler, error) {
	jwksURL := fmt.Sprintf("%s/protocol/openid-connect/certs", cfg.KeycloakIssuer)
	k, err := keyfunc.NewDefaultCtx(context.Background(), []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("middleware.JWTAuth: ambil JWKS dari %s: %w", jwksURL, err)
	}

	return func(c *fiber.Ctx) error {
		// Kode error di sini pakai INVALID_CREDENTIALS (401), BUKAN
		// UNAUTHORIZED -- docs/coding-conventions.md §7.3 mendefinisikan
		// UNAUTHORIZED khusus untuk 403 (token valid tapi role tidak
		// berizin, lihat RequirePlatformAdmin di bawah).
		tokenStr, ok := strings.CutPrefix(c.Get("Authorization"), "Bearer ")
		if !ok || tokenStr == "" {
			return unauthorized(c, "INVALID_CREDENTIALS", "Token tidak ditemukan")
		}

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, k.Keyfunc,
			jwt.WithIssuer(cfg.KeycloakIssuer),
			jwt.WithValidMethods([]string{"RS256"}),
		)
		if err != nil || !token.Valid {
			if errors.Is(err, jwt.ErrTokenExpired) {
				return unauthorized(c, "TOKEN_EXPIRED", "Token sudah kedaluwarsa")
			}
			return unauthorized(c, "INVALID_CREDENTIALS", "Token tidak valid")
		}

		// S1-28: token secara kriptografis valid, tapi sesinya sendiri
		// bisa sudah di-revoke (logout/force-logout) atau idle > 30 menit
		// (sliding expiration) -- keduanya HARUS ditolak meski JWT-nya
		// sendiri belum expired. Tidak ada baris sesi sama sekali (mis.
		// token dari sebelum S1-27 ada) dianggap TIDAK valid juga --
		// fail-closed, konsisten dengan model "setiap login membuat sesi".
		valid, err := sessions.IsValidSession(c.Context(), claims.ID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "gagal memverifikasi sesi")
		}
		if !valid {
			return unauthorized(c, "TOKEN_EXPIRED", "Sesi sudah berakhir atau tidak aktif")
		}

		c.Locals(claimsLocalsKey, claims)
		return c.Next()
	}, nil
}

// RequirePlatformAdmin menolak request yang klaim prodo_platform_role-nya
// bukan "platform_admin". Harus dipasang setelah JWTAuth.
func RequirePlatformAdmin() fiber.Handler {
	return func(c *fiber.Ctx) error {
		claims, ok := ClaimsFromContext(c)
		if !ok || claims.PlatformRole != "platform_admin" {
			return forbidden(c, "UNAUTHORIZED", "Hanya Platform Admin yang dapat mengakses endpoint ini")
		}
		return c.Next()
	}
}

// ClaimsFromContext mengambil Claims yang disimpan JWTAuth di locals.
func ClaimsFromContext(c *fiber.Ctx) (*Claims, bool) {
	claims, ok := c.Locals(claimsLocalsKey).(*Claims)
	return claims, ok
}

func unauthorized(c *fiber.Ctx, code, message string) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error": fiber.Map{"code": code, "message": message},
	})
}

func forbidden(c *fiber.Ctx, code, message string) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"error": fiber.Map{"code": code, "message": message},
	})
}
