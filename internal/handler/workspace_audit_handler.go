package handler

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// WorkspaceAuditHandler -- Audit Trail Workspace (S4W-16/17, desain "AW
// Audit Trail.dc.html"). Lihat komentar package service/repository.
type WorkspaceAuditHandler struct {
	audit  *service.WorkspaceAuditService
	logger *zap.Logger
}

func NewWorkspaceAuditHandler(audit *service.WorkspaceAuditService, logger *zap.Logger) *WorkspaceAuditHandler {
	return &WorkspaceAuditHandler{audit: audit, logger: logger}
}

// List menangani GET /workspaces/:wsId/audit-logs -- filter actor_id/
// action_type/days, paginasi page/per_page, atau ?export=csv, pola PERSIS
// GroupAuditHandler.List.
func (h *WorkspaceAuditHandler) List(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceAuditHandler.List dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceAuditHandler.List dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	actionType := c.Query("action_type")
	if !validActionTypes[actionType] {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "action_type harus salah satu dari CREATE/UPDATE/DELETE/ACCESS", nil))
	}
	actorFilterID := c.Query("actor_id")
	days := c.QueryInt("days", 30)

	if c.Query("export") == "csv" {
		entries, err := h.audit.ExportCSV(c.Context(), exec, workspaceID, actorUserID, actorFilterID, actionType, days, actorRole)
		if err != nil {
			return h.mapError(c, err, "Gagal mengekspor audit trail")
		}
		return h.writeCSV(c, entries)
	}

	page := c.QueryInt("page", 1)
	if page < 1 {
		page = 1
	}
	perPage := c.QueryInt("per_page", 10)
	if perPage < 1 {
		perPage = 10
	}

	entries, total, err := h.audit.List(c.Context(), exec, workspaceID, actorUserID, actorFilterID, actionType, days, perPage, (page-1)*perPage, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil audit trail")
	}

	data := make([]fiber.Map, len(entries))
	for i := range entries {
		data[i] = workspaceAuditEntryToMap(&entries[i])
	}
	totalPages := (total + perPage - 1) / perPage
	return c.JSON(fiber.Map{
		"data": data,
		"meta": fiber.Map{"page": page, "per_page": perPage, "total": total, "total_pages": totalPages},
	})
}

// Actors menangani GET /workspaces/:wsId/audit-logs/actors.
func (h *WorkspaceAuditHandler) Actors(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceAuditHandler.Actors dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceAuditHandler.Actors dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	actors, err := h.audit.ListActors(c.Context(), exec, workspaceID, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil daftar aktor")
	}
	data := make([]fiber.Map, len(actors))
	for i, a := range actors {
		data[i] = fiber.Map{"id": a.ID, "name": a.Name}
	}
	return c.JSON(response.Success(data))
}

// Executions menangani GET /workspaces/:wsId/audit-logs/rule-executions --
// feed terpisah (Log Eksekusi Rule Automation) yang digabung di FE ke grid
// utama, pola persis desain "AW Audit Trail.dc.html"
// (this.state.audit.concat(fromRules)).
func (h *WorkspaceAuditHandler) Executions(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceAuditHandler.Executions dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WorkspaceAuditHandler.Executions dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	list, err := h.audit.RuleExecutions(c.Context(), exec, workspaceID, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil log eksekusi rule")
	}
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
	return c.JSON(response.Success(data))
}

func (h *WorkspaceAuditHandler) writeCSV(c *fiber.Ctx, entries []repository.WorkspaceAuditLogEntry) error {
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", `attachment; filename="audit-trail-workspace.csv"`)

	w := csv.NewWriter(c.Response().BodyWriter())
	if err := w.Write([]string{"entry_id", "timestamp_utc", "actor_name", "actor_id", "action_type", "action", "entity_type", "entity_id", "entity_name", "value_before", "value_after", "ip_address", "request_path"}); err != nil {
		return fmt.Errorf("handler.writeCSV: header: %w", err)
	}
	for i := range entries {
		e := &entries[i]
		if err := w.Write([]string{
			e.ID,
			e.LoggedAt.UTC().Format(time.RFC3339),
			stringOrEmpty(e.ActorDisplayName),
			stringOrEmpty(e.ActorID),
			e.Type,
			workspaceAuditNarrativeText(e),
			e.EntityType,
			stringOrEmpty(e.EntityID),
			stringOrEmpty(e.TargetName),
			string(e.StateBefore),
			string(e.StateAfter),
			stringOrEmpty(e.ActorIP),
			stringOrEmpty(e.RequestPath),
		}); err != nil {
			return fmt.Errorf("handler.writeCSV: row: %w", err)
		}
	}
	w.Flush()
	return w.Error()
}

func workspaceAuditEntryToMap(e *repository.WorkspaceAuditLogEntry) fiber.Map {
	var metadata, stateBefore, stateAfter any
	if len(e.Metadata) > 0 {
		metadata = json.RawMessage(e.Metadata)
	}
	if len(e.StateBefore) > 0 {
		stateBefore = json.RawMessage(e.StateBefore)
	}
	if len(e.StateAfter) > 0 {
		stateAfter = json.RawMessage(e.StateAfter)
	}
	return fiber.Map{
		"id":                 e.ID,
		"actor_id":           e.ActorID,
		"actor_email":        e.ActorEmail,
		"actor_display_name": e.ActorDisplayName,
		"actor_role":         e.ActorRole,
		"action":             e.Action,
		"type":               e.Type,
		"entity_type":        e.EntityType,
		"entity_id":          e.EntityID,
		"target_name":        e.TargetName,
		"actor_ip":           e.ActorIP,
		"request_path":       e.RequestPath,
		"state_before":       stateBefore,
		"state_after":        stateAfter,
		"metadata":           metadata,
		"logged_at":          e.LoggedAt.UTC().Format(time.RFC3339),
	}
}

func (h *WorkspaceAuditHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas workspace ini.", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
