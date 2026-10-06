package handler

import (
	"errors"
	"io"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// ProjectImportHandler -- Import CSV level PROJECT (PM/Admin Workspace),
// "PM Import CSV.dc.html", IG-120. Tahap (a): kind=sprint.
type ProjectImportHandler struct {
	imports *service.ProjectImportService
	logger  *zap.Logger
}

func NewProjectImportHandler(imports *service.ProjectImportService, logger *zap.Logger) *ProjectImportHandler {
	return &ProjectImportHandler{imports: imports, logger: logger}
}

// actorAndTx -- rute project tanpa RequireRole (pola SprintHandler): actorRole
// bisa kosong, service meresolve role efektif sendiri.
func (h *ProjectImportHandler) actorAndTx(c *fiber.Ctx) (actorID, actorRole string, exec db.Executor, ok bool) {
	actorID, actorRole, ok = middleware.ActorFromContext(c)
	if !ok {
		_ = c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
		return "", "", nil, false
	}
	exec, ok = middleware.DBTxFromContext(c)
	if !ok {
		_ = c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
		return "", "", nil, false
	}
	return actorID, actorRole, exec, true
}

// Template menangani GET /projects/:id/data-import/template?kind=sprint.
func (h *ProjectImportHandler) Template(c *fiber.Ctx) error {
	kind := c.Query("kind", "sprint")
	data, ok := service.ProjectImportTemplate(kind)
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "kind tidak dikenal -- gunakan 'sprint' atau 'task'", nil))
	}
	c.Set("Content-Type", "text/csv")
	c.Set("Content-Disposition", `attachment; filename="`+kind+`-import-template.csv"`)
	return c.Send(data)
}

// Validate menangani POST /projects/:id/data-import/validate (multipart:
// file + kind) -- pratinjau dry-run, TIDAK menulis sprint.
func (h *ProjectImportHandler) Validate(c *fiber.Ctx) error {
	actorID, actorRole, exec, ok := h.actorAndTx(c)
	if !ok {
		return nil
	}
	fh, err := c.FormFile("file")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Berkas CSV wajib diunggah (field 'file')", nil))
	}
	f, err := fh.Open()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal membaca berkas", nil))
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal membaca berkas", nil))
	}

	res, err := h.imports.Validate(c.Context(), exec, c.Params("id"), c.FormValue("kind", "sprint"), fh.Filename, data, actorID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal memvalidasi berkas CSV")
	}
	return c.JSON(response.Success(fiber.Map{
		"import_id": res.ImportID, "kind": res.Kind, "total_rows": res.Total,
		"valid_count": res.ValidN, "skipped_count": res.SkippedN, "preview": res.Preview,
	}))
}

// Execute menangani POST /projects/:id/data-import/:importId/execute.
func (h *ProjectImportHandler) Execute(c *fiber.Ctx) error {
	actorID, actorRole, exec, ok := h.actorAndTx(c)
	if !ok {
		return nil
	}
	imp, err := h.imports.Execute(c.Context(), exec, c.Params("id"), c.Params("importId"), actorID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal menjalankan import")
	}
	return c.JSON(response.Success(projectImportJSON(imp)))
}

// Get menangani GET /projects/:id/data-import/:importId.
func (h *ProjectImportHandler) Get(c *fiber.Ctx) error {
	actorID, actorRole, exec, ok := h.actorAndTx(c)
	if !ok {
		return nil
	}
	imp, err := h.imports.Get(c.Context(), exec, c.Params("id"), c.Params("importId"), actorID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil import")
	}
	out := projectImportJSON(imp)
	out["row_results"] = imp.RowResults
	return c.JSON(response.Success(out))
}

// History menangani GET /projects/:id/data-import/history.
func (h *ProjectImportHandler) History(c *fiber.Ctx) error {
	actorID, actorRole, exec, ok := h.actorAndTx(c)
	if !ok {
		return nil
	}
	list, err := h.imports.ListHistory(c.Context(), exec, c.Params("id"), actorID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil riwayat import")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = projectImportJSON(&list[i])
	}
	return c.JSON(response.Success(data))
}

// Report menangani GET /projects/:id/data-import/:importId/report?only=skipped.
func (h *ProjectImportHandler) Report(c *fiber.Ctx) error {
	actorID, actorRole, exec, ok := h.actorAndTx(c)
	if !ok {
		return nil
	}
	only := c.Query("only") == "skipped"
	data, err := h.imports.Report(c.Context(), exec, c.Params("id"), c.Params("importId"), only, actorID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal membuat laporan")
	}
	name := "laporan-import.csv"
	if only {
		name = "baris-dilewati.csv"
	}
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	return c.Send(data)
}

func projectImportJSON(imp *repository.ProjectImport) fiber.Map {
	return fiber.Map{
		"id": imp.ID, "project_id": imp.ProjectID, "kind": imp.Kind, "filename": imp.Filename, "status": imp.Status,
		"total_rows": imp.TotalRows, "success_count": imp.SuccessCount, "failed_count": imp.FailedCount,
		"imported_by_name": imp.ImportedByName, "created_at": imp.CreatedAt, "completed_at": imp.CompletedAt,
	}
}

func (h *ProjectImportHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR",
			"Input tidak valid -- jenis import harus 'sprint' atau 'task' dan berkas CSV wajib memuat kolom wajibnya (sprint: code, name; task: title)", nil))
	case errors.Is(err, domain.ErrCSVTooLarge):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("CSV_TOO_LARGE", "Berkas melebihi 10 MB", nil))
	case errors.Is(err, domain.ErrCSVTooManyRows):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("CSV_TOO_MANY_ROWS", "Berkas melebihi 5.000 baris", nil))
	case errors.Is(err, domain.ErrCSVImportNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Import tidak ditemukan", nil))
	case errors.Is(err, domain.ErrCSVImportAlreadyStarted):
		return c.Status(fiber.StatusConflict).JSON(response.Error("ALREADY_STARTED", "Import ini sudah dijalankan", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Hanya Project Manager atau Admin Workspace yang dapat mengimpor data project.", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
