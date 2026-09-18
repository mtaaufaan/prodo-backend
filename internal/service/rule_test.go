package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeRuleRepo struct {
	byID map[string]*repository.Rule

	createCalls []struct{ workspaceID, name string }

	deactivateResult []repository.Rule

	activeForEvent     []repository.Rule
	activeDueDateRules []repository.Rule
	hasExecutionFor    map[string]bool // key: ruleID+"|"+taskID

	executionCalls []struct {
		ruleID string
		status string
	}
}

func (f *fakeRuleRepo) Create(_ context.Context, _ db.Executor, workspaceID, name string, _, _, _ []byte, _, _ string) (string, error) {
	f.createCalls = append(f.createCalls, struct{ workspaceID, name string }{workspaceID, name})
	return "new-rule", nil
}

func (f *fakeRuleRepo) Get(_ context.Context, _ db.Executor, ruleID string) (*repository.Rule, error) {
	rl, ok := f.byID[ruleID]
	if !ok {
		return nil, domain.ErrRuleNotFound
	}
	return rl, nil
}

func (f *fakeRuleRepo) ListForWorkspace(_ context.Context, _ db.Executor, _ string) ([]repository.Rule, error) {
	return nil, nil
}
func (f *fakeRuleRepo) SetActive(_ context.Context, _ db.Executor, _ string, _ bool, _, _ string, _ *repository.Rule) error {
	return nil
}
func (f *fakeRuleRepo) SoftDelete(_ context.Context, _ db.Executor, _, _, _ string, _ *repository.Rule) error {
	return nil
}
func (f *fakeRuleRepo) DeactivateForStatus(_ context.Context, _ db.Executor, _, _, _, _ string) ([]repository.Rule, error) {
	return f.deactivateResult, nil
}
func (f *fakeRuleRepo) ListExecutions(_ context.Context, _ db.Executor, _, _ string) ([]repository.RuleExecution, error) {
	return nil, nil
}
func (f *fakeRuleRepo) ListActiveForEvent(_ context.Context, _ db.Executor, _, _, _ string) ([]repository.Rule, error) {
	return f.activeForEvent, nil
}
func (f *fakeRuleRepo) ListActiveDueDateRules(_ context.Context, _ db.Executor) ([]repository.Rule, error) {
	return f.activeDueDateRules, nil
}
func (f *fakeRuleRepo) CreateExecution(_ context.Context, _ db.Executor, ruleID string, _ json.RawMessage, _ *string, status string, _ json.RawMessage, _ *string) error {
	f.executionCalls = append(f.executionCalls, struct {
		ruleID string
		status string
	}{ruleID, status})
	return nil
}
func (f *fakeRuleRepo) HasExecutionForTask(_ context.Context, _ db.Executor, ruleID, taskID string) (bool, error) {
	return f.hasExecutionFor[ruleID+"|"+taskID], nil
}

type fakeRuleRoleChecker struct{ role string }

func (f *fakeRuleRoleChecker) GetMemberRole(_ context.Context, _ db.Executor, _, _ string) (string, error) {
	return f.role, nil
}

type fakeRuleStatusChecker struct {
	byID map[string]*repository.CustomStatus
}

func (f *fakeRuleStatusChecker) Get(_ context.Context, _ db.Executor, statusID string) (*repository.CustomStatus, error) {
	s, ok := f.byID[statusID]
	if !ok {
		return nil, domain.ErrCustomStatusNotFound
	}
	return s, nil
}

type fakeRuleProjectResolver struct{ workspaceID string }

func (f *fakeRuleProjectResolver) GetWorkspaceID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return f.workspaceID, nil
}

type fakeRuleUserContactFinder struct {
	contact *repository.UserContact
}

func (f *fakeRuleUserContactFinder) FindUserContactByID(_ context.Context, _ string) (*repository.UserContact, error) {
	if f.contact == nil {
		return nil, errors.New("not found")
	}
	return f.contact, nil
}

func newTestRuleService(repo *fakeRuleRepo, role string, statuses map[string]*repository.CustomStatus, projectWorkspaceID string) *RuleService {
	return NewRuleService(repo, &fakeRuleRoleChecker{role: role}, &fakeRuleStatusChecker{byID: statuses}, &fakeRuleProjectResolver{workspaceID: projectWorkspaceID}, &fakeRuleUserContactFinder{}, nil, nil)
}

// fakeRuleTaskActions -- fake ruleTaskActions (S4W-11), dipakai menguji
// Evaluate/executeAction TANPA TaskService sungguhan (dependency
// melingkar TaskService<->RuleService bikin test lintas package tidak
// praktis -- interface kecil di sini cukup).
type fakeRuleTaskActions struct {
	setStatusErr error
	assignErr    error
	subtaskErr   error

	setStatusCalls []struct{ taskID, statusID string }
	assignCalls    []struct{ taskID, userID string }
	subtaskCalls   []struct{ projectID, parentTaskID, title string }
}

