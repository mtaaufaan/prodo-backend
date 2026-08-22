package middleware

import (
	"context"

	"github.com/gofiber/fiber/v2"
)

// Locals key -- pola sama dengan claimsLocalsKey (auth.go).
const (
	actorUserIDLocalsKey = "prodo_actor_user_id"
	actorRoleLocalsKey   = "prodo_actor_role"
)

// ActorFromContext mengembalikan actorUserID + role workspace yang SUDAH
// diresolve RequireRole (hindari handler query ulang hal yang sama).
// role kosong untuk Platform Admin (bypass -- tidak ada "role workspace"
// yang berarti untuknya).
func ActorFromContext(c *fiber.Ctx) (userID, role string, ok bool) {
	userID, ok = c.Locals(actorUserIDLocalsKey).(string)
	if !ok {
		return "", "", false
	}
	role, _ = c.Locals(actorRoleLocalsKey).(string)
	return userID, role, true
}

// UserResolver -- interface didefinisikan di consumer (§3.9), diimplementasikan
// service.AccountService. Menerjemahkan Keycloak subject (JWT claims.Subject)
// ke users.id internal.
type UserResolver interface {
	ResolveActorUserID(ctx context.Context, keycloakSub string) (string, error)
}

// WorkspaceRoleChecker -- interface didefinisikan di consumer (§3.9),
// diimplementasikan service.RBACService.
type WorkspaceRoleChecker interface {
	GetMemberRole(ctx context.Context, workspaceID, userID string) (string, error)
}

// RequireRole meloloskan request HANYA kalau actor adalah Platform Admin
// (bypass penuh, konsisten dengan RequirePlatformAdmin()) ATAU salah satu
// dari `roles` di workspace target (S2-09, US-003). Route WAJIB punya
// param :wsId -- middleware ini dipasang SETELAH JWTAuth.
//
// ⚠️ Gap yang sama dengan WorkspaceHandler.UpdateMemberRole (S2-04) dan
// SessionHandler (S1-30/35): Group Admin belum bisa lolos scoped ke
// organisasinya -- scoping GA butuh group_admin_assignments + traversal
// organisasi yang belum ada (implementation_gaps.md IG-01). GA yang bukan
// member langsung workspace-nya akan tetap 403 di sini.
func RequireRole(users UserResolver, checker WorkspaceRoleChecker, roles ...string) fiber.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}

	return func(c *fiber.Ctx) error {
		claims, ok := ClaimsFromContext(c)
		if !ok {
			return unauthorized(c, "INVALID_CREDENTIALS", "Token tidak ditemukan")
		}

		actorUserID, err := users.ResolveActorUserID(c.Context(), claims.Subject)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fiber.Map{"code": "INTERNAL_ERROR", "message": "Gagal mengidentifikasi user"},
			})
		}
		c.Locals(actorUserIDLocalsKey, actorUserID)

		if claims.PlatformRole == "platform_admin" {
			c.Locals(actorRoleLocalsKey, "platform_admin")
			return c.Next()
		}

		workspaceID := c.Params("wsId")
		if workspaceID == "" {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fiber.Map{"code": "INTERNAL_ERROR", "message": "RequireRole butuh param :wsId di route ini"},
			})
		}

		role, err := checker.GetMemberRole(c.Context(), workspaceID, actorUserID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fiber.Map{"code": "INTERNAL_ERROR", "message": "Gagal memproses permintaan"},
			})
		}
		if !allowed[role] {
			return forbidden(c, "FORBIDDEN", "Anda tidak memiliki izin untuk mengakses resource ini.")
		}
		c.Locals(actorRoleLocalsKey, role)
		return c.Next()
	}
}
