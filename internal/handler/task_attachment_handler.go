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

// TaskAttachmentHandler -- Attachment Management (H20-22, S4W-19/20/21,
// EPIC 10). Lihat komentar package service untuk batas cakupan.
type TaskAttachmentHandler struct {
	attachments *service.TaskAttachmentService
	logger      *zap.Logger
}

func NewTaskAttachmentHandler(attachments *service.TaskAttachmentService, logger *zap.Logger) *TaskAttachmentHandler {
	return &TaskAttachmentHandler{attachments: attachments, logger: logger}
}

// Upload menangani POST /tasks/:id/attachments (multipart, field "file").
func (h *TaskAttachmentHandler) Upload(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.Upload dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.Upload dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	taskID := c.Params("id")

	fh, err := c.FormFile("file")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "File wajib diunggah (field 'file')", nil))
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

	att, err := h.attachments.Upload(c.Context(), exec, taskID, fh.Filename, data, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengunggah lampiran")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(attachmentJSON(att)))
}

// ListForTask menangani GET /tasks/:id/attachments -- tab Lampiran.
func (h *TaskAttachmentHandler) ListForTask(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.ListForTask dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	taskID := c.Params("id")

	list, err := h.attachments.ListForTask(c.Context(), exec, taskID)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil daftar lampiran")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = attachmentJSON(&list[i])
	}
	return c.JSON(response.Success(data))
}

// Download menangani GET /attachments/:id/download.
func (h *TaskAttachmentHandler) Download(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.Download dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	id := c.Params("id")

	att, data, err := h.attachments.Download(c.Context(), exec, id)
	if err != nil {
		return h.mapError(c, err, "Gagal mengunduh lampiran")
	}
	c.Set("Content-Type", att.MimeType)
	c.Set("Content-Disposition", `attachment; filename="`+att.DisplayName+`"`)
	return c.Send(data)
}

type renameAttachmentRequest struct {
	DisplayName string `json:"display_name"`
}

// Rename menangani PUT /attachments/:id.
func (h *TaskAttachmentHandler) Rename(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.Rename dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.Rename dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	id := c.Params("id")

	var body renameAttachmentRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	if err := h.attachments.Rename(c.Context(), exec, id, body.DisplayName, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal mengganti nama lampiran")
	}
	return c.JSON(response.Success(fiber.Map{"id": id, "display_name": body.DisplayName}))
}

// Delete menangani DELETE /attachments/:id -- mode retensi (dari Task
// Detail, US-064b "Hapus").
func (h *TaskAttachmentHandler) Delete(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.Delete dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.Delete dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	id := c.Params("id")

	if err := h.attachments.Delete(c.Context(), exec, id, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal menghapus lampiran")
	}
	return c.JSON(response.Success(fiber.Map{"id": id}))
}

// Restore menangani POST /attachments/:id/restore.
func (h *TaskAttachmentHandler) Restore(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.Restore dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.Restore dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	id := c.Params("id")

	if err := h.attachments.Restore(c.Context(), exec, id, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal memulihkan lampiran")
	}
	return c.JSON(response.Success(fiber.Map{"id": id}))
}

// ListForWorkspace menangani GET /workspaces/:wsId/documents -- grid "AW
// Documents", query: project_id, status, ext, uploader_id, sort.
// Mengembalikan SELURUH hasil filter+urut (tanpa paginasi server) -- pola
// konsisten seluruh FE codebase ini, lihat komentar repository.ListForWorkspace.
func (h *TaskAttachmentHandler) ListForWorkspace(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.ListForWorkspace dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.ListForWorkspace dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	f := repository.AttachmentFilter{
		ProjectID:  c.Query("project_id"),
		Status:     c.Query("status"),
		Ext:        c.Query("ext"),
		UploaderID: c.Query("uploader_id"),
		Sort:       c.Query("sort"),
	}
	list, _, err := h.attachments.ListForWorkspace(c.Context(), exec, workspaceID, &f, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil daftar dokumen")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = attachmentJSON(&list[i])
		data[i]["status"] = list[i].Status()
		data[i]["task_code"] = list[i].TaskCode
		data[i]["task_title"] = list[i].TaskTitle
		data[i]["project_id"] = list[i].ProjectID
		data[i]["project_name"] = list[i].ProjectName
		data[i]["sprint_name"] = list[i].SprintName
	}
	return c.JSON(response.Success(data))
}

// Quota menangani GET /workspaces/:wsId/documents/quota.
func (h *TaskAttachmentHandler) Quota(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.Quota dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.Quota dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	overview, err := h.attachments.QuotaOverview(c.Context(), exec, workspaceID, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil info kuota")
	}
	perProject := make([]fiber.Map, len(overview.PerProject))
	for i, p := range overview.PerProject {
		perProject[i] = fiber.Map{"project_id": p.ProjectID, "project_name": p.ProjectName, "bytes": p.Bytes, "file_count": p.FileCount}
	}
	return c.JSON(response.Success(fiber.Map{
		"quota_bytes": overview.QuotaBytes, "used_bytes": overview.UsedBytes, "retention_days": overview.RetentionDays,
		"per_project": perProject,
	}))
}

