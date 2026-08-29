package handler

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// PlatformAuditHandler -- S4P-22, US-071: GET /platform/audit-logs (list +
// export CSV) untuk panel "Platform Audit Trail".
type PlatformAuditHandler struct {
	audit  *service.PlatformAuditService
	logger *zap.Logger
}

func NewPlatformAuditHandler(audit *service.PlatformAuditService, logger *zap.Logger) *PlatformAuditHandler {
	return &PlatformAuditHandler{audit: audit, logger: logger}
}

// parseAuditDateFilter -- "from"/"to" berformat YYYY-MM-DD (bukan RFC3339,
// AC cuma minta "rentang tanggal", bukan jam). "to" digeser ke akhir hari
// itu supaya inklusif (tanpa ini, ?to=2026-08-28 tidak menyertakan kejadian
// hari itu sendiri karena logged_at punya komponen jam).
func parseAuditDateFilter(c *fiber.Ctx) (from, to *time.Time, err error) {
	if raw := c.Query("from"); raw != "" {
		t, parseErr := time.Parse("2006-01-02", raw)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("from tidak valid, pakai format YYYY-MM-DD")
		}
		from = &t
	}
	if raw := c.Query("to"); raw != "" {
		t, parseErr := time.Parse("2006-01-02", raw)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("to tidak valid, pakai format YYYY-MM-DD")
		}
		endOfDay := t.Add(24*time.Hour - time.Nanosecond)
		to = &endOfDay
	}
	return from, to, nil
}

// List menangani GET /platform/audit-logs -- filter action_type/actor_id/
// from/to, paginasi page/per_page (docs/coding-conventions.md §7.1), atau
// ?export=csv untuk unduh CSV seluruh hasil filter (tanpa paginasi).
func (h *PlatformAuditHandler) List(c *fiber.Ctx) error {
	from, to, err := parseAuditDateFilter(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", err.Error(), nil))
	}

	filter := repository.PlatformAuditLogFilter{
		ActionType: c.Query("action_type"),
		ActorID:    c.Query("actor_id"),
		From:       from,
		To:         to,
	}

	if c.Query("export") == "csv" {
		return h.exportCSV(c, filter)
	}

	page := c.QueryInt("page", 1)
	if page < 1 {
		page = 1
	}
	perPage := c.QueryInt("per_page", 50)
	if perPage < 1 {
		perPage = 50
	}
	filter.Limit = perPage
	filter.Offset = (page - 1) * perPage

	entries, total, err := h.audit.ListAuditLogs(c.Context(), filter)
	if err != nil {
		h.logger.Error("gagal mengambil platform audit log", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil platform audit log", nil))
	}

	data := make([]fiber.Map, len(entries))
	for i := range entries {
		data[i] = auditLogEntryToMap(&entries[i])
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

// exportCSV -- S4P-22 AC "?export=csv → download CSV". Limit dipatok ke
// maxAuditLogPerPage*10 (bukan benar-benar tanpa batas) supaya satu request
// tidak bisa memicu full-table scan tanpa henti -- cukup besar untuk
// kebutuhan audit rutin, dicatat sebagai batasan yang disengaja.
const csvExportLimit = 2000

func (h *PlatformAuditHandler) exportCSV(c *fiber.Ctx, filter repository.PlatformAuditLogFilter) error {
	filter.Limit = csvExportLimit
	filter.Offset = 0
	entries, _, err := h.audit.ListAuditLogs(c.Context(), filter)
	if err != nil {
		h.logger.Error("gagal mengekspor platform audit log", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengekspor platform audit log", nil))
	}

	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", `attachment; filename="platform-audit-logs.csv"`)

	w := csv.NewWriter(c.Response().BodyWriter())
	if err := w.Write([]string{"logged_at", "action", "entity_type", "entity_id", "target_name", "actor_email", "actor_display_name", "actor_role", "actor_ip"}); err != nil {
		return fmt.Errorf("handler.exportCSV: header: %w", err)
	}
	for i := range entries {
		e := &entries[i]
		targetName := stringOrEmpty(e.TargetUserName)
		if targetName == "" {
			targetName = stringOrEmpty(e.TargetTierName)
		}
		if err := w.Write([]string{
			e.LoggedAt.UTC().Format(time.RFC3339),
			e.Action,
			e.EntityType,
			stringOrEmpty(e.EntityID),
			targetName,
			stringOrEmpty(e.ActorEmail),
			stringOrEmpty(e.ActorDisplayName),
			stringOrEmpty(e.ActorRole),
			stringOrEmpty(e.ActorIP),
		}); err != nil {
			return fmt.Errorf("handler.exportCSV: row: %w", err)
		}
	}
	w.Flush()
	return w.Error()
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func auditLogEntryToMap(e *repository.PlatformAuditLogEntry) fiber.Map {
	var metadata any
	if len(e.Metadata) > 0 {
		metadata = json.RawMessage(e.Metadata)
	}
	return fiber.Map{
		"id":                 e.ID,
		"actor_id":           e.ActorID,
		"actor_email":        e.ActorEmail,
		"actor_display_name": e.ActorDisplayName,
		"actor_role":         e.ActorRole,
		"action":             e.Action,
		"entity_type":        e.EntityType,
		"entity_id":          e.EntityID,
		"target_user_name":   e.TargetUserName,
		"target_user_role":   e.TargetUserRole,
		"target_tier_name":   e.TargetTierName,
		"actor_ip":           e.ActorIP,
		"metadata":           metadata,
		"logged_at":          e.LoggedAt.UTC().Format(time.RFC3339),
	}
}
