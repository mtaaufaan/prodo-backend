package handler

import (
	"errors"
	"fmt"
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
	GroupID           string `json:"group_id"`
	Name              string `json:"name"`
	Slug              string `json:"slug"`
	Domain            string `json:"domain"`
	DefaultLanguage   string `json:"default_language"`
	StorageQuotaBytes int64  `json:"storage_quota_bytes"`
	RetentionDays     int    `json:"retention_days"`
}

// Create menangani POST /organizations (S3-02, domain/default_language/
// storage_quota_bytes/retention_days ditambahkan S4G-31/Track S4G sesuai
// desain "GA Add Organization.dc.html" -- satu form, satu submit).
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
	req.Domain = strings.TrimSpace(req.Domain)
	if req.GroupID == "" || req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "group_id dan name wajib diisi", nil))
	}
	if !validator.IsValidSlug(req.Slug) {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "slug tidak valid",
			[]response.FieldError{{Field: "slug", Message: "lowercase, alphanumeric, hyphen (mis. \"acme-corp\")"}}))
	}

	org, err := h.orgs.CreateOrganization(c.Context(), exec, req.GroupID, req.Name, req.Slug, req.Domain, req.DefaultLanguage, req.StorageQuotaBytes, req.RetentionDays, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal membuat organisasi")
	}

	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{
		"id":                  org.ID,
		"group_id":            org.GroupID,
		"name":                org.Name,
		"slug":                org.Slug,
		"domain":              org.Domain,
		"default_language":    org.DefaultLanguage,
		"storage_quota_bytes": org.StorageQuotaBytes,
		"retention_days":      org.RetentionDays,
	}))
}

type updateOrganizationRequest struct {
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	Domain string `json:"domain"`
}

// Update menangani PUT /organizations/:id (S3-03, domain ditambahkan
// S4G-02/Track S4G sesuai desain "GA Organizations.dc.html" -- lihat
// migrasi 20260910090000).
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
	req.Domain = strings.TrimSpace(req.Domain)
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "name wajib diisi", nil))
	}
	if !validator.IsValidSlug(req.Slug) {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "slug tidak valid",
			[]response.FieldError{{Field: "slug", Message: "lowercase, alphanumeric, hyphen (mis. \"acme-corp\")"}}))
	}

	if err := h.orgs.UpdateOrganization(c.Context(), exec, orgID, req.Name, req.Slug, req.Domain, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal mengubah organisasi")
	}

	return c.JSON(response.Success(fiber.Map{"id": orgID, "name": req.Name, "slug": req.Slug, "domain": req.Domain}))
}

type updateSettingsRequest struct {
	DefaultLanguage string `json:"default_language"`
}

// UpdateSettings menangani PUT /organizations/:id/settings (S3-30, US-010).
func (h *OrganizationHandler) UpdateSettings(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.UpdateSettings dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.UpdateSettings dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	orgID := c.Params("id")

	var req updateSettingsRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}

	if err := h.orgs.UpdateSettings(c.Context(), exec, orgID, req.DefaultLanguage, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal mengubah pengaturan organisasi")
	}

	return c.JSON(response.Success(fiber.Map{"id": orgID, "default_language": req.DefaultLanguage}))
}

type updateStorageQuotaRequest struct {
	StorageQuotaBytes int64 `json:"storage_quota_bytes"`
	RetentionDays     int   `json:"retention_days"`
}

