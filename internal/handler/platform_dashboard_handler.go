package handler

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// PlatformDashboardHandler -- S4P-24/25/26, US-072: KPI, tren, dan
// deteksi anomali untuk panel "Dashboard Kesehatan Platform".
type PlatformDashboardHandler struct {
	dashboard *service.PlatformDashboardService
	logger    *zap.Logger
}

func NewPlatformDashboardHandler(dashboard *service.PlatformDashboardService, logger *zap.Logger) *PlatformDashboardHandler {
	return &PlatformDashboardHandler{dashboard: dashboard, logger: logger}
}

// HealthMetrics menangani GET /platform/health-metrics (S4P-24).
func (h *PlatformDashboardHandler) HealthMetrics(c *fiber.Ctx) error {
	m, err := h.dashboard.HealthMetrics(c.Context())
	if err != nil {
		h.logger.Error("gagal mengambil health metrics", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil health metrics", nil))
	}
	return c.JSON(response.Success(fiber.Map{
		"active_ga_count":          m.ActiveGACount,
		"active_org_count":         m.ActiveOrgCount,
		"total_storage_used_bytes": m.TotalStorageUsedByte,
		"tier_distribution":        m.TierDistribution,
	}))
}

// Trends menangani GET /platform/trends?period=7|30|90 (S4P-25).
func (h *PlatformDashboardHandler) Trends(c *fiber.Ctx) error {
	period := c.QueryInt("period", 30)
	if period != 7 && period != 30 && period != 90 {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "period harus 7, 30, atau 90", nil))
	}

	points, err := h.dashboard.Trends(c.Context(), period)
	if err != nil {
		h.logger.Error("gagal mengambil trends", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil trends", nil))
	}

	data := make([]fiber.Map, len(points))
	for i, p := range points {
		data[i] = fiber.Map{
			"date":          p.Date.Format("2006-01-02"),
			"new_ga_count":  p.NewGACount,
			"new_org_count": p.NewOrgCount,
		}
	}
	return c.JSON(response.Success(data))
}

// Anomalies menangani GET /platform/anomalies?period=7|30|90 (S4P-26).
// Endpoint awalnya tidak disebut literal di AC sprint_backlog.md (yang
// cuma menyebut "Service AnomalyDetector") tapi dibutuhkan supaya FE
// (S4P-27, tabel alert) punya cara membaca hasilnya. Param `period`
// ditambah 2026-08-29 atas permintaan user: jendela simetris [-N,+N]
// hari untuk anomali kontrak berakhir, supaya kontrak yang sudah lama
// kedaluwarsa tidak menumpuk selamanya di alert.
func (h *PlatformDashboardHandler) Anomalies(c *fiber.Ctx) error {
	period := c.QueryInt("period", 7)
	if period != 7 && period != 30 && period != 90 {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "period harus 7, 30, atau 90", nil))
	}

	a, err := h.dashboard.Anomalies(c.Context(), period)
	if err != nil {
		h.logger.Error("gagal mengambil anomalies", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil anomalies", nil))
	}

	storage := make([]fiber.Map, len(a.Storage))
	for i, s := range a.Storage {
		storage[i] = fiber.Map{
			"group_id":   s.GroupID,
			"group_name": s.GroupName,
			"used_mb":    s.UsedMB,
			"quota_gb":   s.QuotaGB,
			"severity":   s.Severity,
		}
	}
	contractEnd := make([]fiber.Map, len(a.ContractEnd))
	for i, ce := range a.ContractEnd {
		contractEnd[i] = fiber.Map{
			"org_id":          ce.OrgID,
			"org_name":        ce.OrgName,
			"group_name":      ce.GroupName,
			"contract_end_at": ce.ContractEndAt.UTC().Format(time.RFC3339),
		}
	}
	return c.JSON(response.Success(fiber.Map{
		"storage":      storage,
		"contract_end": contractEnd,
	}))
}
