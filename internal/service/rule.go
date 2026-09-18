// Package service -- RuleService (Rule Automation, S4W-10/12, EPIC 7, US-
// 048/049/051/052/053, desain "AW Rule Automation.dc.html"+"AW Add Rule.
// dc.html"). Lihat komentar RuleRepository untuk batas cakupan (workspace-
// scope saja) dan penyimpangan skema.
//
// TIDAK ADA trigger nyata di sini -- CRUD murni, execution engine (hook
// status-change sinkron + job Asynq due-date) menyusul S4W-11 (H17-19),
// sesuai kickoff plan ("log eksekusi diisi data uji manual" untuk task
// ini). Tab Log Eksekusi karena itu akan kosong sampai S4W-11 selesai --
// bukan bug, expected.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// RuleTriggerEvents/RuleConditionTypes/RuleActionTypes -- katalog resmi
// backlog.md US-049 AC ("Open Builder"). "Tanpa kondisi" TIDAK masuk
// RuleConditionTypes -- direpresentasikan lewat conditionConfig kosong
// (condition_config NULL di DB), bukan value string.
var (
	RuleTriggerEvents  = []string{"status_changed", "assignee_changed", "due_date_approaching", "task_created", "comment_added"}
	RuleConditionTypes = []string{"project", "sprint", "priority", "assignee"}
	RuleActionTypes    = []string{"change_status", "notify", "assign", "create_subtask"}
)

func inList(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ruleTriggerConfig/ruleConditionConfig/ruleActionConfig -- bentuk JSONB
// trigger_config/condition_config/action_config. status_id dipakai KEDUA
// trigger (status_changed) dan action (change_status) -- kunci yang sama
// dibaca RuleRepository.DeactivateForStatus untuk mencari rule terdampak
// saat status di-undefine (US-053), jadi field ini WAJIB persis "status_id"
// di kedua struct.
type ruleTriggerConfig struct {
	Event    string `json:"event"`
	StatusID string `json:"status_id,omitempty"`
	Days     int    `json:"days,omitempty"`
}

type ruleConditionConfig struct {
	Type      string `json:"type"`
	ProjectID string `json:"project_id,omitempty"`
	SprintID  string `json:"sprint_id,omitempty"`
	Priority  string `json:"priority,omitempty"`
	UserID    string `json:"user_id,omitempty"`
}

type ruleActionConfig struct {
	Type         string `json:"type"`
	StatusID     string `json:"status_id,omitempty"`
	TargetUserID string `json:"target_user_id,omitempty"`
}

// RuleTriggerInput/RuleConditionInput/RuleActionInput -- bentuk request dari
// handler, sengaja dipisah dari struct JSONB internal supaya kontrak
// HTTP tidak diam-diam ikut berubah kalau bentuk penyimpanan berubah.
type RuleTriggerInput struct {
	Event    string
	StatusID string
	Days     int
}

type RuleConditionInput struct {
	Type      string
	ProjectID string
	SprintID  string
	Priority  string
	UserID    string
}

type RuleActionInput struct {
	Type         string
	StatusID     string
	TargetUserID string
}

// ruleRepository -- interface didefinisikan di consumer, §3.9.
type ruleRepository interface {
	Create(ctx context.Context, exec db.Executor, workspaceID, name string, triggerConfig, conditionConfig, actionConfig []byte, actorID, actorRole string) (string, error)
	Get(ctx context.Context, exec db.Executor, ruleID string) (*repository.Rule, error)
	ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.Rule, error)
	SetActive(ctx context.Context, exec db.Executor, ruleID string, active bool, actorID, actorRole string, before *repository.Rule) error
	SoftDelete(ctx context.Context, exec db.Executor, ruleID, actorID, actorRole string, before *repository.Rule) error
	DeactivateForStatus(ctx context.Context, exec db.Executor, statusID, reason, actorID, actorRole string) ([]repository.Rule, error)
	ListExecutions(ctx context.Context, exec db.Executor, workspaceID, statusFilter string) ([]repository.RuleExecution, error)
}

// ruleStatusChecker -- reuse CustomStatusRepository.Get, dipakai validasi
// status_id (trigger status_changed & action change_status) benar milik
// workspace ini dan tidak sedang UNDEFINED.
type ruleStatusChecker interface {
	Get(ctx context.Context, exec db.Executor, statusID string) (*repository.CustomStatus, error)
}

// ruleProjectResolver -- reuse ProjectRepository.GetWorkspaceID, dipakai
// validasi condition "project tertentu".
type ruleProjectResolver interface {
	GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error)
}

// ruleUserContactFinder -- reuse AccountRepository.FindUserContactByID,
// dipakai DeactivateForStatus mengirim email ke pembuat rule (US-053).
type ruleUserContactFinder interface {
	FindUserContactByID(ctx context.Context, userID string) (*repository.UserContact, error)
}

type RuleService struct {
	repo     ruleRepository
	rbac     sprintWorkspaceRoleChecker
	statuses ruleStatusChecker
	projects ruleProjectResolver
	contacts ruleUserContactFinder
	emailer  *EmailService
}