func (f *fakeRuleTaskActions) SetStatusForRule(_ context.Context, _ db.Executor, taskID, statusID, _, _ string) error {
	f.setStatusCalls = append(f.setStatusCalls, struct{ taskID, statusID string }{taskID, statusID})
	return f.setStatusErr
}
func (f *fakeRuleTaskActions) AssignUserForRule(_ context.Context, _ db.Executor, taskID, userID, _, _ string) error {
	f.assignCalls = append(f.assignCalls, struct{ taskID, userID string }{taskID, userID})
	return f.assignErr
}
func (f *fakeRuleTaskActions) CreateSubtaskForRule(_ context.Context, _ db.Executor, projectID, parentTaskID, title, _ string) error {
	f.subtaskCalls = append(f.subtaskCalls, struct{ projectID, parentTaskID, title string }{projectID, parentTaskID, title})
	return f.subtaskErr
}

// fakeRuleDueTaskLister -- fake ruleDueTaskLister (S4W-11).
type fakeRuleDueTaskLister struct {
	tasks []repository.Task
}

func (f *fakeRuleDueTaskLister) ListDueForWorkspace(_ context.Context, _ db.Executor, _ string, _ int) ([]repository.Task, error) {
	return f.tasks, nil
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestRuleService_Create_ForbiddenForNonAdmin(t *testing.T) {
	repo := &fakeRuleRepo{}
	svc := newTestRuleService(repo, "editor", nil, "")

	_, err := svc.Create(context.Background(), nil, "ws-1", "Rule Uji Coba",
		RuleTriggerInput{Event: "task_created"}, nil, RuleActionInput{Type: "create_subtask"}, "user-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want domain.ErrForbidden", err)
	}
	if len(repo.createCalls) != 0 {
		t.Errorf("createCalls = %d, want 0", len(repo.createCalls))
	}
}

func TestRuleService_Create_NameTooShort(t *testing.T) {
	repo := &fakeRuleRepo{}
	svc := newTestRuleService(repo, "admin_workspace", nil, "")

	_, err := svc.Create(context.Background(), nil, "ws-1", "AB",
		RuleTriggerInput{Event: "task_created"}, nil, RuleActionInput{Type: "create_subtask"}, "aw-1", "member")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want domain.ErrInvalidInput", err)
	}
}

func TestRuleService_Create_InvalidTriggerEvent(t *testing.T) {
	repo := &fakeRuleRepo{}
	svc := newTestRuleService(repo, "admin_workspace", nil, "")

	_, err := svc.Create(context.Background(), nil, "ws-1", "Rule Uji Coba",
		RuleTriggerInput{Event: "not_a_real_event"}, nil, RuleActionInput{Type: "create_subtask"}, "aw-1", "member")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want domain.ErrInvalidInput", err)
	}
}

// TestRuleService_Create_StatusUndefinedBlocked (US-049 AC: "Rule yang
// menggunakan status UNDEFINED sebagai trigger... tidak dapat disimpan").
func TestRuleService_Create_StatusUndefinedBlocked(t *testing.T) {
	repo := &fakeRuleRepo{}
	statuses := map[string]*repository.CustomStatus{
		"s1": {ID: "s1", ScopeType: "workspace", ScopeID: "ws-1", IsUndefined: true},
	}
	svc := newTestRuleService(repo, "admin_workspace", statuses, "")

	_, err := svc.Create(context.Background(), nil, "ws-1", "Rule Uji Coba",
		RuleTriggerInput{Event: "status_changed", StatusID: "s1"}, nil, RuleActionInput{Type: "create_subtask"}, "aw-1", "member")
	if !errors.Is(err, domain.ErrTaskStatusUndefined) {
		t.Errorf("err = %v, want domain.ErrTaskStatusUndefined", err)
	}
	if len(repo.createCalls) != 0 {
		t.Errorf("createCalls = %d, want 0", len(repo.createCalls))
	}
}

func TestRuleService_Create_StatusFromOtherWorkspaceRejected(t *testing.T) {
	repo := &fakeRuleRepo{}
	statuses := map[string]*repository.CustomStatus{
		"s1": {ID: "s1", ScopeType: "workspace", ScopeID: "ws-OTHER", IsUndefined: false},
	}
	svc := newTestRuleService(repo, "admin_workspace", statuses, "")

	_, err := svc.Create(context.Background(), nil, "ws-1", "Rule Uji Coba",
		RuleTriggerInput{Event: "status_changed", StatusID: "s1"}, nil, RuleActionInput{Type: "create_subtask"}, "aw-1", "member")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want domain.ErrInvalidInput", err)
	}
}

