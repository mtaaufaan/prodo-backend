package handler

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/validator"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// GroupAdminHandler -- S1-05, US-073: Platform Admin membuat akun Group Admin.
type GroupAdminHandler struct {
	accounts   *service.AccountService
	email      *service.EmailService
	appBaseURL string
	logger     *zap.Logger
}

func NewGroupAdminHandler(accounts *service.AccountService, email *service.EmailService, appBaseURL string, logger *zap.Logger) *GroupAdminHandler {
	return &GroupAdminHandler{accounts: accounts, email: email, appBaseURL: appBaseURL, logger: logger}
}

type createGroupAdminRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	// GroupName -- IG-21, sesuai desain "Tambah Group Admin" (field "Nama
	// Perusahaan / Grup"). Wajib -- setiap GA baru langsung mengelola satu
	// grup baru.
	GroupName string `json:"group_name"`
	// JobTitle/Address/Phone/Tier/StorageQuotaGB -- S4P-06/07, field
	// lengkap sesuai desain "PA Group Admin Form". Semua opsional kecuali
	// yang di atas -- Tier default "starter" kalau kosong.
	JobTitle       string `json:"job_title"`
	Address        string `json:"address"`
	Phone          string `json:"phone"`
	Tier           string `json:"tier"`
	StorageQuotaGB *int   `json:"storage_quota_gb"`
}

// Create menangani POST /platform/group-admins -- dipasang di belakang
// middleware.JWTAuth + middleware.RequirePlatformAdmin (lihat cmd/api/main.go).
func (h *GroupAdminHandler) Create(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}

	var req createGroupAdminRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.Email = strings.TrimSpace(req.Email)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.GroupName = strings.TrimSpace(req.GroupName)
	if req.Email == "" || req.DisplayName == "" || req.GroupName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "email, display_name, dan group_name wajib diisi", nil))
	}
	if !validator.IsValidEmail(req.Email) {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "format email tidak valid", nil))
	}

	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users",
			zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	result, err := h.accounts.CreateGroupAdmin(c.Context(), &service.CreateGroupAdminRequest{
		Email:           req.Email,
		DisplayName:     req.DisplayName,
		GroupName:       req.GroupName,
		JobTitle:        req.JobTitle,
		Address:         req.Address,
		Phone:           req.Phone,
		Tier:            req.Tier,
		StorageQuotaGB:  req.StorageQuotaGB,
		InvitedByUserID: actorUserID,
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrEmailAlreadyExists):
			return c.Status(fiber.StatusConflict).JSON(response.Error("CONFLICT", "Email sudah terdaftar", nil))
		case errors.Is(err, domain.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "email, display_name, dan group_name wajib diisi", nil))
		case errors.Is(err, domain.ErrInvalidTier):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("INVALID_TIER", "Tier tidak valid", nil))
		default:
			h.logger.Error("gagal membuat akun Group Admin", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal membuat akun Group Admin", nil))
		}
	}

	h.sendActivationEmail(c, result, "akun Group Admin dibuat tapi gagal kirim email aktivasi")

	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{
		"id":           result.UserID,
		"email":        result.Email,
		"display_name": result.DisplayName,
		"expires_at":   result.ExpiresAt.UTC().Format(time.RFC3339),
	}))
}

