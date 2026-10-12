// Package handler -- TimesheetReportHandler (menu "Timesheet & Analitik" PM,
// "PM Timesheet.dc.html", IG-128). Endpoint agregat per project + timesheet
// pribadi; aksi per-entri (approve/reject/edit) tetap di TimeEntryHandler.
package handler

import (
	"encoding/csv"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

type TimesheetReportHandler struct {
	report *service.TimesheetReportService
	logger *zap.Logger
}

func NewTimesheetReportHandler(report *service.TimesheetReportService, logger *zap.Logger) *TimesheetReportHandler {
	return &TimesheetReportHandler{report: report, logger: logger}
}

// Summary menangani GET /projects/:projectId/timesheet.
func (h *TimesheetReportHandler) Summary(c *fiber.Ctx) error {
	actorID, actorRole, exec, errResp := h.actorAndExec(c)
	if errResp != nil {
		return errResp
	}
	sum, err := h.report.Summary(c.Context(), exec, c.Params("projectId"), actorID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil timesheet project")
	}
	byStatus := make([]fiber.Map, len(sum.ByStatus))
	for i := range sum.ByStatus {
		b := &sum.ByStatus[i]
		byStatus[i] = fiber.Map{"name": b.Name, "color_token": b.ColorToken, "is_undefined": b.IsUndefined, "hours": b.Hours, "task_count": b.TaskCount}
	}
	tasks := make([]fiber.Map, len(sum.Tasks))
	for i := range sum.Tasks {
		t := &sum.Tasks[i]
		tasks[i] = fiber.Map{"task_code": t.Code, "title": t.Title, "status_name": t.StatusName, "estimated_hours": t.EstimatedHours, "logged_hours": t.LoggedHours}
	}
	members := make([]fiber.Map, len(sum.Members))
	for i := range sum.Members {
		m := &sum.Members[i]
		members[i] = fiber.Map{"user_id": m.UserID, "user_name": m.Name, "role": m.Role, "task_count": m.TaskCount, "hours": m.Hours, "active_count": m.ActiveCount}
	}
	return c.JSON(response.Success(fiber.Map{
		"project_name": sum.ProjectName, "logged_hours": sum.LoggedHours, "estimate_hours": sum.EstimateHours,
		"over_count": sum.OverCount, "over_codes": sum.OverCodes, "members_involved": sum.MembersInvolved,
		"pending_count": sum.PendingCount, "by_status": byStatus, "tasks": tasks, "members": members,
	}))
}

// Entries menangani GET /projects/:projectId/timesheet/entries?approval_status=.
func (h *TimesheetReportHandler) Entries(c *fiber.Ctx) error {
	actorID, actorRole, exec, errResp := h.actorAndExec(c)
	if errResp != nil {
		return errResp
	}
	list, err := h.report.Entries(c.Context(), exec, c.Params("projectId"), c.Query("approval_status"), actorID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil antrian persetujuan timesheet")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		e := &list[i]
		item := timeEntryJSON(&e.TimeEntry)
		item["task_code"] = e.TaskCode
		item["task_title"] = e.TaskTitle
		data[i] = item
	}
	return c.JSON(response.Success(fiber.Map{"items": data, "total": len(data)}))
}

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// Export menangani GET /projects/:projectId/timesheet/export (CSV).
func (h *TimesheetReportHandler) Export(c *fiber.Ctx) error {
	actorID, actorRole, exec, errResp := h.actorAndExec(c)
	if errResp != nil {
		return errResp
	}
	name, rows, err := h.report.Export(c.Context(), exec, c.Params("projectId"), actorID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengekspor timesheet project")
	}
	slug := strings.Trim(nonSlugChars.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" {
		slug = "project"
	}
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="timesheet-%s.csv"`, slug))

	w := csv.NewWriter(c.Response().BodyWriter())
	if err := w.Write([]string{"kode_task", "judul", "status", "pic_aktif", "estimasi_jam", "jam_tercatat", "selisih_jam"}); err != nil {
		return fmt.Errorf("handler.TimesheetExport: header: %w", err)
	}
	for i := range rows {
		r := &rows[i]
		diff := ""
		if r.EstimatedHours > 0 {
			diff = strconv.FormatFloat(r.LoggedHours-r.EstimatedHours, 'f', 2, 64)
		}
		if err := w.Write([]string{
			csvSafe(r.Code), csvSafe(r.Title), csvSafe(r.StatusName), csvSafe(strings.Join(r.ActivePICs, "; ")),
			strconv.FormatFloat(r.EstimatedHours, 'f', 2, 64), strconv.FormatFloat(r.LoggedHours, 'f', 2, 64), diff,
		}); err != nil {
			return fmt.Errorf("handler.TimesheetExport: row: %w", err)
		}
	}
	w.Flush()
	return w.Error()
}

// csvSafe -- netralkan formula injection spreadsheet: sel teks yang diawali
// = + - @ diberi apostrof di depan.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@", rune(v[0])) {
		return "'" + v
	}
	return v
}

// Mine menangani GET /me/timesheets?from=&to=.
func (h *TimesheetReportHandler) Mine(c *fiber.Ctx) error {
	actorID, _, exec, errResp := h.actorAndExec(c)
	if errResp != nil {
		return errResp
	}
	list, err := h.report.Mine(c.Context(), exec, actorID, c.Query("from"), c.Query("to"))
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil timesheet pribadi")
	}
	data := make([]fiber.Map, len(list))
	totalMinutes, pendingMinutes := 0, 0
	for i := range list {
		e := &list[i]
		item := timeEntryJSON(&e.TimeEntry)
		item["task_code"] = e.TaskCode
		item["task_title"] = e.TaskTitle
		item["project"] = fiber.Map{"project_id": e.ProjectID, "name": e.ProjectName}
		data[i] = item
		if e.DurationMinutes != nil && e.IsApproved != nil && *e.IsApproved {
			totalMinutes += *e.DurationMinutes
		}
		if e.DurationMinutes != nil && e.IsApproved == nil {
			pendingMinutes += *e.DurationMinutes
		}
	}
	return c.JSON(response.Success(fiber.Map{"items": data, "total": len(data), "approved_minutes": totalMinutes, "pending_minutes": pendingMinutes}))
}

func (h *TimesheetReportHandler) actorAndExec(c *fiber.Ctx) (actorID, actorRole string, exec db.Executor, errResp error) {
	actorID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return "", "", nil, c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok = middleware.DBTxFromContext(c)
	if !ok {
		return "", "", nil, c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	return actorID, actorRole, exec, nil
}

func (h *TimesheetReportHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Timesheet & Analitik hanya untuk PM project, Admin Workspace, atau Group Admin.", nil))
	case errors.Is(err, domain.ErrProjectNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Project tidak ditemukan", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