func NewRuleService(repo ruleRepository, rbac sprintWorkspaceRoleChecker, statuses ruleStatusChecker, projects ruleProjectResolver, contacts ruleUserContactFinder, emailer *EmailService) *RuleService {
	return &RuleService{repo: repo, rbac: rbac, statuses: statuses, projects: projects, contacts: contacts, emailer: emailer}
}

// authorizeWorkspace -- AW-only, PERSIS pola CustomStatusService.
// authorizeAdmin/WebhookService.authorizeWorkspace.
func (s *RuleService) authorizeWorkspace(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return nil
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("service.authorizeWorkspace: %w", err)
	}
	if role != "admin_workspace" {
		return fmt.Errorf("service.authorizeWorkspace: %w", domain.ErrForbidden)
	}
	return nil
}

// validateStatusTarget -- status_id (trigger status_changed / action
// change_status) wajib ada, milik workspace ini, dan tidak UNDEFINED.
func (s *RuleService) validateStatusTarget(ctx context.Context, exec db.Executor, workspaceID, statusID string) error {
	if statusID == "" {
		return fmt.Errorf("service.validateStatusTarget: %w", domain.ErrInvalidInput)
	}
	status, err := s.statuses.Get(ctx, exec, statusID)
	if err != nil {
		return fmt.Errorf("service.validateStatusTarget: %w", domain.ErrInvalidInput)
	}
	if status.ScopeType != "workspace" || status.ScopeID != workspaceID {
		return fmt.Errorf("service.validateStatusTarget: %w", domain.ErrInvalidInput)
	}
	if status.IsUndefined {
		return fmt.Errorf("service.validateStatusTarget: %w", domain.ErrTaskStatusUndefined)
	}
	return nil
}

// buildConfigs -- validasi TCA lengkap + serialisasi ke JSONB. Dipakai
// Create (Update sengaja tidak ada -- desain "AW Add Rule.dc.html" cuma
// bikin rule baru, tidak ada mode edit).
func (s *RuleService) buildConfigs(ctx context.Context, exec db.Executor, workspaceID string, trigger RuleTriggerInput, condition *RuleConditionInput, action RuleActionInput) (triggerJSON, conditionJSON, actionJSON []byte, err error) {
	if !inList(RuleTriggerEvents, trigger.Event) {
		return nil, nil, nil, fmt.Errorf("service.buildConfigs: %w", domain.ErrInvalidInput)
	}
	tc := ruleTriggerConfig{Event: trigger.Event}
	switch trigger.Event {
	case "status_changed":
		if err := s.validateStatusTarget(ctx, exec, workspaceID, trigger.StatusID); err != nil {
			return nil, nil, nil, err
		}
		tc.StatusID = trigger.StatusID
	case "due_date_approaching":
		if trigger.Days <= 0 {
			return nil, nil, nil, fmt.Errorf("service.buildConfigs: %w", domain.ErrInvalidInput)
		}
		tc.Days = trigger.Days
	}
	triggerJSON, err = json.Marshal(tc)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("service.buildConfigs: %w", err)
	}

	if condition != nil {
		if !inList(RuleConditionTypes, condition.Type) {
			return nil, nil, nil, fmt.Errorf("service.buildConfigs: %w", domain.ErrInvalidInput)
		}
		cc := ruleConditionConfig{Type: condition.Type}
		switch condition.Type {
		case "project":
			if condition.ProjectID == "" {
				return nil, nil, nil, fmt.Errorf("service.buildConfigs: %w", domain.ErrInvalidInput)
			}
			projectWorkspaceID, err := s.projects.GetWorkspaceID(ctx, exec, condition.ProjectID)
			if err != nil || projectWorkspaceID != workspaceID {
				return nil, nil, nil, fmt.Errorf("service.buildConfigs: %w", domain.ErrInvalidInput)
			}
			cc.ProjectID = condition.ProjectID
		case "sprint":
			if condition.SprintID == "" {
				return nil, nil, nil, fmt.Errorf("service.buildConfigs: %w", domain.ErrInvalidInput)
			}
			cc.SprintID = condition.SprintID
		case "priority":
			if !inList([]string{"critical", "high", "medium", "low"}, condition.Priority) {
				return nil, nil, nil, fmt.Errorf("service.buildConfigs: %w", domain.ErrInvalidInput)
			}
			cc.Priority = condition.Priority
		case "assignee":
			if condition.UserID == "" {
				return nil, nil, nil, fmt.Errorf("service.buildConfigs: %w", domain.ErrInvalidInput)
			}
			cc.UserID = condition.UserID
		}
		conditionJSON, err = json.Marshal(cc)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("service.buildConfigs: %w", err)
		}
	}

	if !inList(RuleActionTypes, action.Type) {
		return nil, nil, nil, fmt.Errorf("service.buildConfigs: %w", domain.ErrInvalidInput)
	}
	ac := ruleActionConfig{Type: action.Type}
	switch action.Type {
	case "change_status":
		if err := s.validateStatusTarget(ctx, exec, workspaceID, action.StatusID); err != nil {
			return nil, nil, nil, err
		}
		ac.StatusID = action.StatusID
	case "notify", "assign":
		if action.TargetUserID == "" {
			return nil, nil, nil, fmt.Errorf("service.buildConfigs: %w", domain.ErrInvalidInput)
		}
		ac.TargetUserID = action.TargetUserID
	}
	actionJSON, err = json.Marshal(ac)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("service.buildConfigs: %w", err)
	}
	return triggerJSON, conditionJSON, actionJSON, nil
}