// List menangani GET /platform/group-admins -- daftar Group Admin untuk
// panel Platform Admin (S1-12). Pagination page/per_page sesuai
// docs/coding-conventions.md §7.1 (default 1/50, max per_page 200).
func (h *GroupAdminHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	if page < 1 {
		page = 1
	}
	perPage := c.QueryInt("per_page", 50)
	if perPage < 1 {
		perPage = 50
	}
	if perPage > 200 {
		perPage = 200
	}

	summaries, total, err := h.accounts.ListGroupAdmins(c.Context(), perPage, (page-1)*perPage)
	if err != nil {
		h.logger.Error("gagal mengambil daftar Group Admin", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil daftar Group Admin", nil))
	}

	data := make([]fiber.Map, len(summaries))
	for i := range summaries {
		data[i] = groupAdminSummaryToMap(&summaries[i])
	}

	totalPages := (total + perPage - 1) / perPage
	return c.JSON(fiber.Map{
		"data": data,
		"meta": fiber.Map{
			"page":        page,
			"per_page":    perPage,
			"total":       total,
			"total_pages": totalPages,
		},
	})
}

// ResendActivation menangani POST /platform/group-admins/:id/resend-activation
// -- S1-08. Meng-invalidate token lama, menerbitkan yang baru, kirim ulang
// email. Dipasang di belakang middleware yang sama dengan Create.
func (h *GroupAdminHandler) ResendActivation(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}

	targetUserID := c.Params("id")
	if targetUserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "ID user wajib diisi", nil))
	}

	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users",
			zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	result, err := h.accounts.ResendActivation(c.Context(), targetUserID, actorUserID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrUserNotFound), errors.Is(err, domain.ErrInvitationNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND",
				"User tidak ditemukan atau tidak ada invitation pending untuknya (mungkin sudah diaktivasi)", nil))
		default:
			h.logger.Error("gagal menerbitkan ulang token aktivasi", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menerbitkan ulang token aktivasi", nil))
		}
	}

	h.sendActivationEmail(c, result, "token aktivasi diterbitkan ulang tapi gagal kirim email")

	return c.JSON(response.Success(fiber.Map{
		"id":           result.UserID,
		"email":        result.Email,
		"display_name": result.DisplayName,
		"expires_at":   result.ExpiresAt.UTC().Format(time.RFC3339),
	}))
}

// Suspend menangani PUT /platform/group-admins/:id/suspend (S4P-02, US-067).
func (h *GroupAdminHandler) Suspend(c *fiber.Ctx) error {
	return h.setSuspension(c, true)
}

// Reactivate menangani PUT /platform/group-admins/:id/reactivate (S4P-02, US-067).
func (h *GroupAdminHandler) Reactivate(c *fiber.Ctx) error {
	return h.setSuspension(c, false)
}

// setSuspension -- logika bersama Suspend/Reactivate, cuma beda repo call
// dan pesan sukses.
func (h *GroupAdminHandler) setSuspension(c *fiber.Ctx, suspend bool) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}

	targetUserID := c.Params("id")
	if targetUserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "ID user wajib diisi", nil))
	}

	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users",
			zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	status := "suspended"
	if suspend {
		err = h.accounts.SuspendGroupAdmin(c.Context(), targetUserID, actorUserID)
	} else {
		status = "active"
		err = h.accounts.ReactivateGroupAdmin(c.Context(), targetUserID, actorUserID)
	}
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrUserNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Group Admin tidak ditemukan", nil))
		default:
			h.logger.Error("gagal mengubah status suspend Group Admin", zap.Bool("suspend", suspend), zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengubah status akun", nil))
		}
	}

	return c.JSON(response.Success(fiber.Map{
		"id":     targetUserID,
		"status": status,
	}))
}

type transferGroupRequest struct {
	ToUserID string `json:"to_user_id"`
}

// Transfer menangani POST /platform/group-admins/:id/transfer (S4P-03/04,
// IG-21) -- pindahkan pengelolaan seluruh grup :id ke to_user_id.
func (h *GroupAdminHandler) Transfer(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}

	fromUserID := c.Params("id")
	if fromUserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "ID user wajib diisi", nil))
	}

	var req transferGroupRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.ToUserID = strings.TrimSpace(req.ToUserID)
	if req.ToUserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "to_user_id wajib diisi", nil))
	}

	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users",
			zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	count, err := h.accounts.TransferGroup(c.Context(), fromUserID, req.ToUserID, actorUserID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidTransferTarget):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("INVALID_TRANSFER_TARGET",
				"Target transfer bukan akun Group Admin yang valid", nil))
		default:
			h.logger.Error("gagal transfer grup", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal transfer grup", nil))
		}
	}

	return c.JSON(response.Success(fiber.Map{
		"from_user_id":       fromUserID,
		"to_user_id":         req.ToUserID,
		"transferred_groups": count,
	}))
}