func TestRuleService_Create_ProjectConditionFromOtherWorkspaceRejected(t *testing.T) {
	repo := &fakeRuleRepo{}
	svc := newTestRuleService(repo, "admin_workspace", nil, "ws-OTHER")

	condition := &RuleConditionInput{Type: "project", ProjectID: "proj-1"}
	_, err := svc.Create(context.Background(), nil, "ws-1", "Rule Uji Coba",
		RuleTriggerInput{Event: "task_created"}, condition, RuleActionInput{Type: "create_subtask"}, "aw-1", "member")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want domain.ErrInvalidInput", err)
	}
}

func TestRuleService_Create_Success(t *testing.T) {
	repo := &fakeRuleRepo{}
	statuses := map[string]*repository.CustomStatus{
		"s1": {ID: "s1", ScopeType: "workspace", ScopeID: "ws-1", IsUndefined: false},
	}
	svc := newTestRuleService(repo, "admin_workspace", statuses, "")

	id, err := svc.Create(context.Background(), nil, "ws-1", "Auto-assign QA",
		RuleTriggerInput{Event: "status_changed", StatusID: "s1"}, nil, RuleActionInput{Type: "assign", TargetUserID: "user-2"}, "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == "" {
		t.Errorf("id kosong, want terisi")
	}
	if len(repo.createCalls) != 1 || repo.createCalls[0].workspaceID != "ws-1" {
		t.Errorf("createCalls = %+v, want satu entri workspaceID=ws-1", repo.createCalls)
	}
}

func TestRuleService_LoadForMutation_ForbiddenForEditor(t *testing.T) {
	repo := &fakeRuleRepo{byID: map[string]*repository.Rule{
		"r1": {ID: "r1", ScopeType: "workspace", ScopeID: "ws-1"},
	}}
	svc := newTestRuleService(repo, "editor", nil, "")

	if err := svc.SetActive(context.Background(), nil, "r1", false, "user-1", "member"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want domain.ErrForbidden", err)
	}
}

