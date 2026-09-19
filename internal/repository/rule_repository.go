// Package repository -- RuleRepository (Rule Automation, S4W-10/12, EPIC 7,
// desain "AW Rule Automation.dc.html"+"AW Add Rule.dc.html"). Tabel
// `automation_rules`+`automation_rule_executions` (migrasi 20261021090000)
// -- lihat komentar migrasi untuk penyimpangan skema (deleted_at/
// inactive_reason ditambah, status VARCHAR bukan enum job_status).
//
// Cakupan HANYA scope_type='workspace' -- rule level project (PM) adalah
// track terpisah di luar Track S4W (dikonfirmasi user, pola sama batas
// cakupan Custom Status/Cooldown Mention/Webhook sebelumnya). Kolom
// scope_type tetap disimpan sesuai skema untuk kompatibilitas RLS/masa
// depan, tapi Create di sini SELALU menulis 'workspace'.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type Rule struct {
	ID              string
	ScopeType       string
	ScopeID         string
	Name            string
	TriggerConfig   json.RawMessage
	ConditionConfig json.RawMessage
	ActionConfig    json.RawMessage
	IsActive        bool
	InactiveReason  *string
	IsTemplate      bool
	CreatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// Runs -- jumlah automation_rule_executions, dihitung via LEFT JOIN
	// (pola sama Webhook.Sent30d), bukan kolom counter -- tidak ada risiko
	// drift.
	Runs int
}

type RuleExecution struct {
	ID           string
	RuleID       string
	RuleName     string
	TriggerEvent json.RawMessage
	TriggeredBy  *string
	ExecutedAt   time.Time
	Status       string
	ActionTaken  json.RawMessage
	ErrorMessage *string
	// TaskCode/TaskTitle -- reuse "task_id" di TriggerEvent (S4W-11 selalu
	// task-based), LEFT JOIN supaya baris tetap muncul walau task sudah
	// dihapus (soft-delete) atau id-nya tidak valid. Dipakai kolom "TASK"
	// CSV export ("AW Rule Automation.dc.html") -- nil kalau task tidak
	// ditemukan.
	TaskCode  *string
	TaskTitle *string
}

type RuleRepository struct{}

func NewRuleRepository() *RuleRepository { return &RuleRepository{} }

// Create -- scope_type SELALU 'workspace' (lihat komentar package).
func (r *RuleRepository) Create(ctx context.Context, exec db.Executor, workspaceID, name string, triggerConfig, conditionConfig, actionConfig []byte, actorID, actorRole string) (string, error) {
	var id string
	err := exec.QueryRow(ctx, `
		INSERT INTO automation_rules (scope_type, scope_id, name, trigger_config, condition_config, action_config, created_by)
		VALUES ('workspace', $1, $2, $3, $4, $5, $6)
		RETURNING id
	`, workspaceID, name, triggerConfig, conditionConfig, actionConfig, actorID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("repository.Create: %w", err)
	}
	if err := insertRuleAudit(ctx, exec, actorID, actorRole, "rule.created", id, workspaceID, name, nil); err != nil {
		return "", fmt.Errorf("repository.Create: %w", err)
	}
	return id, nil
}

// Get mengembalikan satu rule TANPA hitungan eksekusi (dipakai SetActive/
// SoftDelete/DeactivateForStatus, cuma butuh baris intinya).
func (r *RuleRepository) Get(ctx context.Context, exec db.Executor, ruleID string) (*Rule, error) {
	var rl Rule
	err := exec.QueryRow(ctx, `
		SELECT id, scope_type, scope_id, name, trigger_config, condition_config, action_config,
		       is_active, inactive_reason, is_template, created_by, created_at, updated_at
		FROM automation_rules WHERE id = $1 AND deleted_at IS NULL
	`, ruleID).Scan(&rl.ID, &rl.ScopeType, &rl.ScopeID, &rl.Name, &rl.TriggerConfig, &rl.ConditionConfig, &rl.ActionConfig,
		&rl.IsActive, &rl.InactiveReason, &rl.IsTemplate, &rl.CreatedBy, &rl.CreatedAt, &rl.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.Get: %w", domain.ErrRuleNotFound)
		}
		return nil, fmt.Errorf("repository.Get: %w", err)
	}
	return &rl, nil
}

