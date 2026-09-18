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

// RuleHandler -- Rule Automation (S4W-10/12, EPIC 7, desain "AW Rule
// Automation.dc.html"+"AW Add Rule.dc.html"). AW-only, level workspace
// saja -- lihat komentar package service.
type RuleHandler struct {
	rules  *service.RuleService
	logger *zap.Logger
}

func NewRuleHandler(rules *service.RuleService, logger *zap.Logger) *RuleHandler {
	return &RuleHandler{rules: rules, logger: logger}
}

type ruleConditionRequest struct {
	Type      string `json:"type"`
	ProjectID string `json:"project_id"`
	SprintID  string `json:"sprint_id"`
	Priority  string `json:"priority"`
	UserID    string `json:"user_id"`
}

type createRuleRequest struct {
	Name    string `json:"name"`
	Trigger struct {
		Event    string `json:"event"`
		StatusID string `json:"status_id"`
		Days     int    `json:"days"`
	} `json:"trigger"`
	Condition *ruleConditionRequest `json:"condition"`
	Action    struct {
		Type         string `json:"type"`
		StatusID     string `json:"status_id"`
		TargetUserID string `json:"target_user_id"`
	} `json:"action"`
}

// List menangani GET /workspaces/:wsId/rules.
func (h *RuleHandler) List(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("RuleHandler.List dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("RuleHandler.List dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	list, err := h.rules.List(c.Context(), exec, workspaceID, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil daftar rule")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = ruleJSON(&list[i])
	}
	return c.JSON(response.Success(data))
}

// Create menangani POST /workspaces/:wsId/rules.
func (h *RuleHandler) Create(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("RuleHandler.Create dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("RuleHandler.Create dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	var body createRuleRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}

	var condition *service.RuleConditionInput
	if body.Condition != nil {
		condition = &service.RuleConditionInput{
			Type: body.Condition.Type, ProjectID: body.Condition.ProjectID, SprintID: body.Condition.SprintID,
			Priority: body.Condition.Priority, UserID: body.Condition.UserID,
		}
	}

	id, err := h.rules.Create(c.Context(), exec, workspaceID, body.Name,
		service.RuleTriggerInput{Event: body.Trigger.Event, StatusID: body.Trigger.StatusID, Days: body.Trigger.Days},
		condition,
		service.RuleActionInput{Type: body.Action.Type, StatusID: body.Action.StatusID, TargetUserID: body.Action.TargetUserID},
		actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal membuat rule")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{"id": id}))
}

type toggleRuleActiveRequest struct {
	Active bool `json:"active"`
}

// ToggleActive menangani PATCH /rules/:id/toggle-active.
func (h *RuleHandler) ToggleActive(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("RuleHandler.ToggleActive dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("RuleHandler.ToggleActive dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	ruleID := c.Params("id")

	var body toggleRuleActiveRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}

	if err := h.rules.SetActive(c.Context(), exec, ruleID, body.Active, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal mengubah status rule")
	}
	return c.JSON(response.Success(fiber.Map{"id": ruleID, "active": body.Active}))
}

// Delete menangani DELETE /rules/:id.
func (h *RuleHandler) Delete(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("RuleHandler.Delete dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("RuleHandler.Delete dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	ruleID := c.Params("id")

	if err := h.rules.Delete(c.Context(), exec, ruleID, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal menghapus rule")
	}
	return c.JSON(response.Success(fiber.Map{"id": ruleID}))
}

// ListExecutions menangani GET /workspaces/:wsId/rules/executions?status=.
func (h *RuleHandler) ListExecutions(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("RuleHandler.ListExecutions dipanggil tanpa RequireRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("RuleHandler.ListExecutions dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	list, err := h.rules.ListExecutions(c.Context(), exec, workspaceID, c.Query("status"), actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil log eksekusi")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = ruleExecutionJSON(&list[i])
	}
	return c.JSON(response.Success(data))
}

func ruleJSON(rl *repository.Rule) fiber.Map {
	return fiber.Map{
		"id": rl.ID, "name": rl.Name, "trigger_config": rl.TriggerConfig, "condition_config": rl.ConditionConfig,
		"action_config": rl.ActionConfig, "is_active": rl.IsActive, "inactive_reason": rl.InactiveReason,
		"is_template": rl.IsTemplate, "created_by": rl.CreatedBy, "created_at": rl.CreatedAt, "runs": rl.Runs,
	}
}

func ruleExecutionJSON(e *repository.RuleExecution) fiber.Map {
	return fiber.Map{
		"id": e.ID, "rule_id": e.RuleID, "rule_name": e.RuleName, "trigger_event": e.TriggerEvent,
		"triggered_by": e.TriggeredBy, "executed_at": e.ExecutedAt, "status": e.Status,
		"action_taken": e.ActionTaken, "error_message": e.ErrorMessage,
	}
}

func (h *RuleHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR",
			"Input tidak valid -- periksa nama, trigger, kondisi, dan action rule", nil))
	case errors.Is(err, domain.ErrTaskStatusUndefined):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("STATUS_UNDEFINED",
			"Status yang dipilih sedang UNDEFINED -- rule tidak dapat disimpan dengan status ini", nil))
	case errors.Is(err, domain.ErrRuleNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Rule tidak ditemukan", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas workspace ini.", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
