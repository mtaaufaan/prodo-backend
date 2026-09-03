package handler

import (
	"context"
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
	// JobTitle/Address/Phone/TierID/StorageQuotaGB -- S4P-06/07, field
	// lengkap sesuai desain "PA Group Admin Form". Semua opsional kecuali
	// yang di atas -- TierID default tier "starter" kalau kosong. TierID
	// (bukan nama, S4P-11) -- diambil dari GET /platform/tiers.
	JobTitle       string `json:"job_title"`
	Address        string `json:"address"`
	Phone          string `json:"phone"`
	TierID         string `json:"tier_id"`
	StorageQuotaGB *int   `json:"storage_quota_gb"`

	// Kontrak awal (dikonfirmasi user 2026-08-29) -- OPSIONAL. ContractStartAt
	// kosong berarti grup dibuat tanpa kontrak dulu (bisa ditambah belakangan
	// lewat POST .../renew-contract). Format tanggal "2006-01-02" (date-only,
	// sama pola dengan filter GET /platform/audit-logs).
	ContractStartAt            string `json:"contract_start_at"`
	ContractSubscriptionPeriod string `json:"contract_subscription_period"`
	ContractInvoiceNumber      string `json:"contract_invoice_number"`
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
	var contractStartAt *time.Time
	if req.ContractStartAt != "" {
		t, parseErr := time.Parse("2006-01-02", req.ContractStartAt)
		if parseErr != nil {
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "format contract_start_at tidak valid (YYYY-MM-DD)", nil))
		}
		contractStartAt = &t
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
		TierID:          req.TierID,
		StorageQuotaGB:  req.StorageQuotaGB,
		InvitedByUserID: actorUserID,

		ContractStartAt:            contractStartAt,
		ContractSubscriptionPeriod: req.ContractSubscriptionPeriod,
		ContractInvoiceNumber:      req.ContractInvoiceNumber,
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrEmailAlreadyExists):
			return c.Status(fiber.StatusConflict).JSON(response.Error("CONFLICT", "Email sudah terdaftar", nil))
		case errors.Is(err, domain.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "email, display_name, dan group_name wajib diisi", nil))
		case errors.Is(err, domain.ErrInvalidTier):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("INVALID_TIER", "Tier tidak valid", nil))
		case errors.Is(err, domain.ErrInvalidSubscriptionPeriod):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("INVALID_SUBSCRIPTION_PERIOD", "Masa langganan harus monthly, quarterly, atau yearly", nil))
		default:
			h.logger.Error("gagal membuat akun Group Admin", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal membuat akun Group Admin", nil))
		}
	}

	// S4G-33, Track S4G: LinkedExistingAccount berarti tidak ada invitation
	// baru sama sekali (akun existing ditautkan ke grup baru) -- tidak ada
	// email aktivasi untuk dikirim.
	if !result.LinkedExistingAccount {
		h.sendActivationEmail(c, result, "akun Group Admin dibuat tapi gagal kirim email aktivasi")
	}

	resp := fiber.Map{
		"id":                    result.UserID,
		"email":                 result.Email,
		"display_name":          result.DisplayName,
		"linked_existing_admin": result.LinkedExistingAccount,
	}
	if !result.LinkedExistingAccount {
		resp["expires_at"] = result.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(resp))
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
	// GroupID (S4G-33, Track S4G) -- WAJIB, grup SPESIFIK yang dipindahkan.
	// Sebelumnya endpoint ini memindahkan SEMUA grup milik :id sekaligus;
	// sejak satu GA bisa mengelola >1 grup (DATABASE_SCHEMA.md §5.6),
	// caller (baris panel PA yang di-klik "Pindahkan") wajib menyebut grup
	// mana secara eksplisit -- baris lain milik GA yang sama tidak ikut.
	GroupID string `json:"group_id"`
}

