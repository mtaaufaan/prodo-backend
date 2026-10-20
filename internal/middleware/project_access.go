package middleware

import (
	"context"

	"github.com/gofiber/fiber/v2"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

// FollowedProjectAccessChecker -- interface didefinisikan di consumer,
// diimplementasikan repository.FollowedProjectAccessRepository (S6E-02, IG-133).
// restricted true untuk role workspace `editor` atau `approver` (diperluas di
// S7A-02, IG-148); project/task yang tidak ditemukan -> (false, false, nil).
type FollowedProjectAccessChecker interface {
	FollowedProjectAccess(ctx context.Context, exec db.Executor, projectID, userID string) (restricted, followed bool, err error)
	FollowedTaskAccess(ctx context.Context, exec db.Executor, taskID, userID string) (restricted, followed bool, err error)
}

// RequireFollowedProjectAccess -- gerbang baca untuk endpoint yang dipakai frame
// Editor dan Approver: role itu yang BUKAN anggota/assignee/PIC aktif project
// -> 403 NOT_PROJECT_MEMBER. Role lain (AW/PM/viewer/GA/PA) lolos tanpa
// perubahan. byTask true: param :id adalah id task (GET /tasks/:id), project
// diturunkan dari task; false: :id adalah id project. Harus dipasang SETELAH
// jwtAuth dan DBContextMiddleware.
func RequireFollowedProjectAccess(checker FollowedProjectAccessChecker, byTask bool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userID, _, ok := ActorFromContext(c)
		exec, txOK := DBTxFromContext(c)
		if !ok || !txOK {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fiber.Map{"code": "INTERNAL_ERROR", "message": "RequireFollowedProjectAccess butuh DBContextMiddleware dipasang lebih dulu di route ini"},
			})
		}

		var (
			restricted, followed bool
			err                  error
		)
		if byTask {
			restricted, followed, err = checker.FollowedTaskAccess(c.Context(), exec, c.Params("id"), userID)
		} else {
			restricted, followed, err = checker.FollowedProjectAccess(c.Context(), exec, c.Params("id"), userID)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fiber.Map{"code": "INTERNAL_ERROR", "message": "Gagal memproses permintaan"},
			})
		}
		if restricted && !followed {
			return forbidden(c, "NOT_PROJECT_MEMBER", "Anda bukan anggota project ini.")
		}
		return c.Next()
	}
}
