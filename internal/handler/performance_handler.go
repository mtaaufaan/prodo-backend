// Package handler -- PerformanceHandler (EPIC 12 Reporting & Analytics,
// US-075/076/077/078, desain "Performance Dashboard.dc.html").
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

type PerformanceHandler struct {
	performance *service.PerformanceService
	logger      *zap.Logger
}

func NewPerformanceHandler(performance *service.PerformanceService, logger *zap.Logger) *PerformanceHandler {
	return &PerformanceHandler{performance: performance, logger: logger}
}

// ForWorkspace menangani GET /workspaces/:wsId/performance (AW-only, gate
// RequireRole di route). Query params: `project_id` (opsional, "" = agregat
// lintas seluruh project workspace) dan `range` dalam hari (0/absen =
// SEMUA, sama pola GroupPerformanceHandler).
func (h *PerformanceHandler) ForWorkspace(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("PerformanceHandler.ForWorkspace dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("PerformanceHandler.ForWorkspace dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")
	projectID := c.Query("project_id")
	rangeDays := c.QueryInt("range", 30)

	result, err := h.performance.DashboardForWorkspace(c.Context(), exec, workspaceID, projectID, rangeDays, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil Performance Dashboard workspace")
	}
	return c.JSON(response.Success(dashboardToMap(result)))
}

// ForProject menangani GET /projects/:projectId/performance (PM pemilik
// project ini, atau Full mode -- gerbang di service, bukan route, karena
// bergantung data project itu sendiri).
func (h *PerformanceHandler) ForProject(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("PerformanceHandler.ForProject dipanggil tanpa jwtAuth -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("PerformanceHandler.ForProject dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("projectId")
	rangeDays := c.QueryInt("range", 30)

	result, err := h.performance.DashboardForProject(c.Context(), exec, projectID, rangeDays, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil Performance Dashboard project")
	}
	return c.JSON(response.Success(dashboardToMap(result)))
}

func dashboardToMap(d *service.DashboardResult) fiber.Map {
	onTime := make([]fiber.Map, len(d.OnTime))
	for i, o := range d.OnTime {
		onTime[i] = fiber.Map{"priority": o.Priority, "done_with_due": o.DoneWithDue, "on_time": o.OnTime, "rate_pct": o.RatePct}
	}
	backlog := make([]fiber.Map, len(d.BacklogHealth))
	for i, b := range d.BacklogHealth {
		backlog[i] = fiber.Map{"priority": b.Priority, "count": b.Count}
	}
	cycle := make([]fiber.Map, len(d.Cycle))
	for i, c := range d.Cycle {
		cycle[i] = fiber.Map{"status_name": c.StatusName, "avg_by_priority": c.AvgByPriority, "avg_hours": c.AvgHours}
	}
	overdue := make([]fiber.Map, len(d.Overdue))
	for i, t := range d.Overdue {
		overdue[i] = fiber.Map{
			"task_code": t.TaskCode, "title": t.Title, "project_name": t.ProjectName,
			"priority": t.Priority, "due_date": t.DueDate, "days_late": t.DaysLate,
		}
	}
	members := make([]fiber.Map, len(d.Members))
	for i, m := range d.Members {
		members[i] = fiber.Map{
			"user_id": m.UserID, "user_name": m.UserName, "active_by_priority": m.ActiveByPriority,
			"total_count": m.TotalCount, "done_count": m.DoneCount,
			"completion_rate_raw": m.CompletionRateRaw, "completion_rate_weighted": m.CompletionRateWeighted,
			"avg_completion_hours": m.AvgCompletionHours, "ack_avg_hours": m.AckAvgHours, "ack_pending_count": m.AckPendingCount,
		}
	}
	regByStatus := make([]fiber.Map, len(d.RegressionByStatus))
	for i, r := range d.RegressionByStatus {
		regByStatus[i] = fiber.Map{
			"status_name": r.StatusName, "regressions": r.Regressions, "sessions": r.Sessions,
			"tasks_through": r.TasksThrough, "rate_pct": r.RatePct,
		}
	}
	bottleneck := make([]fiber.Map, len(d.Bottleneck))
	for i, b := range d.Bottleneck {
		bottleneck[i] = fiber.Map{
			"status_name": b.StatusName, "avg_total_hours": b.AvgTotalHours, "avg_queue_hours": b.AvgQueueHours,
			"avg_active_hours": b.AvgActiveHours, "session_count": b.SessionCount,
		}
	}
	handoff := make([]fiber.Map, len(d.HandoffDelay))
	for i, h := range d.HandoffDelay {
		handoff[i] = fiber.Map{
			"project_id": h.ProjectID, "project_name": h.ProjectName, "avg_hours": h.AvgHours, "pending_count": h.PendingCount,
		}
	}

	return fiber.Map{
		"scope_total": d.ScopeTotal, "scope_done": d.ScopeDone,
		"completion_rate_raw": d.CompletionRateRaw, "completion_rate_weighted": d.CompletionRateWeighted,
		"on_time": onTime, "no_due_count": d.NoDueCount,
		"backlog_health": backlog,
		"cycle":          cycle,
		"overdue":        overdue, "overdue_total": d.OverdueTotal, "overdue_critical": d.OverdueCritical,
		"members":              members,
		"flow_efficiency_pct":  d.FlowEfficiencyPct,
		"lead_time_hours":      d.LeadTimeHours,
		"cycle_time_avg_hours": d.CycleTimeAvgHours,
		"regression_rate_pct":  d.RegressionRatePct,
		"regressed_tasks":      d.RegressedTasks,
		"regression_by_status": regByStatus,
		"bottleneck":           bottleneck,
		"handoff_delay":        handoff,
	}
}

func (h *PerformanceHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Performance Dashboard tidak tersedia untuk role ini.", nil))
	case errors.Is(err, domain.ErrProjectNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Project tidak ditemukan", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