// Transfer menangani POST /platform/group-admins/:id/transfer (S4P-03/04,
// IG-21, di-scope per grup sejak S4G-33) -- pindahkan pengelolaan SATU
// grup (group_id di body) dari :id ke to_user_id.
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
	req.GroupID = strings.TrimSpace(req.GroupID)
	if req.ToUserID == "" || req.GroupID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "to_user_id dan group_id wajib diisi", nil))
	}

	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users",
			zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	if err := h.accounts.TransferGroup(c.Context(), req.GroupID, fromUserID, req.ToUserID, actorUserID); err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidTransferTarget):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("INVALID_TRANSFER_TARGET",
				"Target transfer bukan akun Group Admin yang valid", nil))
		case errors.Is(err, domain.ErrGroupAdminAssignmentNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND",
				"Group Admin ini tidak mengelola grup tersebut", nil))
		default:
			h.logger.Error("gagal transfer grup", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal transfer grup", nil))
		}
	}

	return c.JSON(response.Success(fiber.Map{
		"from_user_id": fromUserID,
		"to_user_id":   req.ToUserID,
		"group_id":     req.GroupID,
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
		"tier_id":             s.TierID,
		"tier":                s.Tier,
		"storage_quota_gb":    s.StorageQuotaGB,
		"tier_max_org":        s.TierMaxOrg,
		"tier_max_storage_gb": s.TierMaxStorage,
		"tier_max_members":    s.TierMaxMembers,
		"used_org_count":      s.UsedOrgCount,
		"used_storage_mb":     s.UsedStorageMB,
		"used_member_count":   s.UsedMemberCount,
		"contract_start_at":   formatOptionalDate(s.ContractStartAt),
		"subscription_period": s.SubscriptionPeriod,
		"contract_end_at":     formatOptionalDate(s.ContractEndAt),
		"invoice_number":      s.ContractInvoiceNum,
	}
	return m
}

// formatOptionalDate -- kontrak grup (dikonfirmasi user 2026-08-29): nil
// jadi null JSON, bukan panic/zero-value yang menyesatkan (pola sama
// dengan optionalPtr yang disebut komentar di atas untuk *string/*int).
func formatOptionalDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// Get menangani GET /platform/group-admins/:id?group_id=X -- mode
// "Lihat"/"Ubah" (S4P-06). group_id (S4G-33) WAJIB kalau baris panel yang
// diklik punya grup (kosong/tidak diberikan berarti baris 0-grup) --
// satu baris panel PA sekarang = satu grup, bukan "grup pertama" GA lagi.
func (h *GroupAdminHandler) Get(c *fiber.Ctx) error {
	targetUserID := c.Params("id")
	if targetUserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "ID user wajib diisi", nil))
	}

	detail, err := h.accounts.GetGroupAdminDetail(c.Context(), targetUserID, c.Query("group_id"))
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
	// GroupID (S4G-33) -- WAJIB, grup SPESIFIK yang diubah. Sebelumnya
	// diresolusi diam-diam ke "grup pertama" GA itu.
	GroupID        string `json:"group_id"`
	DisplayName    string `json:"display_name"`
	GroupName      string `json:"group_name"`
	JobTitle       string `json:"job_title"`
	Address        string `json:"address"`
	Phone          string `json:"phone"`
	TierID         string `json:"tier_id"`
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
	req.GroupID = strings.TrimSpace(req.GroupID)
	if req.DisplayName == "" || req.GroupName == "" || req.GroupID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "group_id, display_name, dan group_name wajib diisi", nil))
	}

	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users",
			zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	oldTier, err := h.accounts.UpdateGroupAdmin(c.Context(), targetUserID, req.GroupID, &repository.UpdateGroupAdminParams{
		DisplayName:    req.DisplayName,
		GroupName:      req.GroupName,
		JobTitle:       req.JobTitle,
		Address:        req.Address,
		Phone:          req.Phone,
		TierID:         req.TierID,
		StorageQuotaGB: req.StorageQuotaGB,
		NewStatus:      req.Status,
	}, actorUserID)
	if err != nil {
		var quotaErr *domain.StorageQuotaBelowUsageError
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
		case errors.Is(err, domain.ErrGroupAdminAssignmentNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Group Admin ini tidak mengelola grup tersebut", nil))
		case errors.As(err, &quotaErr):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("STORAGE_QUOTA_BELOW_USAGE",
				fmt.Sprintf("Plafon minimal %d GB — grup ini sudah memakai %d GB. Turunkan alokasi organisasinya terlebih dahulu.", quotaErr.MinimumGB, quotaErr.MinimumGB),
				fiber.Map{"minimum_gb": quotaErr.MinimumGB}))
		default:
			h.logger.Error("gagal mengubah data Group Admin", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengubah data Group Admin", nil))
		}
	}

	detail, err := h.accounts.GetGroupAdminDetail(c.Context(), targetUserID, req.GroupID)
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

type renewGroupContractRequest struct {
	// GroupID (S4G-33) -- WAJIB, grup SPESIFIK yang diperpanjang kontraknya.
	GroupID            string `json:"group_id"`
	StartAt            string `json:"start_at"`
	SubscriptionPeriod string `json:"subscription_period"`
	InvoiceNumber      string `json:"invoice_number"`
}