// Delete menangani DELETE /platform/group-admins/:id (S4P-05, IG-21) --
// HANYA berhasil kalau target sudah tidak mengelola grup manapun.
func (h *GroupAdminHandler) Delete(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}

	targetUserID := c.Params("id")
	if targetUserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "ID user wajib diisi", nil))
	}

	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users",
			zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	if err := h.accounts.DeleteGroupAdmin(c.Context(), targetUserID, actorUserID); err != nil {
		switch {
		case errors.Is(err, domain.ErrGroupTransferRequired):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("GROUP_TRANSFER_REQUIRED",
				"Transfer grup ke Group Admin lain sebelum menghapus akun ini", nil))
		case errors.Is(err, domain.ErrUserNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Group Admin tidak ditemukan", nil))
		default:
			h.logger.Error("gagal menghapus akun Group Admin", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menghapus akun Group Admin", nil))
		}
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// groupAdminStatusLabel -- S4P-06, 3 state sesuai desain (dropdown Status
// "PA Group Admin Form"): AKTIF (aktif, tidak disuspend), SUSPENDED
// (disuspend PA), TIDAK AKTIF (belum menyelesaikan aktivasi/pending).
func groupAdminStatusLabel(isActive bool, suspendedAt *time.Time) string {
	if suspendedAt != nil {
		return "SUSPENDED"
	}
	if !isActive {
		return "TIDAK AKTIF"
	}
	return "AKTIF"
}

// groupAdminSummaryToMap -- bentuk JSON bersama List (S1-12) dan Get
// (S4P-06). optionalPtr helper kecil supaya *string/*int nil jadi null,
// bukan panic/zero-value yang menyesatkan.
func groupAdminSummaryToMap(s *repository.GroupAdminSummary) fiber.Map {
	m := fiber.Map{
		"id":                  s.ID,
		"email":               s.Email,
		"display_name":        s.DisplayName,
		"status":              groupAdminStatusLabel(s.IsActive, s.SuspendedAt),
		"created_at":          s.CreatedAt.UTC().Format(time.RFC3339),
		"group_id":            s.GroupID,
		"group_name":          s.GroupName,
		"job_title":           s.JobTitle,
		"address":             s.Address,
		"phone":               s.Phone,
		"tier":                s.Tier,
		"storage_quota_gb":    s.StorageQuotaGB,
		"tier_max_org":        s.TierMaxOrg,
		"tier_max_storage_gb": s.TierMaxStorage,
		"tier_max_members":    s.TierMaxMembers,
		"used_org_count":      s.UsedOrgCount,
		"used_storage_mb":     s.UsedStorageMB,
		"used_member_count":   s.UsedMemberCount,
	}
	return m
}

// Get menangani GET /platform/group-admins/:id -- mode "Lihat"/"Ubah"
// (S4P-06).
func (h *GroupAdminHandler) Get(c *fiber.Ctx) error {
	targetUserID := c.Params("id")
	if targetUserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "ID user wajib diisi", nil))
	}

	detail, err := h.accounts.GetGroupAdminDetail(c.Context(), targetUserID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrUserNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Group Admin tidak ditemukan", nil))
		default:
			h.logger.Error("gagal mengambil detail Group Admin", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil detail Group Admin", nil))
		}
	}

	return c.JSON(response.Success(groupAdminSummaryToMap(detail)))
}

type updateGroupAdminRequest struct {
	DisplayName    string `json:"display_name"`
	GroupName      string `json:"group_name"`
	JobTitle       string `json:"job_title"`
	Address        string `json:"address"`
	Phone          string `json:"phone"`
	Tier           string `json:"tier"`
	StorageQuotaGB *int   `json:"storage_quota_gb"`
	// Status -- "", "AKTIF", atau "SUSPENDED". "TIDAK AKTIF" tidak bisa
	// diset manual, lihat domain.ErrInvalidStatusTransition.
	Status string `json:"status"`
}