// ListForWorkspace -- daftar rule untuk tab "Rule Aktif" + stats bar.
func (r *RuleRepository) ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]Rule, error) {
	rows, err := exec.Query(ctx, `
		SELECT ar.id, ar.scope_type, ar.scope_id, ar.name, ar.trigger_config, ar.condition_config, ar.action_config,
		       ar.is_active, ar.inactive_reason, ar.is_template, ar.created_by, ar.created_at, ar.updated_at,
		       COUNT(e.id)
		FROM automation_rules ar
		LEFT JOIN automation_rule_executions e ON e.rule_id = ar.id
		WHERE ar.scope_type = 'workspace' AND ar.scope_id = $1 AND ar.deleted_at IS NULL
		GROUP BY ar.id
		ORDER BY ar.created_at DESC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListForWorkspace: %w", err)
	}
	defer rows.Close()

	list := make([]Rule, 0)
	for rows.Next() {
		var rl Rule
		if err := rows.Scan(&rl.ID, &rl.ScopeType, &rl.ScopeID, &rl.Name, &rl.TriggerConfig, &rl.ConditionConfig, &rl.ActionConfig,
			&rl.IsActive, &rl.InactiveReason, &rl.IsTemplate, &rl.CreatedBy, &rl.CreatedAt, &rl.UpdatedAt, &rl.Runs); err != nil {
			return nil, fmt.Errorf("repository.ListForWorkspace: scan: %w", err)
		}
		list = append(list, rl)
	}
	return list, rows.Err()
}

func (r *RuleRepository) SetActive(ctx context.Context, exec db.Executor, ruleID string, active bool, actorID, actorRole string, before *Rule) error {
	tag, err := exec.Exec(ctx, `
		UPDATE automation_rules SET is_active = $2, inactive_reason = NULL, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, ruleID, active)
	if err != nil {
		return fmt.Errorf("repository.SetActive: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetActive: %w", domain.ErrRuleNotFound)
	}
	action := "rule.activated"
	if !active {
		action = "rule.deactivated"
	}
	if err := insertRuleAudit(ctx, exec, actorID, actorRole, action, ruleID, before.ScopeID, before.Name, nil); err != nil {
		return fmt.Errorf("repository.SetActive: %w", err)
	}
	return nil
}