// RenewContract menangani POST /platform/group-admins/:id/renew-contract
// (dikonfirmasi user 2026-08-29) -- kontrak PERTAMA (grup belum pernah
// punya baris group_contracts) maupun PERPANJANGAN, service/repository
// yang membedakan audit action-nya.
func (h *GroupAdminHandler) RenewContract(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}

	targetUserID := c.Params("id")
	if targetUserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "ID user wajib diisi", nil))
	}

	var req renewGroupContractRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	req.GroupID = strings.TrimSpace(req.GroupID)
	if req.GroupID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "group_id wajib diisi", nil))
	}
	startAt, err := time.Parse("2006-01-02", req.StartAt)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "format start_at tidak valid (YYYY-MM-DD)", nil))
	}

	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users",
			zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}

	endAt, err := h.accounts.RenewGroupContract(c.Context(), targetUserID, req.GroupID, startAt, req.SubscriptionPeriod, req.InvoiceNumber, actorUserID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidSubscriptionPeriod):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("INVALID_SUBSCRIPTION_PERIOD", "Masa langganan harus monthly, quarterly, atau yearly", nil))
		case errors.Is(err, domain.ErrUserNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Group Admin tidak ditemukan", nil))
		case errors.Is(err, domain.ErrGroupAdminAssignmentNotFound):
			return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Group Admin ini tidak mengelola grup tersebut", nil))
		default:
			h.logger.Error("gagal memperpanjang kontrak grup", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal memperpanjang kontrak", nil))
		}
	}

	return c.JSON(response.Success(fiber.Map{"contract_end_at": endAt.UTC().Format(time.RFC3339)}))
}

// tierToMap -- bentuk JSON bersama ListTiers/CreateTier/UpdateTier (S4P-07/11).
func tierToMap(t *repository.ServiceTier) fiber.Map {
	return fiber.Map{
		"id":                 t.ID,
		"name":               t.Name,
		"min_retention_days": t.MinRetentionDays,
		"max_retention_days": t.MaxRetentionDays,
		"webhook_rate":       t.WebhookRate,
		"sso_enabled":        t.SSOEnabled,
		"max_org":            t.MaxOrg,
		"max_storage_gb":     t.MaxStorageGB,
		"max_members":        t.MaxMembers,
		"is_custom":          t.IsCustom,
		"deactivated_at":     t.DeactivatedAt,
		"archived_at":        t.ArchivedAt,
	}
}

// ListTiers menangani GET /platform/tiers (S4P-07/11) -- default
// (?all tidak diisi) cuma tier assignable, dipakai dropdown Tier di form
// Group Admin. ?all=true mengembalikan SEMUA tier termasuk nonaktif/
// archived, dipakai halaman admin "Tier & Kuota Global".
func (h *GroupAdminHandler) ListTiers(c *fiber.Ctx) error {
	tiers, err := h.accounts.ListServiceTiers(c.Context(), c.QueryBool("all", false))
	if err != nil {
		h.logger.Error("gagal mengambil katalog tier", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil katalog tier", nil))
	}

	data := make([]fiber.Map, len(tiers))
	for i := range tiers {
		data[i] = tierToMap(&tiers[i])
	}
	return c.JSON(response.Success(data))
}

type serviceTierRequest struct {
	Name             string `json:"name"`
	MinRetentionDays int    `json:"min_retention_days"`
	MaxRetentionDays int    `json:"max_retention_days"`
	WebhookRate      int    `json:"webhook_rate"`
	SSOEnabled       bool   `json:"sso_enabled"`
	MaxOrg           int    `json:"max_org"`
	MaxStorageGB     int    `json:"max_storage_gb"`
	MaxMembers       int    `json:"max_members"`
}

func (r *serviceTierRequest) toParams() *repository.ServiceTierParams {
	return &repository.ServiceTierParams{
		Name:             strings.TrimSpace(r.Name),
		MinRetentionDays: r.MinRetentionDays,
		MaxRetentionDays: r.MaxRetentionDays,
		WebhookRate:      r.WebhookRate,
		SSOEnabled:       r.SSOEnabled,
		MaxOrg:           r.MaxOrg,
		MaxStorageGB:     r.MaxStorageGB,
		MaxMembers:       r.MaxMembers,
	}
}

func mapTierError(c *fiber.Ctx, err error, logger *zap.Logger, fallbackMsg string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR",
			"Nama wajib diisi, semua batas numerik harus > 0, retensi minimum >= 30 hari, retensi maksimum <= 3650 hari dan >= retensi minimum", nil))
	case errors.Is(err, domain.ErrTierNameAlreadyExists):
		return c.Status(fiber.StatusConflict).JSON(response.Error("TIER_NAME_ALREADY_EXISTS", "Nama tier sudah dipakai", nil))
	case errors.Is(err, domain.ErrTierNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Tier tidak ditemukan", nil))
	case errors.Is(err, domain.ErrTierInUse):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("TIER_IN_USE",
			"Tier masih dipakai satu atau lebih grup -- archive dulu dan pindahkan grup yang memakainya", nil))
	case errors.Is(err, domain.ErrTierNotDeletable):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("TIER_NOT_DELETABLE",
			"Tier standar tidak bisa dihapus, cuma bisa dinonaktifkan/di-archive", nil))
	default:
		logger.Error(fallbackMsg, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMsg, nil))
	}
}

