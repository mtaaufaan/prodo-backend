package handler

import (
	"errors"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// MyTimesheetHandler -- GET /me/timesheets mode workspace (Timesheet Editor, S6E-10, IG-141). Mode lama
// (`from`/`to`, tab "Timesheet Saya" PM) tetap dilayani TimesheetReportHandler.Mine; pemilihan mode ada di routing.
type MyTimesheetHandler struct {
	svc    *service.MyTimesheetService
	logger *zap.Logger
}

func NewMyTimesheetHandler(svc *service.MyTimesheetService, logger *zap.Logger) *MyTimesheetHandler {
	return &MyTimesheetHandler{svc: svc, logger: logger}
}

// Get menangani GET /me/timesheets?workspace_id=&view=week|history&page=&per_page=.
func (h *MyTimesheetHandler) Get(c *fiber.Ctx) error {
	actorID, _, ok := middleware.ActorFromContext(c)
	exec, txOK := middleware.DBTxFromContext(c)
	if !ok || !txOK {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	view := c.Query("view", "week")
	page, _ := strconv.Atoi(c.Query("page", "1"))
	perPage, _ := strconv.Atoi(c.Query("per_page", "10"))
	res, err := h.svc.Get(c.Context(), exec, actorID, c.Query("workspace_id"), view, page, perPage, time.Now())
	if err != nil {
		if errors.Is(err, domain.ErrInvalidInput) {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "workspace_id wajib dan view harus week atau history.", nil))
		}
		h.logger.Error("Gagal mengambil timesheet saya", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil timesheet saya", nil))
	}
	tasks := make([]fiber.Map, len(res.Tasks))
	for i := range res.Tasks {
		t := &res.Tasks[i]
		tasks[i] = fiber.Map{
			"task_id": t.TaskID, "task_code": t.TaskCode, "title": t.Title, "project_id": t.ProjectID, "project_name": t.ProjectName,
			"status_name": t.StatusName, "estimate_hours": t.EstimateHours, "logged_hours": t.LoggedHours, "week_hours": t.WeekHours, "is_active": t.IsActive,
		}
	}
	items := make([]fiber.Map, len(res.Items))
	for i := range res.Items {
		e := &res.Items[i]
		items[i] = fiber.Map{
			"id": e.ID, "task_id": e.TaskID, "task_code": e.TaskCode, "task_title": e.TaskTitle, "project_name": e.ProjectName,
			"entry_date": e.EntryDate, "hours": e.Hours, "note": e.Note, "is_estimate": e.IsEstimate,
		}
	}
	return c.JSON(response.Success(fiber.Map{
		"stats": fiber.Map{
			"logged_hours": res.Stats.LoggedHours, "estimate_hours": res.Stats.EstimateHours,
			"over_count": res.Stats.OverCount, "active_count": res.Stats.ActiveCount,
		},
		"tasks": tasks, "items": items, "total": res.Total,
	}))
}