// SoftDelete -- "HAPUS PERMANEN" di desain, tapi kebijakan standing project
// ini soft-delete di semua tabel entitas (lihat komentar migrasi) -- UI
// tetap terlihat permanen ke user (tidak ada tombol restore di desain).
func (r *RuleRepository) SoftDelete(ctx context.Context, exec db.Executor, ruleID, actorID, actorRole string, before *Rule) error {
	tag, err := exec.Exec(ctx, `UPDATE automation_rules SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, ruleID)
	if err != nil {
		return fmt.Errorf("repository.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SoftDelete: %w", domain.ErrRuleNotFound)
	}
	if err := insertRuleAudit(ctx, exec, actorID, actorRole, "rule.deleted", ruleID, before.ScopeID, before.Name, nil); err != nil {
		return fmt.Errorf("repository.SoftDelete: %w", err)
	}
	return nil
}

// DeactivateForStatus (US-053) -- dipanggil CustomStatusService.Undefine
// SETELAH status berhasil di-undefine. Mencari rule AKTIF yang trigger ATAU
// action-nya mereferensi statusID (disimpan sebagai "status_id" di JSONB),
// menonaktifkannya + mencatat alasan, mengembalikan baris yang terdampak
// supaya caller bisa mengirim notifikasi in-app+email ke masing-masing
// pembuat rule.
func (r *RuleRepository) DeactivateForStatus(ctx context.Context, exec db.Executor, statusID, reason, actorID, actorRole string) ([]Rule, error) {
	rows, err := exec.Query(ctx, `
		UPDATE automation_rules
		SET is_active = FALSE, inactive_reason = $2, updated_at = NOW()
		WHERE deleted_at IS NULL AND is_active = TRUE
		  AND (trigger_config->>'status_id' = $1 OR action_config->>'status_id' = $1)
		RETURNING id, scope_type, scope_id, name, trigger_config, condition_config, action_config,
		          is_active, inactive_reason, is_template, created_by, created_at, updated_at
	`, statusID, reason)
	if err != nil {
		return nil, fmt.Errorf("repository.DeactivateForStatus: %w", err)
	}
	defer rows.Close()

	var affected []Rule
	for rows.Next() {
		var rl Rule
		if err := rows.Scan(&rl.ID, &rl.ScopeType, &rl.ScopeID, &rl.Name, &rl.TriggerConfig, &rl.ConditionConfig, &rl.ActionConfig,
			&rl.IsActive, &rl.InactiveReason, &rl.IsTemplate, &rl.CreatedBy, &rl.CreatedAt, &rl.UpdatedAt); err != nil {
			return nil, fmt.Errorf("repository.DeactivateForStatus: scan: %w", err)
		}
		affected = append(affected, rl)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.DeactivateForStatus: %w", err)
	}
	for i := range affected {
		if err := insertRuleAudit(ctx, exec, actorID, actorRole, "rule.auto_deactivated", affected[i].ID, affected[i].ScopeID, affected[i].Name,
			map[string]any{"reason": reason}); err != nil {
			return nil, fmt.Errorf("repository.DeactivateForStatus: %w", err)
		}
	}
	return affected, nil
}

// ListExecutions -- tab "Log Eksekusi". Kosong sampai S4W-11 (execution
// engine) benar-benar menulis baris ke automation_rule_executions -- method
// ini tetap dibangun penuh sekarang supaya FE punya endpoint nyata untuk
// tab-nya, bukan endpoint palsu yang menyusul.
func (r *RuleRepository) ListExecutions(ctx context.Context, exec db.Executor, workspaceID, statusFilter string) ([]RuleExecution, error) {
	rows, err := exec.Query(ctx, `
		SELECT e.id, e.rule_id, ar.name, e.trigger_event, e.triggered_by, e.executed_at, e.status, e.action_taken, e.error_message,
		       t.task_code, t.title
		FROM automation_rule_executions e
		JOIN automation_rules ar ON ar.id = e.rule_id
		LEFT JOIN tasks t ON t.id = NULLIF(e.trigger_event->>'task_id', '')::uuid
		WHERE ar.scope_type = 'workspace' AND ar.scope_id = $1
		  AND ($2 = '' OR e.status = $2)
		ORDER BY e.executed_at DESC
		LIMIT 500
	`, workspaceID, statusFilter)
	if err != nil {
		return nil, fmt.Errorf("repository.ListExecutions: %w", err)
	}
	defer rows.Close()

	list := make([]RuleExecution, 0)
	for rows.Next() {
		var e RuleExecution
		if err := rows.Scan(&e.ID, &e.RuleID, &e.RuleName, &e.TriggerEvent, &e.TriggeredBy, &e.ExecutedAt, &e.Status, &e.ActionTaken, &e.ErrorMessage,
			&e.TaskCode, &e.TaskTitle); err != nil {
			return nil, fmt.Errorf("repository.ListExecutions: scan: %w", err)
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

// ListActiveForEvent (S4W-11) -- rule aktif workspace ini yang trigger-nya
// cocok event (+ statusID kalau event="status_changed", "" berarti tidak
// difilter statusnya -- dipakai due-date/task_created yang tidak
// mereferensi status tertentu di trigger).
func (r *RuleRepository) ListActiveForEvent(ctx context.Context, exec db.Executor, workspaceID, event, statusID string) ([]Rule, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, scope_type, scope_id, name, trigger_config, condition_config, action_config,
		       is_active, inactive_reason, is_template, created_by, created_at, updated_at
		FROM automation_rules
		WHERE scope_type = 'workspace' AND scope_id = $1 AND deleted_at IS NULL AND is_active = TRUE
		  AND trigger_config->>'event' = $2
		  AND ($3 = '' OR trigger_config->>'status_id' = $3)
	`, workspaceID, event, statusID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListActiveForEvent: %w", err)
	}
	defer rows.Close()

	list := make([]Rule, 0)
	for rows.Next() {
		var rl Rule
		if err := rows.Scan(&rl.ID, &rl.ScopeType, &rl.ScopeID, &rl.Name, &rl.TriggerConfig, &rl.ConditionConfig, &rl.ActionConfig,
			&rl.IsActive, &rl.InactiveReason, &rl.IsTemplate, &rl.CreatedBy, &rl.CreatedAt, &rl.UpdatedAt); err != nil {
			return nil, fmt.Errorf("repository.ListActiveForEvent: scan: %w", err)
		}
		list = append(list, rl)
	}
	return list, rows.Err()
}

