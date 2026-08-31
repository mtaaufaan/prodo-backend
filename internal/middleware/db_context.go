package middleware

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

const dbTxLocalsKey = "prodo_db_tx"

// DBTxFromContext mengembalikan transaksi request-scoped yang disuntik
// DBContextMiddleware (S2-11) -- dipakai handler/middleware lain untuk
// memanggil service/repository yang menyentuh tabel ber-RLS.
func DBTxFromContext(c *fiber.Ctx) (db.Executor, bool) {
	tx, ok := c.Locals(dbTxLocalsKey).(db.Executor)
	return tx, ok
}

// DBContextMiddleware membuka SATU transaksi per-request dan menyuntikkan
// session variable RLS (RLS_DESIGN.md §3/§5) SEBELUM handler (dan
// RequireRole, yang query workspace_members ber-RLS) berjalan -- commit
// kalau handler sukses, rollback kalau error. WAJIB dipasang SETELAH
// JWTAuth dan SEBELUM RequireRole di route yang menyentuh tabel ber-RLS
// (S2-10).
//
// Diterapkan HANYA di route /workspaces/... untuk sekarang, BUKAN global
// -- baru dua tabel (workspace_members, notifications) yang ber-RLS di
// S2, route lain (session, MFA, activation) menyentuh tabel yang sengaja
// TIDAK di-RLS (RLS_DESIGN.md §8) jadi tidak butuh transaksi ini. Perluas
// ke global begitu lebih banyak tabel ber-RLS ada di S3+ (implementation_
// gaps.md IG-10).
func DBContextMiddleware(pool *pgxpool.Pool, users UserResolver) fiber.Handler {
	return func(c *fiber.Ctx) error {
		claims, ok := ClaimsFromContext(c)
		if !ok {
			return unauthorized(c, "INVALID_CREDENTIALS", "Token tidak ditemukan")
		}

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

		tx, err := db.SetRLSContext(c.Context(), pool, actorUserID, claims.PlatformRole)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fiber.Map{"code": "INTERNAL_ERROR", "message": "Gagal menyiapkan koneksi database"},
			})
		}
		c.Locals(dbTxLocalsKey, db.Executor(tx))

		handlerErr := c.Next()

		// Handler di codebase ini menandai kegagalan dengan menulis response
		// JSON error lewat mapError (c.Status(4xx/5xx).JSON(...)) dan
		// mengembalikan nil ke Fiber -- BUKAN dengan mengembalikan Go error
		// literal. handlerErr saja karena itu SELALU nil untuk kegagalan
		// bisnis biasa (validasi, conflict, dsb.), sehingga versi lama di
		// sini selalu commit walau responsenya 4xx/5xx -- ditemukan S4G-05
		// lewat POST /organizations yang menulis (INSERT) sebelum
		// memvalidasi kuota: percobaan yang divalidasi GAGAL (422) tetap
		// commit organisasi dengan kuota/retensi default, DAN percobaan
		// berikutnya kena "current transaction is aborted" saat COMMIT
		// (Postgres mengabort seluruh transaksi begitu satu statement error,
		// mis. unique violation slug) -- menutupi response asli (409) dengan
		// 500 generik "Gagal menyimpan perubahan". Cek status response,
		// bukan cuma handlerErr, supaya rollback sesuai intent asli komentar
		// method ini ("rollback kalau error").
		if handlerErr != nil || c.Response().StatusCode() >= fiber.StatusBadRequest {
			tx.Rollback(c.Context()) //nolint:errcheck // request sudah gagal, rollback best-effort
			return handlerErr
		}
		if err := tx.Commit(c.Context()); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fiber.Map{"code": "INTERNAL_ERROR", "message": "Gagal menyimpan perubahan"},
			})
		}
		return nil
	}
}
