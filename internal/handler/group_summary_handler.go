// Package handler -- GroupSummaryHandler (Dashboard Ringkasan/Landing Page
// GA, Track S4G S4G-29, desain "GA Ringkasan.dc.html").
package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

type GroupSummaryHandler struct {
	summary *service.GroupSummaryService
	logger  *zap.Logger
}

func NewGroupSummaryHandler(summary *service.GroupSummaryService, logger *zap.Logger) *GroupSummaryHandler {
	return &GroupSummaryHandler{summary: summary, logger: logger}
}

func orgToMap(o *repository.Organization) fiber.Map {
	return fiber.Map{
		"id":                  o.ID,
		"name":                o.Name,
		"member_count":        o.MemberCount,
		"workspace_count":     o.WorkspaceCount,
		"storage_quota_bytes": o.StorageQuotaBytes,
		"storage_used_bytes":  o.StorageUsedBytes,
		"deactivated_at":      o.DeactivatedAt,
	}
}

func retentionItemToMap(it *repository.RetentionScheduleItem) fiber.Map {
	return fiber.Map{
		"kind":       it.Kind,
		"item_id":    it.ItemID,
		"item_name":  it.ItemName,
		"org_name":   it.OrgName,
		"event_at":   it.EventAt,
		"total_days": it.TotalDays,
		"purge_at":   it.PurgeAt,
		"days_left":  it.DaysLeft,
	}
}

func pendingInviteToMap(p *repository.GroupPendingInvite) fiber.Map {
	return fiber.Map{
		"id":             p.ID,
		"email":          p.Email,
		"role":           p.Role,
		"workspace_name": p.WorkspaceName,
		"org_name":       p.OrgName,
		"is_executive":   p.IsExecutive,
		"created_at":     p.CreatedAt,
		"expires_at":     p.ExpiresAt,
	}
}

// Get menangani GET /groups/:groupId/summary.
func (h *GroupSummaryHandler) Get(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("GroupSummaryHandler.Get dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("GroupSummaryHandler.Get dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")

	result, err := h.summary.Get(c.Context(), exec, groupID, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil ringkasan grup")
	}

	activity := make([]fiber.Map, len(result.Activity))
	for i := range result.Activity {
		activity[i] = groupAuditEntryToMap(&result.Activity[i])
	}
	overQuota := make([]fiber.Map, len(result.OverQuotaOrgs))
	for i := range result.OverQuotaOrgs {
		overQuota[i] = orgToMap(&result.OverQuotaOrgs[i])
	}
	inactive := make([]fiber.Map, len(result.InactiveOrgs))
	for i := range result.InactiveOrgs {
		inactive[i] = orgToMap(&result.InactiveOrgs[i])
	}
	distribution := make([]fiber.Map, len(result.OrgDistribution))
	for i := range result.OrgDistribution {
		distribution[i] = orgToMap(&result.OrgDistribution[i])
	}
	retentionSoon := make([]fiber.Map, len(result.RetentionSoon))
	for i := range result.RetentionSoon {
		retentionSoon[i] = retentionItemToMap(&result.RetentionSoon[i])
	}
	pendingInvites := make([]fiber.Map, len(result.PendingInvites))
	for i := range result.PendingInvites {
		pendingInvites[i] = pendingInviteToMap(&result.PendingInvites[i])
	}

	return c.JSON(response.Success(fiber.Map{
		"stats": fiber.Map{
			"org_total":             result.OrgTotal,
			"org_active":            result.OrgActive,
			"org_inactive":          result.OrgInactive,
			"workspace_total":       result.WorkspaceTotal,
			"member_total":          result.MemberTotal,
			"pending_invites_total": result.PendingInvitesTotal,
			"quota_allocated_bytes": result.QuotaAllocatedBytes,
			"quota_ceiling_bytes":   result.QuotaCeilingBytes,
			"storage_used_bytes":    result.StorageUsedBytes,
		},
		"activity": activity,
		"todos": fiber.Map{
			"over_quota_orgs": overQuota,
			"retention_soon":  retentionSoon,
			"pending_invites": pendingInvites,
			"inactive_orgs":   inactive,
		},
		"org_distribution": distribution,
	}))
}

func (h *GroupSummaryHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas grup ini.", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
