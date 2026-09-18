package service

import (
	"context"
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
	return NewRuleService(repo, &fakeRuleRoleChecker{role: role}, &fakeRuleStatusChecker{byID: statuses}, &fakeRuleProjectResolver{workspaceID: projectWorkspaceID}, &fakeRuleUserContactFinder{}, nil)
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
		&fakeRuleUserContactFinder{contact: &repository.UserContact{Email: "a@example.com", DisplayName: "A"}}, nil)

	if err := svc.DeactivateForStatus(context.Background(), stubExecutor{}, "s1", "MENUNGGU VENDOR", "aw-1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
