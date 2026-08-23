package middleware

import (
	"context"
	"fmt"

	"github.com/gofiber/fiber/v2"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
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
// diimplementasikan service.RBACService. exec (db.Executor, S2-10/11)
// adalah transaksi request-scoped dari DBContextMiddleware -- query
// workspace_members di sini kena RLS, jadi WAJIB dipanggil dengan exec
// yang session variable-nya sudah disuntik (bukan sembarang koneksi).
type WorkspaceRoleChecker interface {
	GetMemberRole(ctx context.Context, exec db.Executor, workspaceID, userID string) (string, error)

	// GetWorkspaceOrgID -- S3-41, dasar scoping Group Admin di RequireRole
	// (implementation_gaps.md IG-01).
	GetWorkspaceOrgID(ctx context.Context, exec db.Executor, workspaceID string) (string, error)
}

// RequirePlatformRole meloloskan request HANYA kalau claims.PlatformRole ada
// di antara `roles` -- gerbang cepat berbasis klaim JWT saja (tanpa query
// DB), untuk endpoint level-platform yang dibagi lebih dari satu platform
// role (mis. organizations: Platform Admin ATAU Group Admin, S3-02/03/04).
// Beda dari RequireRole (S2-09) yang scoped ke workspace tertentu lewat
// param :wsId + query workspace_members -- endpoint organizations tidak
// selalu punya org yang sudah ada (POST /organizations bikin org BARU),
// jadi scoping detail (GA ini benar-benar pengelola grup yang dituju atau
// bukan) dilakukan di service layer via group_admin_assignments, bukan di
// sini. actorUserID diresolve & disimpan di locals sama seperti RequireRole
// supaya handler tidak query ulang.
func RequirePlatformRole(users UserResolver, roles ...string) fiber.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}

	return func(c *fiber.Ctx) error {
		claims, ok := ClaimsFromContext(c)
		if !ok {
			return unauthorized(c, "INVALID_CREDENTIALS", "Token tidak ditemukan")
		}
		if !allowed[claims.PlatformRole] {
			return forbidden(c, "FORBIDDEN", "Anda tidak memiliki izin untuk mengakses resource ini.")
		}

		// actorUserID mungkin sudah diresolve DBContextMiddleware yang
		// jalan lebih dulu (S3-42, route organizations sekarang butuh
		// dbCtx) -- pakai itu kalau ada, hindari query users dua kali.
		if _, ok := c.Locals(actorUserIDLocalsKey).(string); !ok {
			resolved, err := users.ResolveActorUserID(c.Context(), claims.Subject)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": fiber.Map{"code": "INTERNAL_ERROR", "message": "Gagal mengidentifikasi user"},
				})
			}
			c.Locals(actorUserIDLocalsKey, resolved)
		}
		c.Locals(actorRoleLocalsKey, claims.PlatformRole)
		return c.Next()
	}
}

// GroupAdminOrgChecker -- interface didefinisikan di consumer, lihat §3.9.
// Diimplementasikan repository.SessionRepository (S3-40).
type GroupAdminOrgChecker interface {
	IsUserInOrg(ctx context.Context, userID, orgID string) (bool, error)
}

// RequireGroupAdminInOrg mengecek apakah actor berwenang atas targetUserID
// (S3-39, implementation_gaps.md IG-01): Platform Admin selalu lolos; Group
// Admin lolos kalau targetUserID adalah member SALAH SATU org di
// claims.ProdoOrgIDs (S3-38); role lain ditolak.
//
// BUKAN fiber.Handler seperti RequireRole/RequirePlatformRole -- route yang
// butuh ini (S1-30/35 admin sessions, dst.) punya target di param :userId,
// bukan :orgId, jadi org yang relevan baru bisa diketahui SETELAH resolve
// target (checker.IsUserInOrg per org GA) -- tidak bisa dicek generik di
// edge routing seperti middleware berbasis :wsId/:orgId. Dipanggil langsung
// dari handler setelah RequirePlatformRole (gerbang kasar PA/GA) meloloskan
// request.
func RequireGroupAdminInOrg(ctx context.Context, checker GroupAdminOrgChecker, claims *Claims, targetUserID string) error {
	if claims.PlatformRole == "platform_admin" {
		return nil
	}
	if claims.PlatformRole != "group_admin" {
		return domain.ErrForbidden
	}
	for _, orgID := range claims.ProdoOrgIDs {
		inOrg, err := checker.IsUserInOrg(ctx, targetUserID, orgID)
		if err != nil {
			return fmt.Errorf("middleware.RequireGroupAdminInOrg: %w", err)
		}
		if inOrg {
			return nil
		}
	}
	return domain.ErrForbidden
}

// RequireRole meloloskan request HANYA kalau actor adalah Platform Admin
// (bypass penuh, konsisten dengan RequirePlatformAdmin()) ATAU salah satu
// dari `roles` di workspace target (S2-09, US-003). Route WAJIB punya
// param :wsId DAN sudah dipasangi DBContextMiddleware (S2-10/11) SEBELUM
// middleware ini -- GetMemberRole query workspace_members yang sekarang
// ber-RLS, tanpa session variable dari DBContextMiddleware query itu akan
// selalu kosong (default RLS: tolak semua).
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

		// actorUserID mungkin sudah diresolve DBContextMiddleware yang
		// jalan lebih dulu -- pakai itu kalau ada, hindari query users
		// dua kali per request.
		actorUserID, ok := c.Locals(actorUserIDLocalsKey).(string)
		if !ok {
			resolved, err := users.ResolveActorUserID(c.Context(), claims.Subject)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": fiber.Map{"code": "INTERNAL_ERROR", "message": "Gagal mengidentifikasi user"},
				})
			}
			actorUserID = resolved
			c.Locals(actorUserIDLocalsKey, actorUserID)
		}

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

		exec, ok := DBTxFromContext(c)
		if !ok {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fiber.Map{"code": "INTERNAL_ERROR", "message": "RequireRole butuh DBContextMiddleware dipasang lebih dulu di route ini"},
			})
		}

		// S3-41 (implementation_gaps.md IG-01): Group Admin lolos (seperti
		// Platform Admin -- bypass penuh `roles`, bukan diperiksa terhadap
		// `allowed`) kalau workspace target ada di ORGANISASI yang dia
		// kelola (claims.ProdoOrgIDs, S3-38), TIDAK PERLU jadi
		// workspace_members. GA yang bukan pengelola org workspace ini
		// jatuh lanjut ke pengecekan role biasa di bawah (kemungkinan besar
		// tetap ditolak, kecuali GA itu KEBETULAN juga workspace member --
		// jarang, tapi tidak sengaja diblokir).
		if claims.PlatformRole == "group_admin" {
			workspaceOrgID, err := checker.GetWorkspaceOrgID(c.Context(), exec, workspaceID)
			if err == nil {
				for _, gaOrgID := range claims.ProdoOrgIDs {
					if gaOrgID == workspaceOrgID {
						c.Locals(actorRoleLocalsKey, "group_admin")
						return c.Next()
					}
				}
			}
		}

		role, err := checker.GetMemberRole(c.Context(), exec, workspaceID, actorUserID)
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