// Update menangani PUT /platform/group-admins/:id -- form "Ubah Group
// Admin" (S4P-06).
func (h *GroupAdminHandler) Update(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}

	targetUserID := c.Params("id")
	if targetUserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "ID user wajib diisi", nil))
	}

	var req updateGroupAdminRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.GroupName = strings.TrimSpace(req.GroupName)
	if req.DisplayName == "" || req.GroupName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "display_name dan group_name wajib diisi", nil))
	}

	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users",
			zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	oldTier, err := h.accounts.UpdateGroupAdmin(c.Context(), targetUserID, &repository.UpdateGroupAdminParams{
		DisplayName:    req.DisplayName,
		GroupName:      req.GroupName,
		JobTitle:       req.JobTitle,
		Address:        req.Address,
		Phone:          req.Phone,
		Tier:           req.Tier,
		StorageQuotaGB: req.StorageQuotaGB,
		NewStatus:      req.Status,
	}, actorUserID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "display_name dan group_name wajib diisi", nil))
		case errors.Is(err, domain.ErrInvalidTier):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("INVALID_TIER", "Tier tidak valid", nil))
		case errors.Is(err, domain.ErrInvalidStatusTransition):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("INVALID_STATUS_TRANSITION",
				`Status hanya bisa diubah ke "AKTIF" atau "SUSPENDED"`, nil))
		case errors.Is(err, domain.ErrUserNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Group Admin tidak ditemukan", nil))
		default:
			h.logger.Error("gagal mengubah data Group Admin", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengubah data Group Admin", nil))
		}
	}

	detail, err := h.accounts.GetGroupAdminDetail(c.Context(), targetUserID)
	if err != nil {
		h.logger.Error("Group Admin berhasil diubah tapi gagal membaca ulang detailnya", zap.Error(err))
		return c.JSON(response.Success(fiber.Map{"id": targetUserID}))
	}

	// S4P-09: email tier_changed, best-effort, cuma kalau tier benar-benar
	// berubah -- notifikasi in-app-nya sudah di-insert di repository
	// (tx yang sama dengan update) begitu tier terdeteksi berubah.
	if detail.Tier != nil && oldTier != "" && *detail.Tier != oldTier {
		if err := h.email.SendTierChangedEmail(c.Context(), detail.Email, detail.DisplayName, oldTier, *detail.Tier); err != nil {
			h.logger.Error("gagal mengirim email tier_changed", zap.String("user_id", targetUserID), zap.Error(err))
		}
	}

	return c.JSON(response.Success(groupAdminSummaryToMap(detail)))
}

// ListTiers menangani GET /platform/tiers (S4P-07) -- katalog tier untuk
// dropdown Tier + panel "Paket Tier (Otomatis)" di form Group Admin.
func (h *GroupAdminHandler) ListTiers(c *fiber.Ctx) error {
	tiers, err := h.accounts.ListServiceTiers(c.Context())
	if err != nil {
		h.logger.Error("gagal mengambil katalog tier", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil katalog tier", nil))
	}

	data := make([]fiber.Map, len(tiers))
	for i, t := range tiers {
		data[i] = fiber.Map{
			"name":               t.Name,
			"min_retention_days": t.MinRetentionDays,
			"max_retention_days": t.MaxRetentionDays,
			"webhook_rate":       t.WebhookRate,
			"sso_enabled":        t.SSOEnabled,
			"max_org":            t.MaxOrg,
			"max_storage_gb":     t.MaxStorageGB,
			"max_members":        t.MaxMembers,
		}
	}
	return c.JSON(response.Success(data))
}

// sendActivationEmail mengirim email aktivasi -- dipakai Create (S1-05) dan
// ResendActivation (S1-08). Gagal kirim TIDAK membatalkan aksi utama
// (ponytail: akun/token sudah terlanjur dibuat/diterbitkan; kegagalan
// dicatat sebagai error, bukan menggagalkan seluruh request).
func (h *GroupAdminHandler) sendActivationEmail(c *fiber.Ctx, result *service.GroupAdminInvitation, failureLogMsg string) {
	link := fmt.Sprintf("%s/activate?token=%s", h.appBaseURL, result.ActivationToken)
	if err := h.email.SendActivationEmail(c.Context(), result.Email, result.DisplayName, link, result.ExpiresAt); err != nil {
		h.logger.Error(failureLogMsg, zap.String("user_id", result.UserID), zap.Error(err))
	}
}