// TestRuleService_DeactivateForStatus_NotifiesEachCreator (US-053).
func TestRuleService_DeactivateForStatus_NotifiesEachCreator(t *testing.T) {
	repo := &fakeRuleRepo{deactivateResult: []repository.Rule{
		{ID: "r1", Name: "Rule A", CreatedBy: "user-1"},
		{ID: "r2", Name: "Rule B", CreatedBy: "user-2"},
	}}
	svc := NewRuleService(repo, &fakeRuleRoleChecker{}, &fakeRuleStatusChecker{}, &fakeRuleProjectResolver{},
		&fakeRuleUserContactFinder{contact: &repository.UserContact{Email: "a@example.com", DisplayName: "A"}}, nil, nil)

	if err := svc.DeactivateForStatus(context.Background(), stubExecutor{}, "s1", "MENUNGGU VENDOR", "aw-1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- S4W-11 Execution Engine ---

func TestConditionMatches(t *testing.T) {
	task := &repository.Task{ProjectID: "proj-1", Priority: "high", Assignees: []repository.TaskAssignee{{UserID: "user-1"}}}

	cases := []struct {
		name      string
		condition json.RawMessage
		want      bool
	}{
		{"kosong selalu cocok", nil, true},
		{"project cocok", mustMarshal(t, ruleConditionConfig{Type: "project", ProjectID: "proj-1"}), true},
		{"project tidak cocok", mustMarshal(t, ruleConditionConfig{Type: "project", ProjectID: "proj-OTHER"}), false},
		{"priority cocok", mustMarshal(t, ruleConditionConfig{Type: "priority", Priority: "high"}), true},
		{"priority tidak cocok", mustMarshal(t, ruleConditionConfig{Type: "priority", Priority: "low"}), false},
		{"assignee cocok", mustMarshal(t, ruleConditionConfig{Type: "assignee", UserID: "user-1"}), true},
		{"assignee tidak cocok", mustMarshal(t, ruleConditionConfig{Type: "assignee", UserID: "user-OTHER"}), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := conditionMatches(c.condition, task); got != c.want {
				t.Errorf("conditionMatches() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRuleService_Evaluate_ConditionMismatch_NoExecutionRecorded(t *testing.T) {
	repo := &fakeRuleRepo{activeForEvent: []repository.Rule{{
		ID: "r1", CreatedBy: "aw-1",
		ConditionConfig: mustMarshal(t, ruleConditionConfig{Type: "priority", Priority: "critical"}),
		ActionConfig:    mustMarshal(t, ruleActionConfig{Type: "assign", TargetUserID: "user-2"}),
	}}}
	actions := &fakeRuleTaskActions{}
	svc := NewRuleService(repo, &fakeRuleRoleChecker{}, &fakeRuleStatusChecker{}, &fakeRuleProjectResolver{}, &fakeRuleUserContactFinder{}, nil, nil)
	svc.SetTaskActions(actions)

	task := &repository.Task{ID: "task-1", Priority: "low"}
	svc.Evaluate(context.Background(), stubExecutor{}, "ws-1", "task_created", task, "user-1", "member")

	if len(repo.executionCalls) != 0 {
		t.Errorf("executionCalls = %v, want kosong (condition tidak cocok)", repo.executionCalls)
	}
	if len(actions.assignCalls) != 0 {
		t.Errorf("assignCalls = %v, want kosong", actions.assignCalls)
	}
}

func TestRuleService_Evaluate_ActionSuccess_RecordsCompleted(t *testing.T) {
	repo := &fakeRuleRepo{activeForEvent: []repository.Rule{{
		ID: "r1", Name: "Assign QA", CreatedBy: "aw-1",
		ActionConfig: mustMarshal(t, ruleActionConfig{Type: "assign", TargetUserID: "user-2"}),
	}}}
	actions := &fakeRuleTaskActions{}
	svc := NewRuleService(repo, &fakeRuleRoleChecker{}, &fakeRuleStatusChecker{}, &fakeRuleProjectResolver{}, &fakeRuleUserContactFinder{}, nil, nil)
	svc.SetTaskActions(actions)

	task := &repository.Task{ID: "task-1"}
	svc.Evaluate(context.Background(), stubExecutor{}, "ws-1", "task_created", task, "user-1", "member")

	if len(actions.assignCalls) != 1 || actions.assignCalls[0].taskID != "task-1" || actions.assignCalls[0].userID != "user-2" {
		t.Errorf("assignCalls = %+v, want satu entri task-1/user-2", actions.assignCalls)
	}
	if len(repo.executionCalls) != 1 || repo.executionCalls[0].status != "completed" {
		t.Errorf("executionCalls = %+v, want satu entri status=completed", repo.executionCalls)
	}
}

func TestRuleService_Evaluate_ActionFailure_RecordsFailed(t *testing.T) {
	repo := &fakeRuleRepo{activeForEvent: []repository.Rule{{
		ID: "r1", CreatedBy: "aw-1",
		ActionConfig: mustMarshal(t, ruleActionConfig{Type: "change_status", StatusID: "s-done"}),
	}}}
	actions := &fakeRuleTaskActions{setStatusErr: domain.ErrPicRequired}
	svc := NewRuleService(repo, &fakeRuleRoleChecker{}, &fakeRuleStatusChecker{}, &fakeRuleProjectResolver{}, &fakeRuleUserContactFinder{}, nil, nil)
	svc.SetTaskActions(actions)

	task := &repository.Task{ID: "task-1"}
	svc.Evaluate(context.Background(), stubExecutor{}, "ws-1", "status_changed", task, "user-1", "member")

	if len(repo.executionCalls) != 1 || repo.executionCalls[0].status != "failed" {
		t.Errorf("executionCalls = %+v, want satu entri status=failed", repo.executionCalls)
	}
}

func TestRuleService_RunDueDateCheck_DedupSkipsAlreadyExecuted(t *testing.T) {
	repo := &fakeRuleRepo{
		activeDueDateRules: []repository.Rule{{
			ID: "r1", ScopeID: "ws-1", CreatedBy: "aw-1",
			TriggerConfig: mustMarshal(t, ruleTriggerConfig{Event: "due_date_approaching", Days: 3}),
			ActionConfig:  mustMarshal(t, ruleActionConfig{Type: "assign", TargetUserID: "user-2"}),
		}},
		hasExecutionFor: map[string]bool{"r1|task-1": true},
	}
	actions := &fakeRuleTaskActions{}
	tasks := &fakeRuleDueTaskLister{tasks: []repository.Task{{ID: "task-1"}, {ID: "task-2"}}}
	svc := NewRuleService(repo, &fakeRuleRoleChecker{}, &fakeRuleStatusChecker{}, &fakeRuleProjectResolver{}, &fakeRuleUserContactFinder{}, nil, tasks)
	svc.SetTaskActions(actions)

	if err := svc.RunDueDateCheck(context.Background(), stubExecutor{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions.assignCalls) != 1 || actions.assignCalls[0].taskID != "task-2" {
		t.Errorf("assignCalls = %+v, want satu entri task-2 (task-1 sudah dedup)", actions.assignCalls)
	}
	if len(repo.executionCalls) != 1 {
		t.Errorf("executionCalls = %v, want satu entri", repo.executionCalls)
	}
}