// Create -- POST /workspaces/:wsId/rules.
func (s *RuleService) Create(ctx context.Context, exec db.Executor, workspaceID, name string, trigger RuleTriggerInput, condition *RuleConditionInput, action RuleActionInput, actorID, actorRole string) (string, error) {
	name = strings.TrimSpace(name)
	if workspaceID == "" || len(name) < 4 {
		return "", fmt.Errorf("service.Create: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return "", err
	}
	triggerJSON, conditionJSON, actionJSON, err := s.buildConfigs(ctx, exec, workspaceID, trigger, condition, action)
	if err != nil {
		return "", err
	}
	id, err := s.repo.Create(ctx, exec, workspaceID, name, triggerJSON, conditionJSON, actionJSON, actorID, actorRole)
	if err != nil {
		return "", fmt.Errorf("service.Create: %w", err)
	}
	return id, nil
}

// List -- GET /workspaces/:wsId/rules, tab "Rule Aktif".
func (s *RuleService) List(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) ([]repository.Rule, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.List: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return nil, err
	}
	list, err := s.repo.ListForWorkspace(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.List: %w", err)
	}
	return list, nil
}

func (s *RuleService) loadForMutation(ctx context.Context, exec db.Executor, ruleID, actorID, actorRole string) (*repository.Rule, error) {
	rl, err := s.repo.Get(ctx, exec, ruleID)
	if err != nil {
		return nil, fmt.Errorf("service.loadForMutation: %w", err)
	}
	if err := s.authorizeWorkspace(ctx, exec, rl.ScopeID, actorID, actorRole); err != nil {
		return nil, err
	}
	return rl, nil
}

func (s *RuleService) SetActive(ctx context.Context, exec db.Executor, ruleID string, active bool, actorID, actorRole string) error {
	before, err := s.loadForMutation(ctx, exec, ruleID, actorID, actorRole)
	if err != nil {
		return err
	}
	if err := s.repo.SetActive(ctx, exec, ruleID, active, actorID, actorRole, before); err != nil {
		return fmt.Errorf("service.SetActive: %w", err)
	}
	return nil
}

func (s *RuleService) Delete(ctx context.Context, exec db.Executor, ruleID, actorID, actorRole string) error {
	before, err := s.loadForMutation(ctx, exec, ruleID, actorID, actorRole)
	if err != nil {
		return err
	}
	if err := s.repo.SoftDelete(ctx, exec, ruleID, actorID, actorRole, before); err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	return nil
}

// ListExecutions -- GET /workspaces/:wsId/rules/executions, tab "Log
// Eksekusi". Kosong sampai S4W-11 -- lihat komentar package.
func (s *RuleService) ListExecutions(ctx context.Context, exec db.Executor, workspaceID, statusFilter, actorID, actorRole string) ([]repository.RuleExecution, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.ListExecutions: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return nil, err
	}
	list, err := s.repo.ListExecutions(ctx, exec, workspaceID, statusFilter)
	if err != nil {
		return nil, fmt.Errorf("service.ListExecutions: %w", err)
	}
	return list, nil
}

// DeactivateForStatus (US-053) -- dipanggil CustomStatusService.Undefine
// SETELAH status berhasil di-undefine (best-effort, TIDAK PERNAH
// menggagalkan Undefine itu sendiri -- lihat pemanggil). Menonaktifkan rule
// terdampak + notifikasi in-app+email ke masing-masing pembuat rule.
func (s *RuleService) DeactivateForStatus(ctx context.Context, exec db.Executor, statusID, statusName, actorID, actorRole string) error {
	reason := fmt.Sprintf("Status %q dihapus (UNDEFINED)", statusName)
	affected, err := s.repo.DeactivateForStatus(ctx, exec, statusID, reason, actorID, actorRole)
	if err != nil {
		return fmt.Errorf("service.DeactivateForStatus: %w", err)
	}
	for i := range affected {
		rl := affected[i]
		contact, err := s.contacts.FindUserContactByID(ctx, rl.CreatedBy)
		if err != nil {
			continue
		}
		if _, execErr := exec.Exec(ctx, `
			INSERT INTO notifications (user_id, actor_id, type, entity_type, entity_id, title, body)
			VALUES ($1, NULL, 'rule_auto_deactivated', 'automation_rule', $2, $3, $4)
		`, rl.CreatedBy, rl.ID, fmt.Sprintf("Rule %q menjadi Inactive", rl.Name), reason); execErr != nil {
			continue
		}
		if s.emailer != nil {
			_ = s.emailer.SendRuleDeactivatedEmail(ctx, contact.Email, contact.DisplayName, rl.Name, statusName)
		}
	}
	return nil
}
