package handler

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// ProjectAuditHandler -- Audit Trail PM (IG-130). Bentuk respons IDENTIK
// WorkspaceAuditHandler supaya FE memakai komponen yang sama; hanya cakupan
// (project) dan gerbang akses (PM project / AW / GA / PA, di service) yang beda.
type ProjectAuditHandler struct {
	audit  *service.ProjectAuditService
	logger *zap.Logger
}

func NewProjectAuditHandler(audit *service.ProjectAuditService, logger *zap.Logger) *ProjectAuditHandler {
	return &ProjectAuditHandler{audit: audit, logger: logger}
}

// List menangani GET /projects/:projectId/audit-logs (filter actor_id/action_type/
// days, paginasi, atau ?export=csv).
func (h *ProjectAuditHandler) List(c *fiber.Ctx) error {
	actorUserID, actorRole, exec, errResp := auditActorAndExec(c, h.logger, "ProjectAuditHandler.List")
	if errResp != nil {
		return errResp
	}
	projectID := c.Params("projectId")

	actionType := c.Query("action_type")
	if !validActionTypes[actionType] {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "action_type harus salah satu dari CREATE/UPDATE/DELETE/ACCESS", nil))
	}
	actorFilterID := c.Query("actor_id")
	days := c.QueryInt("days", 30)

	if c.Query("export") == "csv" {
		entries, err := h.audit.ExportCSV(c.Context(), exec, projectID, actorUserID, actorFilterID, actionType, days, actorRole)
		if err != nil {
			return h.mapError(c, err, "Gagal mengekspor audit trail project")
		}
		return writeWorkspaceAuditCSV(c, entries, "audit-trail-project.csv")
	}

	page := c.QueryInt("page", 1)
	if page < 1 {
		page = 1
	}
	perPage := c.QueryInt("per_page", 10)
	if perPage < 1 {
		perPage = 10
	}
	entries, total, err := h.audit.List(c.Context(), exec, projectID, actorUserID, actorFilterID, actionType, days, perPage, (page-1)*perPage, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil audit trail project")
	}
	data := make([]fiber.Map, len(entries))
	for i := range entries {
		data[i] = workspaceAuditEntryToMap(&entries[i])
	}
	return c.JSON(fiber.Map{
		"data": data,
		"meta": fiber.Map{"page": page, "per_page": perPage, "total": total, "total_pages": (total + perPage - 1) / perPage},
	})
}

// Actors menangani GET /projects/:projectId/audit-logs/actors.
func (h *ProjectAuditHandler) Actors(c *fiber.Ctx) error {
	actorUserID, actorRole, exec, errResp := auditActorAndExec(c, h.logger, "ProjectAuditHandler.Actors")
	if errResp != nil {
		return errResp
	}
	actors, err := h.audit.ListActors(c.Context(), exec, c.Params("projectId"), actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil daftar aktor")
	}
	data := make([]fiber.Map, len(actors))
	for i, a := range actors {
		data[i] = fiber.Map{"id": a.ID, "name": a.Name}
	}
	return c.JSON(response.Success(data))
}

// Executions menangani GET /projects/:projectId/audit-logs/rule-executions.
func (h *ProjectAuditHandler) Executions(c *fiber.Ctx) error {
	actorUserID, actorRole, exec, errResp := auditActorAndExec(c, h.logger, "ProjectAuditHandler.Executions")
	if errResp != nil {
		return errResp
	}
	list, err := h.audit.RuleExecutions(c.Context(), exec, c.Params("projectId"), actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil log eksekusi rule")
	}
	return c.JSON(response.Success(ruleExecutionsToMaps(list)))
}

func (h *ProjectAuditHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Audit Trail project hanya untuk PM project ini, Admin Workspace, atau Group Admin.", nil))
	case errors.Is(err, domain.ErrProjectNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Project tidak ditemukan", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}

// ruleExecutionsToMaps -- bentuk Log Eksekusi yang sama dengan Audit Trail Workspace.
func ruleExecutionsToMaps(list []repository.RuleExecution) []fiber.Map {
	data := make([]fiber.Map, len(list))
	for i := range list {
		e := &list[i]
		data[i] = fiber.Map{
			"id": e.ID, "rule_id": e.RuleID, "rule_name": e.RuleName,
			"trigger_event": json.RawMessage(e.TriggerEvent), "triggered_by": e.TriggeredBy,
			"executed_at": e.ExecutedAt.UTC().Format(time.RFC3339), "status": e.Status,
			"action_taken": json.RawMessage(e.ActionTaken), "error_message": e.ErrorMessage,
			"task_code": e.TaskCode, "task_title": e.TaskTitle,
		}
	}
	return data
}
