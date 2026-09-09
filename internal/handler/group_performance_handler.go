// Package handler -- GroupPerformanceHandler (Performance Dashboard Lintas
// Organisasi, Track S4G, desain "GA Kinerja Grup.dc.html", US-079/S4G-25).
package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

type GroupPerformanceHandler struct {
	performance *service.GroupPerformanceService
	logger      *zap.Logger
}

func NewGroupPerformanceHandler(performance *service.GroupPerformanceService, logger *zap.Logger) *GroupPerformanceHandler {
	return &GroupPerformanceHandler{performance: performance, logger: logger}
}

// Summary menangani GET /groups/:groupId/performance -- query params
// `org_id` (opsional, "" = semua organisasi aktif) dan `range` dalam hari
// (opsional, 0/absen = SEMUA, sesuai chip RENTANG desain "7 HARI"/"30
// HARI"/"SEMUA").
func (h *GroupPerformanceHandler) Summary(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("GroupPerformanceHandler.Summary dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("GroupPerformanceHandler.Summary dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")
	orgID := c.Query("org_id")
	rangeDays := c.QueryInt("range", 30)

	result, err := h.performance.Summary(c.Context(), exec, groupID, orgID, rangeDays, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil ringkasan kinerja grup")
	}

	orgs := make([]fiber.Map, len(result.Organizations))
	for i := range result.Organizations {
		orgs[i] = orgPerformanceToMap(&result.Organizations[i])
	}
	return c.JSON(response.Success(fiber.Map{
		"summary": fiber.Map{
			"completion_rate_raw":      result.CompletionRateRaw,
			"completion_rate_weighted": result.CompletionRateWeighted,
			"total_tasks":              result.TotalTasks,
			"overdue_count":            result.OverdueCount,
			"overdue_critical_count":   result.OverdueCriticalCount,
			"bottleneck_status_name":   result.BottleneckStatusName,
			"bottleneck_org_name":      result.BottleneckOrgName,
			"bottleneck_avg_days":      result.BottleneckAvgDays,
		},
		"organizations": orgs,
	}))
}

func orgPerformanceToMap(o *service.OrgPerformance) fiber.Map {
	bottleneck := make([]fiber.Map, len(o.Bottleneck))
	for i, b := range o.Bottleneck {
		bottleneck[i] = fiber.Map{"status_name": b.StatusName, "avg_days": b.AvgDays}
	}
	return fiber.Map{
		"organization_id":          o.OrgID,
		"organization_name":        o.OrgName,
		"workspace_count":          o.WorkspaceCount,
		"total_tasks":              o.TotalTasks,
		"completion_rate_raw":      o.CompletionRateRaw,
		"completion_rate_weighted": o.CompletionRateWeighted,
		"on_time_rate":             o.OnTimeRateByPriority,
		"overdue_count":            o.OverdueCount,
		"overdue_critical_count":   o.OverdueCriticalCount,
		"bottleneck":               bottleneck,
	}
}

func (h *GroupPerformanceHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas grup ini.", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
