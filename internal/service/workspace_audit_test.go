package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeWorkspaceAuditRepo struct {
	entries []repository.WorkspaceAuditLogEntry
	actors  []repository.WorkspaceAuditActor

	listCalls []repository.WorkspaceAuditLogFilter
}

func (f *fakeWorkspaceAuditRepo) List(_ context.Context, _ db.Executor, filter repository.WorkspaceAuditLogFilter) ([]repository.WorkspaceAuditLogEntry, int, error) {
	f.listCalls = append(f.listCalls, filter)
	return f.entries, len(f.entries), nil
}

func (f *fakeWorkspaceAuditRepo) ListActors(_ context.Context, _ db.Executor, _ string) ([]repository.WorkspaceAuditActor, error) {
	return f.actors, nil
}

type fakeWorkspaceAuditRuleExecutions struct {
	list []repository.RuleExecution
	err  error
}

func (f *fakeWorkspaceAuditRuleExecutions) ListExecutions(_ context.Context, _ db.Executor, _, _ string) ([]repository.RuleExecution, error) {
	return f.list, f.err
}

type fakeWorkspaceAuditRoleChecker struct{ role string }

func (f *fakeWorkspaceAuditRoleChecker) GetMemberRole(_ context.Context, _ db.Executor, _, _ string) (string, error) {
	return f.role, nil
}

func newTestWorkspaceAuditService(repo *fakeWorkspaceAuditRepo, rules *fakeWorkspaceAuditRuleExecutions, role string) *WorkspaceAuditService {
	if repo == nil {
		repo = &fakeWorkspaceAuditRepo{}
	}
	if rules == nil {
		rules = &fakeWorkspaceAuditRuleExecutions{}
	}
	return NewWorkspaceAuditService(repo, rules, &fakeWorkspaceAuditRoleChecker{role: role})
}

func TestWorkspaceAuditList_ForbidsNonAdminWorkspace(t *testing.T) {
	svc := newTestWorkspaceAuditService(nil, nil, "editor")
	_, _, err := svc.List(context.Background(), nil, "ws-1", "user-1", "", "", 30, 10, 0, "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

func TestWorkspaceAuditList_AllowsAdminWorkspace(t *testing.T) {
	repo := &fakeWorkspaceAuditRepo{entries: []repository.WorkspaceAuditLogEntry{{ID: "log-1"}}}
	svc := newTestWorkspaceAuditService(repo, nil, "admin_workspace")
	entries, total, err := svc.List(context.Background(), nil, "ws-1", "aw-1", "", "", 30, 10, 0, "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 || len(entries) != 1 {
		t.Errorf("entries/total = %v/%d, want 1/1", entries, total)
	}
}

func TestWorkspaceAuditList_AllowsGroupAdminFullMode(t *testing.T) {
	svc := newTestWorkspaceAuditService(nil, nil, "editor")
	_, _, err := svc.List(context.Background(), nil, "ws-1", "ga-1", "", "", 30, 10, 0, "group_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWorkspaceAuditList_ClampsOutOfRangeLimit -- limit 0/negatif/>500
// harus jatuh ke default aman, bukan diteruskan mentah ke repository
// (potensi query tanpa batas).
func TestWorkspaceAuditList_ClampsOutOfRangeLimit(t *testing.T) {
	repo := &fakeWorkspaceAuditRepo{}
	svc := newTestWorkspaceAuditService(repo, nil, "admin_workspace")
	if _, _, err := svc.List(context.Background(), nil, "ws-1", "aw-1", "", "", 30, 0, -5, "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.listCalls) != 1 {
		t.Fatalf("listCalls = %d, want 1", len(repo.listCalls))
	}
	f := repo.listCalls[0]
	if f.Limit != 200 {
		t.Errorf("Limit = %d, want 200 (default aman)", f.Limit)
	}
	if f.Offset != 0 {
		t.Errorf("Offset = %d, want 0 (negatif dijepit)", f.Offset)
	}
}

func TestWorkspaceAuditExportCSV_UsesExportLimitNotPageLimit(t *testing.T) {
	repo := &fakeWorkspaceAuditRepo{}
	svc := newTestWorkspaceAuditService(repo, nil, "admin_workspace")
	if _, err := svc.ExportCSV(context.Background(), nil, "ws-1", "aw-1", "", "", 0, "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.listCalls) != 1 || repo.listCalls[0].Limit != WorkspaceAuditCSVExportLimit {
		t.Errorf("listCalls = %+v, want Limit = %d", repo.listCalls, WorkspaceAuditCSVExportLimit)
	}
}

func TestWorkspaceAuditExportCSV_ForbidsNonAdminWorkspace(t *testing.T) {
	svc := newTestWorkspaceAuditService(nil, nil, "project_manager")
	_, err := svc.ExportCSV(context.Background(), nil, "ws-1", "user-1", "", "", 30, "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden (PM bukan admin_workspace)", err)
	}
}

func TestWorkspaceAuditRuleExecutions_ForbidsNonAdminWorkspace(t *testing.T) {
	svc := newTestWorkspaceAuditService(nil, nil, "editor")
	_, err := svc.RuleExecutions(context.Background(), nil, "ws-1", "user-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

func TestWorkspaceAuditRuleExecutions_ReturnsListForAdminWorkspace(t *testing.T) {
	rules := &fakeWorkspaceAuditRuleExecutions{list: []repository.RuleExecution{{ID: "exec-1", RuleName: "Auto-assign"}}}
	svc := newTestWorkspaceAuditService(nil, rules, "admin_workspace")
	list, err := svc.RuleExecutions(context.Background(), nil, "ws-1", "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list) != 1 || list[0].RuleName != "Auto-assign" {
		t.Errorf("list = %+v, want 1 entry Auto-assign", list)
	}
}

func TestWorkspaceAuditListActors_ForbidsNonAdminWorkspace(t *testing.T) {
	svc := newTestWorkspaceAuditService(nil, nil, "viewer")
	_, err := svc.ListActors(context.Background(), nil, "ws-1", "user-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

func TestWorkspaceAuditList_RejectsEmptyWorkspaceID(t *testing.T) {
	svc := newTestWorkspaceAuditService(nil, nil, "admin_workspace")
	_, _, err := svc.List(context.Background(), nil, "", "aw-1", "", "", 30, 10, 0, "member")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want ErrInvalidInput", err)
	}
}
