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

// GroupAuditHandler -- Audit Trail (Track S4G, desain "GA Audit
// Trail.dc.html"). Lihat komentar package service kenapa ini baca dari
// audit_logs, bukan tabel terpisah.
type GroupAuditHandler struct {
	audit  *service.GroupAuditService
	logger *zap.Logger
}

func NewGroupAuditHandler(audit *service.GroupAuditService, logger *zap.Logger) *GroupAuditHandler {
	return &GroupAuditHandler{audit: audit, logger: logger}
}

var validActionTypes = map[string]bool{"": true, "CREATE": true, "UPDATE": true, "DELETE": true, "ACCESS": true}

// List menangani GET /groups/:groupId/audit-logs -- filter actor_id/
// action_type/days, paginasi page/per_page, atau ?export=csv untuk unduh
// seluruh hasil filter (sinkron, tanpa paginasi -- lihat komentar service).
func (h *GroupAuditHandler) List(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("GroupAuditHandler.List dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("GroupAuditHandler.List dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")

	actionType := c.Query("action_type")
	if !validActionTypes[actionType] {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "action_type harus salah satu dari CREATE/UPDATE/DELETE/ACCESS", nil))
	}
	actorFilterID := c.Query("actor_id")
	days := c.QueryInt("days", 30)

	if c.Query("export") == "csv" {
		entries, err := h.audit.ExportCSV(c.Context(), exec, groupID, actorUserID, actorFilterID, actionType, days, actorRole)
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

	entries, total, err := h.audit.List(c.Context(), exec, groupID, actorUserID, actorFilterID, actionType, days, perPage, (page-1)*perPage, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil audit trail")
	}

	data := make([]fiber.Map, len(entries))
	for i := range entries {
		data[i] = groupAuditEntryToMap(&entries[i])
	}
	totalPages := (total + perPage - 1) / perPage
	return c.JSON(fiber.Map{
		"data": data,
		"meta": fiber.Map{"page": page, "per_page": perPage, "total": total, "total_pages": totalPages},
	})
}

// Actors menangani GET /groups/:groupId/audit-logs/actors -- opsi dropdown
// "AKTOR".
func (h *GroupAuditHandler) Actors(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("GroupAuditHandler.Actors dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("GroupAuditHandler.Actors dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")

	actors, err := h.audit.ListActors(c.Context(), exec, groupID, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil daftar aktor")
	}
	data := make([]fiber.Map, len(actors))
	for i, a := range actors {
		data[i] = fiber.Map{"id": a.ID, "name": a.Name}
	}
	return c.JSON(response.Success(data))
}

func (h *GroupAuditHandler) writeCSV(c *fiber.Ctx, entries []repository.GroupAuditLogEntry) error {
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", `attachment; filename="audit-trail.csv"`)

	w := csv.NewWriter(c.Response().BodyWriter())
	if err := w.Write([]string{"entry_id", "timestamp_utc", "actor_name", "actor_id", "action_type", "action", "entity_type", "entity_id", "entity_name", "organization", "value_before", "value_after", "ip_address"}); err != nil {
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
			e.Action,
			e.EntityType,
			stringOrEmpty(e.EntityID),
			stringOrEmpty(e.TargetName),
			stringOrEmpty(e.OrgName),
			string(e.StateBefore),
			string(e.StateAfter),
			stringOrEmpty(e.ActorIP),
		}); err != nil {
			return fmt.Errorf("handler.writeCSV: row: %w", err)
		}
	}
	w.Flush()
	return w.Error()
}

func groupAuditEntryToMap(e *repository.GroupAuditLogEntry) fiber.Map {
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
		"org_name":           e.OrgName,
		"target_name":        e.TargetName,
		"actor_ip":           e.ActorIP,
		"state_before":       stateBefore,
		"state_after":        stateAfter,
		"metadata":           metadata,
		"logged_at":          e.LoggedAt.UTC().Format(time.RFC3339),
	}
}

func (h *GroupAuditHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
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
