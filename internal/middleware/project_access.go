package middleware

import (
	"context"

	"github.com/gofiber/fiber/v2"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

// EditorProjectAccessChecker -- interface didefinisikan di consumer,
// diimplementasikan repository.EditorProjectAccessRepository (S6E-02, IG-133).
// isEditor true hanya untuk role workspace `editor`; project/task yang tidak
// ditemukan -> (false, false, nil).
type EditorProjectAccessChecker interface {
	EditorProjectAccess(ctx context.Context, exec db.Executor, projectID, userID string) (isEditor, followed bool, err error)
	EditorTaskAccess(ctx context.Context, exec db.Executor, taskID, userID string) (isEditor, followed bool, err error)
}

// RequireEditorProjectAccess -- gerbang baca untuk endpoint yang dipakai frame
// Editor: Editor yang BUKAN anggota/assignee/PIC aktif project -> 403
// NOT_PROJECT_MEMBER. Role lain (AW/PM/approver/viewer/GA/PA) lolos tanpa
// perubahan. byTask true: param :id adalah id task (GET /tasks/:id), project
// diturunkan dari task; false: :id adalah id project. Harus dipasang SETELAH
// jwtAuth dan DBContextMiddleware.
func RequireEditorProjectAccess(checker EditorProjectAccessChecker, byTask bool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userID, _, ok := ActorFromContext(c)
		exec, txOK := DBTxFromContext(c)
		if !ok || !txOK {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fiber.Map{"code": "INTERNAL_ERROR", "message": "RequireEditorProjectAccess butuh DBContextMiddleware dipasang lebih dulu di route ini"},
			})
		}

		var (
			isEditor, followed bool
			err                error
		)
		if byTask {
			isEditor, followed, err = checker.EditorTaskAccess(c.Context(), exec, c.Params("id"), userID)
		} else {
			isEditor, followed, err = checker.EditorProjectAccess(c.Context(), exec, c.Params("id"), userID)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fiber.Map{"code": "INTERNAL_ERROR", "message": "Gagal memproses permintaan"},
			})
		}
		if isEditor && !followed {
			return forbidden(c, "NOT_PROJECT_MEMBER", "Anda bukan anggota project ini.")
		}
		return c.Next()
	}
}