// CreateTier menangani POST /platform/tiers (S4P-11) -- tambah tier custom
// baru ke katalog.
func (h *GroupAdminHandler) CreateTier(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	var req serviceTierRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users", zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}
	id, err := h.accounts.CreateServiceTier(c.Context(), req.toParams(), actorUserID)
	if err != nil {
		return mapTierError(c, err, h.logger, "Gagal menambah tier")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{"id": id}))
}

// UpdateTier menangani PUT /platform/tiers/:id (S4P-11) -- ubah definisi
// tier, termasuk rename.
func (h *GroupAdminHandler) UpdateTier(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	tierID := c.Params("id")
	var req serviceTierRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users", zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}
	if err := h.accounts.UpdateServiceTier(c.Context(), tierID, req.toParams(), actorUserID); err != nil {
		return mapTierError(c, err, h.logger, "Gagal mengubah tier")
	}
	return c.JSON(response.Success(fiber.Map{"id": tierID}))
}

// tierLifecycleAction -- logika bersama 4 endpoint lifecycle tier
// (nonaktifkan/aktifkan/archive/pulihkan, S4P-11): resolve actor lalu
// panggil repo call yang sesuai.
func (h *GroupAdminHandler) tierLifecycleAction(c *fiber.Ctx, action func(ctx context.Context, id, actorUserID string) error, fallbackMsg string) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	tierID := c.Params("id")
	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users", zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}
	if err := action(c.Context(), tierID, actorUserID); err != nil {
		return mapTierError(c, err, h.logger, fallbackMsg)
	}
	return c.JSON(response.Success(fiber.Map{"id": tierID}))
}

// DeactivateTier/ReactivateTier menangani PUT /platform/tiers/:id/deactivate
// dan /reactivate (S4P-11).
func (h *GroupAdminHandler) DeactivateTier(c *fiber.Ctx) error {
	return h.tierLifecycleAction(c, h.accounts.DeactivateServiceTier, "Gagal menonaktifkan tier")
}

func (h *GroupAdminHandler) ReactivateTier(c *fiber.Ctx) error {
	return h.tierLifecycleAction(c, h.accounts.ReactivateServiceTier, "Gagal mengaktifkan tier")
}

// ArchiveTier/UnarchiveTier menangani PUT /platform/tiers/:id/archive dan
// /unarchive (S4P-11).
func (h *GroupAdminHandler) ArchiveTier(c *fiber.Ctx) error {
	return h.tierLifecycleAction(c, h.accounts.ArchiveServiceTier, "Gagal meng-archive tier")
}

func (h *GroupAdminHandler) UnarchiveTier(c *fiber.Ctx) error {
	return h.tierLifecycleAction(c, h.accounts.UnarchiveServiceTier, "Gagal memulihkan tier dari arsip")
}

// DeleteTier menangani DELETE /platform/tiers/:id (S4P-11) -- HANYA tier
// custom yang sudah tidak dipakai grup manapun.
func (h *GroupAdminHandler) DeleteTier(c *fiber.Ctx) error {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	tierID := c.Params("id")
	actorUserID, err := h.accounts.ResolveActorUserID(c.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("Platform Admin JWT valid tapi tidak ditemukan di tabel users", zap.String("keycloak_sub", claims.Subject), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi Platform Admin", nil))
	}
	if err := h.accounts.DeleteServiceTier(c.Context(), tierID, actorUserID); err != nil {
		return mapTierError(c, err, h.logger, "Gagal menghapus tier")
	}
	return c.SendStatus(fiber.StatusNoContent)
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