// UpdateStorageQuota menangani PUT /organizations/:id/storage-quota (S3-34/
// US-011, retention_days ditambah S4G-03/Track S4G -- lihat komentar
// OrganizationRepository.UpdateStorageQuota soal kenapa digabung satu
// endpoint dengan kuota).
func (h *OrganizationHandler) UpdateStorageQuota(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.UpdateStorageQuota dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.UpdateStorageQuota dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	orgID := c.Params("id")

	var req updateStorageQuotaRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}

	if err := h.orgs.UpdateStorageQuota(c.Context(), exec, orgID, req.StorageQuotaBytes, req.RetentionDays, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal mengubah kuota storage")
	}

	return c.JSON(response.Success(fiber.Map{"id": orgID, "storage_quota_bytes": req.StorageQuotaBytes, "retention_days": req.RetentionDays}))
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
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.List dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("OrganizationHandler.List dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}

	// group_id (S4G-32, Track S4G, group switcher): opsional -- scoping
	// tambahan untuk Group Admin yang mengelola >1 grup, lihat komentar
	// OrganizationService.ListOrganizations.
	orgs, ceilingBytes, err := h.orgs.ListOrganizations(c.Context(), exec, c.Query("group_id"), actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil daftar organisasi")
	}

	data := make([]fiber.Map, len(orgs))
	for i := range orgs {
		o := &orgs[i]
		data[i] = fiber.Map{
			"id":                  o.ID,
			"group_id":            o.GroupID,
			"name":                o.Name,
			"slug":                o.Slug,
			"domain":              o.Domain,
			"default_language":    o.DefaultLanguage,
			"storage_quota_bytes": o.StorageQuotaBytes,
			"storage_max_bytes":   o.StorageMaxBytes,
			"storage_used_bytes":  o.StorageUsedBytes,
			"retention_days":      o.RetentionDays,
			"workspace_count":     o.WorkspaceCount,
			"member_count":        o.MemberCount,
			"deactivated_at":      o.DeactivatedAt,
			"created_at":          o.CreatedAt,
		}
	}
	// S4G-03, Track S4G: response dibungkus {organizations, group_storage_ceiling_bytes}
	// (BUKAN array polos seperti sebelumnya) -- FE punya interceptor axios
	// yang unwrap SATU level ".data" (lib/api.ts), jadi field sibling di luar
	// "data" akan hilang begitu saja kalau dipertahankan array-di-"data"
	// langsung. Membungkus dalam objek supaya group_storage_ceiling_bytes
	// (plafon storage grup, dipakai stat "KUOTA TERALOKASI / plafon") ikut
	// sampai ke FE.
	return c.JSON(response.Success(fiber.Map{"organizations": data, "group_storage_ceiling_bytes": ceilingBytes}))
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
	var retentionErr *domain.RetentionOutOfRangeError
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid -- domain harus format domain valid (mis. acme.co.id)", nil))
	case errors.Is(err, domain.ErrStorageQuotaBelowUsed):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("STORAGE_QUOTA_BELOW_USED", "Kuota tidak boleh lebih kecil dari storage yang sudah terpakai", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas grup/organisasi ini.", nil))
	case errors.Is(err, domain.ErrOrganizationNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Organisasi tidak ditemukan", nil))
	case errors.Is(err, domain.ErrSlugAlreadyExists):
		return c.Status(fiber.StatusConflict).JSON(response.Error("SLUG_ALREADY_EXISTS", "Slug sudah dipakai organisasi lain", nil))
	case errors.Is(err, domain.ErrStorageQuotaExceedsMax):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("STORAGE_QUOTA_EXCEEDS_MAX", "Kuota melebihi batas maksimum yang ditetapkan Platform Admin", nil))
	case errors.Is(err, domain.ErrGroupStorageQuotaExceedsCeiling):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("GROUP_STORAGE_QUOTA_EXCEEDS_CEILING", "Total kuota seluruh organisasi dalam grup akan melebihi plafon storage grup", nil))
	case errors.Is(err, domain.ErrOrganizationHasWorkspaces):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("ORGANIZATION_HAS_WORKSPACES", "Organisasi masih punya workspace aktif", nil))
	case errors.As(err, &retentionErr):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("RETENTION_OUT_OF_RANGE",
			fmt.Sprintf("Retensi harus antara %d dan %d hari (batas tier %s)", retentionErr.MinDays, retentionErr.MaxDays, retentionErr.TierName),
			fiber.Map{"min_days": retentionErr.MinDays, "max_days": retentionErr.MaxDays, "tier_name": retentionErr.TierName}))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