type documentDeleteRequest struct {
	Mode                 string `json:"mode"` // "retensi" (default) | "permanen"
	ConfirmWorkspaceName string `json:"confirm_workspace_name"`
}

// DeleteForWorkspace menangani DELETE /workspaces/:wsId/documents/:id.
func (h *TaskAttachmentHandler) DeleteForWorkspace(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.DeleteForWorkspace dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.DeleteForWorkspace dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")
	id := c.Params("id")

	var body documentDeleteRequest
	_ = c.BodyParser(&body)

	var err error
	if body.Mode == "permanen" {
		err = h.attachments.PermanentDelete(c.Context(), exec, workspaceID, id, body.ConfirmWorkspaceName, actorUserID, actorRole)
	} else {
		err = h.attachments.Delete(c.Context(), exec, id, actorUserID, actorRole)
	}
	if err != nil {
		return h.mapError(c, err, "Gagal menghapus dokumen")
	}
	return c.JSON(response.Success(fiber.Map{"id": id}))
}

type bulkDeleteRequest struct {
	IDs                  []string `json:"ids"`
	Mode                 string   `json:"mode"`
	ConfirmWorkspaceName string   `json:"confirm_workspace_name"`
}

// BulkDelete menangani POST /workspaces/:wsId/documents/bulk-delete --
// "HAPUS TERPILIH".
func (h *TaskAttachmentHandler) BulkDelete(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.BulkDelete dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.BulkDelete dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	var body bulkDeleteRequest
	if err := c.BodyParser(&body); err != nil || len(body.IDs) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Daftar id lampiran wajib diisi", nil))
	}
	succeeded, err := h.attachments.BulkDelete(c.Context(), exec, workspaceID, body.IDs, body.Mode, body.ConfirmWorkspaceName, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal menghapus dokumen terpilih")
	}
	return c.JSON(response.Success(fiber.Map{"succeeded": succeeded, "total": len(body.IDs)}))
}

type quotaRequestRequest struct {
	AdditionalGB int    `json:"additional_gb"`
	Reason       string `json:"reason"`
}

// RequestQuota menangani POST /workspaces/:wsId/documents/quota-request --
// "MINTA TAMBAH KUOTA".
func (h *TaskAttachmentHandler) RequestQuota(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.RequestQuota dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("TaskAttachmentHandler.RequestQuota dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	var body quotaRequestRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	if err := h.attachments.RequestQuota(c.Context(), exec, workspaceID, body.AdditionalGB, body.Reason, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal mengirim permintaan kuota")
	}
	return c.JSON(response.Success(fiber.Map{"sent": true}))
}

func attachmentJSON(a *repository.TaskAttachment) fiber.Map {
	return fiber.Map{
		"id": a.ID, "task_id": a.TaskID, "uploader_id": a.UploaderID,
		"uploader_name": a.UploaderName, "uploader_email": a.UploaderEmail,
		"original_name": a.OriginalName, "display_name": a.DisplayName,
		"mime_type": a.MimeType, "size_bytes": a.SizeBytes, "is_image": a.IsImage,
		"created_at": a.CreatedAt, "deleted_at": a.DeletedAt, "purge_scheduled_at": a.PurgeScheduledAt,
	}
}

func (h *TaskAttachmentHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrAttachmentNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Lampiran tidak ditemukan", nil))
	case errors.Is(err, domain.ErrAttachmentTooLarge):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("FILE_TOO_LARGE", "Ukuran file melebihi 50 MB", nil))
	case errors.Is(err, domain.ErrAttachmentTypeNotAllowed):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("FILE_TYPE_NOT_ALLOWED", "Tipe file ini tidak diizinkan", nil))
	case errors.Is(err, domain.ErrStorageQuotaFull):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("STORAGE_QUOTA_FULL", "Penyimpanan organisasi penuh. Hubungi Group Admin Anda untuk menambah kuota.", nil))
	case errors.Is(err, domain.ErrAttachmentNotDeleted):
		return c.Status(fiber.StatusConflict).JSON(response.Error("NOT_DELETED", "Lampiran ini sedang tidak dalam status terhapus", nil))
	case errors.Is(err, domain.ErrAttachmentAlreadyPurged):
		return c.Status(fiber.StatusConflict).JSON(response.Error("ALREADY_PURGED", "Lampiran sudah dihapus permanen dan tidak dapat dipulihkan", nil))
	case errors.Is(err, domain.ErrWorkspaceNameConfirmMismatch):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("CONFIRM_MISMATCH", "Nama workspace yang diketik tidak cocok", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas lampiran ini", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
