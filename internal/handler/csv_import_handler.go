package handler

import (
	"errors"
	"io"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// CSVImportHandler -- Import Data (S4G-15/16/17/18, Track S4G, desain
// "GA Import Data.dc.html"). Kind cuma "member" -- lihat komentar package
// service (kind "task" tidak bisa dibangun, tabel tasks belum ada).
type CSVImportHandler struct {
	imports *service.CSVImportService
	logger  *zap.Logger
}

func NewCSVImportHandler(imports *service.CSVImportService, logger *zap.Logger) *CSVImportHandler {
	return &CSVImportHandler{imports: imports, logger: logger}
}

// Template menangani GET /groups/:groupId/data-import/template?kind=member.
func (h *CSVImportHandler) Template(c *fiber.Ctx) error {
	if kind := c.Query("kind"); kind != "" && kind != "member" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "kind cuma mendukung 'member' untuk sekarang", nil))
	}
	c.Set("Content-Type", "text/csv")
	c.Set("Content-Disposition", `attachment; filename="member-import-template.csv"`)
	return c.Send(service.MemberImportTemplateCSV())
}

// Validate menangani POST /groups/:groupId/data-import/validate (multipart:
// file + org_id) -- dry-run sinkron, TIDAK menulis apa pun ke users/
// workspace_members.
func (h *CSVImportHandler) Validate(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("CSVImportHandler.Validate dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("CSVImportHandler.Validate dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")
	orgID := c.FormValue("org_id")

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

	result, err := h.imports.Validate(c.Context(), exec, groupID, orgID, fh.Filename, data, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal memvalidasi berkas CSV")
	}

	return c.JSON(response.Success(fiber.Map{
		"import_id": result.ImportID, "total_rows": result.Total,
		"valid_count": result.ValidN, "existing_count": result.ExistingN, "skipped_count": result.SkippedN,
		"preview": result.Preview,
	}))
}

// Execute menangani POST /groups/:groupId/data-import/:importId/execute --
// antre job Asynq, TIDAK memproses langsung (lihat komentar package service).
func (h *CSVImportHandler) Execute(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("CSVImportHandler.Execute dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")
	importID := c.Params("importId")

	if err := h.imports.Execute(c.Context(), exec, groupID, importID); err != nil {
		return h.mapError(c, err, "Gagal memulai eksekusi import")
	}
	return c.Status(fiber.StatusAccepted).JSON(response.Success(fiber.Map{"import_id": importID, "status": "queued"}))
}

// Get menangani GET /groups/:groupId/data-import/:importId -- dipoll FE
// sampai status jadi completed/failed.
func (h *CSVImportHandler) Get(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("CSVImportHandler.Get dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")
	importID := c.Params("importId")

	imp, err := h.imports.Get(c.Context(), exec, groupID, importID)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil status import")
	}
	return c.JSON(response.Success(csvImportJSON(imp)))
}

// History menangani GET /groups/:groupId/data-import/history.
func (h *CSVImportHandler) History(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("CSVImportHandler.History dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")

	list, err := h.imports.ListHistory(c.Context(), exec, groupID)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil riwayat import")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = csvImportJSON(&list[i])
	}
	return c.JSON(response.Success(data))
}

// Report menangani GET /groups/:groupId/data-import/:importId/report --
// unduh CSV hasil (baris berhasil+dilewati beserta alasan).
func (h *CSVImportHandler) Report(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("CSVImportHandler.Report dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")
	importID := c.Params("importId")

	csvBytes, err := h.imports.Report(c.Context(), exec, groupID, importID)
	if err != nil {
		return h.mapError(c, err, "Gagal membuat laporan")
	}
	c.Set("Content-Type", "text/csv")
	c.Set("Content-Disposition", `attachment; filename="laporan-import.csv"`)
	return c.Send(csvBytes)
}

func csvImportJSON(imp *repository.CSVImport) fiber.Map {
	return fiber.Map{
		"id":            imp.ID,
		"org_id":        imp.OrgID,
		"kind":          imp.Kind,
		"filename":      imp.Filename,
		"status":        imp.Status,
		"total_rows":    imp.TotalRows,
		"success_count": imp.SuccessCount,
		"failed_count":  imp.FailedCount,
		"row_results":   imp.RowResults,
		"created_at":    imp.CreatedAt,
		"completed_at":  imp.CompletedAt,
	}
}

func (h *CSVImportHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR",
			"Input tidak valid -- organisasi tujuan wajib dipilih dan berkas CSV wajib berisi kolom email, role, workspace", nil))
	case errors.Is(err, domain.ErrCSVTooLarge):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("CSV_TOO_LARGE", "Berkas melebihi 10 MB", nil))
	case errors.Is(err, domain.ErrCSVTooManyRows):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("CSV_TOO_MANY_ROWS", "Berkas melebihi 5.000 baris", nil))
	case errors.Is(err, domain.ErrOrganizationNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Organisasi tidak ditemukan dalam grup ini", nil))
	case errors.Is(err, domain.ErrCSVImportNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Import tidak ditemukan", nil))
	case errors.Is(err, domain.ErrCSVImportAlreadyStarted):
		return c.Status(fiber.StatusConflict).JSON(response.Error("ALREADY_STARTED", "Import ini sudah dieksekusi atau sedang berjalan", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas grup ini.", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