// ListActiveDueDateRules (S4W-11) -- SEMUA rule workspace aktif dengan
// trigger due_date_approaching, lintas workspace -- dipanggil job Asynq
// harian (proses trusted background, bukan request per-workspace).
func (r *RuleRepository) ListActiveDueDateRules(ctx context.Context, exec db.Executor) ([]Rule, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, scope_type, scope_id, name, trigger_config, condition_config, action_config,
		       is_active, inactive_reason, is_template, created_by, created_at, updated_at
		FROM automation_rules
		WHERE scope_type = 'workspace' AND deleted_at IS NULL AND is_active = TRUE
		  AND trigger_config->>'event' = 'due_date_approaching'
	`)
	if err != nil {
		return nil, fmt.Errorf("repository.ListActiveDueDateRules: %w", err)
	}
	defer rows.Close()

	list := make([]Rule, 0)
	for rows.Next() {
		var rl Rule
		if err := rows.Scan(&rl.ID, &rl.ScopeType, &rl.ScopeID, &rl.Name, &rl.TriggerConfig, &rl.ConditionConfig, &rl.ActionConfig,
			&rl.IsActive, &rl.InactiveReason, &rl.IsTemplate, &rl.CreatedBy, &rl.CreatedAt, &rl.UpdatedAt); err != nil {
			return nil, fmt.Errorf("repository.ListActiveDueDateRules: scan: %w", err)
		}
		list = append(list, rl)
	}
	return list, rows.Err()
}

// CreateExecution (S4W-11) -- satu baris PER eksekusi rule, pola PERSIS
// webhook_deliveries (tidak pernah diupdate, immutable log).
func (r *RuleRepository) CreateExecution(ctx context.Context, exec db.Executor, ruleID string, triggerEvent json.RawMessage, triggeredBy *string, status string, actionTaken json.RawMessage, errMessage *string) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO automation_rule_executions (rule_id, trigger_event, triggered_by, status, action_taken, error_message)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, ruleID, triggerEvent, triggeredBy, status, actionTaken, errMessage)
	if err != nil {
		return fmt.Errorf("repository.CreateExecution: %w", err)
	}
	return nil
}

// HasExecutionForTask (S4W-11) -- dedup job due-date: satu rule+task cuma
// boleh eksekusi sekali (tidak berulang tiap kali job harian jalan selama
// task masih dalam jendela ambang hari) -- automation_rule_executions
// sendiri jadi buku catat dedup (trigger_event->>'task_id'), pola sama
// notifications dipakai StorageQuotaCheck/RetentionNotify.
func (r *RuleRepository) HasExecutionForTask(ctx context.Context, exec db.Executor, ruleID, taskID string) (bool, error) {
	var exists bool
	err := exec.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM automation_rule_executions WHERE rule_id = $1 AND trigger_event->>'task_id' = $2)
	`, ruleID, taskID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("repository.HasExecutionForTask: %w", err)
	}
	return exists, nil
}

// insertRuleAudit -- reuse writeAuditLog (chokepoint audit_logs), pola
// PERSIS insertWebhookAudit/insertCustomStatusAudit. stateBefore selalu nil
// -- rule tidak punya Update (builder create-only sesuai desain), semua
// aksi di sini toggle/single-value, tidak ada diff before/after berarti.
// name disertakan sebagai snapshot immutable di metadata.rule_name --
// automation_rules soft-delete (deleted_at) jadi live JOIN sebenarnya aman
// di sini, TAPI snapshot tetap ditambah supaya Audit Trail Workspace tidak
// perlu JOIN sama sekali untuk resolusi nama (satu sumber kebenaran yang
// sama dipakai attachment/custom_status/project, bukan pengecualian).
func insertRuleAudit(ctx context.Context, exec execer, actorID, actorRole, action, ruleID, workspaceID, name string, stateAfter map[string]any) error {
	return writeAuditLog(ctx, exec, "audit_logs", actorID, actorRole, action, "automation_rule", &ruleID,
		map[string]any{"workspace_id": workspaceID, "rule_name": name}, nil, stateAfter)
}
