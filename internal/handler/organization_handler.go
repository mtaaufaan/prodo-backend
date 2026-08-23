package handler

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/validator"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// OrganizationHandler -- S3-02/03/04, US-007. Otorisasi platform-role kasar
// (Platform Admin ATAU Group Admin) sudah ditegakkan
// middleware.RequirePlatformRole di routing; scoping halus (GA ini
// benar-benar pengelola grup target) ada di OrganizationService.
type OrganizationHandler struct {
	orgs   *service.OrganizationService
	logger *zap.Logger
}

func NewOrganizationHandler(orgs *service.OrganizationService, logger *zap.Logger) *OrganizationHandler {
	return &OrganizationHandler{orgs: orgs, logger: logger}
}

type createOrganizationRequest struct {
	GroupID string `json:"group_id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
}

// Create menangani POST /organizations (S3-02).
func (h *OrganizationHandler) Create(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.Create dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.Create dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}

	var req createOrganizationRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.TrimSpace(req.Slug)
	if req.GroupID == "" || req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "group_id dan name wajib diisi", nil))
	}
	if !validator.IsValidSlug(req.Slug) {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "slug tidak valid",
			[]response.FieldError{{Field: "slug", Message: "lowercase, alphanumeric, hyphen (mis. \"acme-corp\")"}}))
	}

	org, err := h.orgs.CreateOrganization(c.Context(), exec, req.GroupID, req.Name, req.Slug, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal membuat organisasi")
	}

	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{
		"id":       org.ID,
		"group_id": org.GroupID,
		"name":     org.Name,
		"slug":     org.Slug,
	}))
}

type updateOrganizationRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Update menangani PUT /organizations/:id (S3-03). Field yang diedit
// name/slug -- lihat catatan sprint_backlog.md soal wording asli
// "nama/logo/domain" yang tidak sesuai DATABASE_SCHEMA.md §5.7.
func (h *OrganizationHandler) Update(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.Update dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.Update dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	orgID := c.Params("id")

	var req updateOrganizationRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.TrimSpace(req.Slug)
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "name wajib diisi", nil))
	}
	if !validator.IsValidSlug(req.Slug) {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "slug tidak valid",
			[]response.FieldError{{Field: "slug", Message: "lowercase, alphanumeric, hyphen (mis. \"acme-corp\")"}}))
	}

	if err := h.orgs.UpdateOrganization(c.Context(), exec, orgID, req.Name, req.Slug, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal mengubah organisasi")
	}

	return c.JSON(response.Success(fiber.Map{"id": orgID, "name": req.Name, "slug": req.Slug}))
}

// Deactivate menangani PUT /organizations/:id/deactivate (S3-04).
func (h *OrganizationHandler) Deactivate(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.Deactivate dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.Deactivate dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	orgID := c.Params("id")

	if err := h.orgs.DeactivateOrganization(c.Context(), exec, orgID, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal menonaktifkan organisasi")
	}

	return c.JSON(response.Success(fiber.Map{"id": orgID, "deactivated": true}))
}

// Reactivate menangani PUT /organizations/:id/reactivate -- kebalikan
// Deactivate, prasyarat S3-07 (lihat OrganizationRepository.Reactivate).
func (h *OrganizationHandler) Reactivate(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.Reactivate dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.Reactivate dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	orgID := c.Params("id")

	if err := h.orgs.ReactivateOrganization(c.Context(), exec, orgID, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal mengaktifkan kembali organisasi")
	}

	return c.JSON(response.Success(fiber.Map{"id": orgID, "deactivated": false}))
}

// List menangani GET /organizations -- prasyarat S3-07 (FE, sama pola
// IG-09). Scoping sepenuhnya lewat RLS (OrganizationService.ListOrganizations).
func (h *OrganizationHandler) List(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.List dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}

	orgs, err := h.orgs.ListOrganizations(c.Context(), exec)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil daftar organisasi")
	}

	data := make([]fiber.Map, len(orgs))
	for i := range orgs {
		o := &orgs[i]
		data[i] = fiber.Map{
			"id":             o.ID,
			"group_id":       o.GroupID,
			"name":           o.Name,
			"slug":           o.Slug,
			"deactivated_at": o.DeactivatedAt,
			"created_at":     o.CreatedAt,
		}
	}
	return c.JSON(response.Success(data))
}

// Delete menangani DELETE /organizations/:id (S3-05).
func (h *OrganizationHandler) Delete(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.Delete dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.Delete dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	orgID := c.Params("id")

	if err := h.orgs.DeleteOrganization(c.Context(), exec, orgID, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal menghapus organisasi")
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// Summary menangani GET /organizations/:id/summary (S3-06).
func (h *OrganizationHandler) Summary(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.Summary dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.Summary dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	orgID := c.Params("id")

	summary, err := h.orgs.GetSummary(c.Context(), exec, orgID, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil ringkasan organisasi")
	}

	return c.JSON(response.Success(fiber.Map{
		"member_count":       summary.MemberCount,
		"workspace_count":    summary.WorkspaceCount,
		"storage_used_bytes": summary.StorageUsedByte,
	}))
}

func (h *OrganizationHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas grup/organisasi ini.", nil))
	case errors.Is(err, domain.ErrOrganizationNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Organisasi tidak ditemukan", nil))
	case errors.Is(err, domain.ErrSlugAlreadyExists):
		return c.Status(fiber.StatusConflict).JSON(response.Error("SLUG_ALREADY_EXISTS", "Slug sudah dipakai organisasi lain", nil))
	case errors.Is(err, domain.ErrOrganizationHasWorkspaces):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("ORGANIZATION_HAS_WORKSPACES", "Organisasi masih punya workspace aktif", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
