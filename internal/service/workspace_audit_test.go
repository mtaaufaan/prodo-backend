package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeWorkspaceAuditRepo struct {
	entries []repository.WorkspaceAuditLogEntry
	actors  []repository.WorkspaceAuditActor

	listCalls []repository.WorkspaceAuditLogFilter
	exports   []string // "<scope>:<rows>" -- ekspor yang dicatat ke audit trail
}

func (f *fakeWorkspaceAuditRepo) List(_ context.Context, _ db.Executor, filter *repository.WorkspaceAuditLogFilter) ([]repository.WorkspaceAuditLogEntry, int, error) {
	f.listCalls = append(f.listCalls, *filter)
	return f.entries, len(f.entries), nil
}

func (f *fakeWorkspaceAuditRepo) LogExport(_ context.Context, _ db.Executor, _, role, _, projectID, _ string, rows int) error {
	scope := "workspace"
	if projectID != "" {
		scope = "project"
	}
	f.exports = append(f.exports, fmt.Sprintf("%s:%s:%d", scope, role, rows))
	return nil
}

func (f *fakeWorkspaceAuditRepo) ListProjectActors(_ context.Context, _ db.Executor, _ string) ([]repository.WorkspaceAuditActor, error) {
	return f.actors, nil
}

func (f *fakeWorkspaceAuditRepo) ProjectInfo(_ context.Context, _ db.Executor, _ string) (workspaceID, name string, err error) {
	return "ws-1", "PRODO", nil
}

func (f *fakeWorkspaceAuditRepo) ListActors(_ context.Context, _ db.Executor, _ string) ([]repository.WorkspaceAuditActor, error) {
	return f.actors, nil
}

type fakeWorkspaceAuditRuleExecutions struct {
	list []repository.RuleExecution
	err  error
}

func (f *fakeWorkspaceAuditRuleExecutions) ListExecutions(_ context.Context, _ db.Executor, _, _, _ string) ([]repository.RuleExecution, error) {
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

// ---- Audit Trail workspace: ekspor dicatat (IG-130) ----

func TestWorkspaceAuditExportCSV_LogsTheExportItself(t *testing.T) {
	repo := &fakeWorkspaceAuditRepo{entries: []repository.WorkspaceAuditLogEntry{{ID: "a"}, {ID: "b"}}}
	svc := newTestWorkspaceAuditService(repo, nil, "admin_workspace")
	if _, err := svc.ExportCSV(context.Background(), nil, "ws-1", "aw-1", "", "", 30, "member"); err != nil {
		t.Fatal(err)
	}
	if len(repo.exports) != 1 || repo.exports[0] != "workspace:admin_workspace:2" {
		t.Fatalf("ekspor harus tercatat sekali dengan role efektif: %v", repo.exports)
	}
}

// ---- ProjectAuditService (Audit Trail PM, IG-130) ----

func newTestProjectAuditService(repo *fakeWorkspaceAuditRepo, pmID, wsRole string) *ProjectAuditService {
	if repo == nil {
		repo = &fakeWorkspaceAuditRepo{}
	}
	return NewProjectAuditService(repo, &fakeWorkspaceAuditRuleExecutions{}, &fakePerformancePMChecker{pmUserID: pmID}, &fakeWorkspaceAuditRoleChecker{role: wsRole})
}

func TestProjectAudit_AccessGate(t *testing.T) {
	ctx := context.Background()
	svc := newTestProjectAuditService(nil, "pm-1", "editor")
	if _, _, err := svc.List(ctx, nil, "p1", "pm-1", "", "", 30, 10, 0, "member"); err != nil {
		t.Fatalf("PM project harus boleh: %v", err)
	}
	if _, _, err := svc.List(ctx, nil, "p1", "x", "", "", 30, 10, 0, "group_admin"); err != nil {
		t.Fatalf("GA harus boleh: %v", err)
	}
	if _, _, err := svc.List(ctx, nil, "p1", "x", "", "", 30, 10, 0, "member"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("editor non-PM harus ditolak, got %v", err)
	}
	aw := newTestProjectAuditService(nil, "pm-1", "admin_workspace")
	if _, _, err := aw.List(ctx, nil, "p1", "x", "", "", 30, 10, 0, "member"); err != nil {
		t.Fatalf("AW harus boleh: %v", err)
	}
	if _, _, err := svc.List(ctx, nil, "", "pm-1", "", "", 30, 10, 0, "member"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("project kosong harus invalid, got %v", err)
	}
	if _, err := svc.ListActors(ctx, nil, "p1", "x", "member"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("aktor juga digerbangi, got %v", err)
	}
	if _, err := svc.RuleExecutions(ctx, nil, "p1", "x", "member"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("log rule juga digerbangi, got %v", err)
	}
}

func TestProjectAudit_FiltersByProjectAndLogsExport(t *testing.T) {
	repo := &fakeWorkspaceAuditRepo{entries: []repository.WorkspaceAuditLogEntry{{ID: "a"}}}
	svc := newTestProjectAuditService(repo, "pm-1", "editor")
	if _, _, err := svc.List(context.Background(), nil, "p1", "pm-1", "actor-9", "UPDATE", 7, 10, 0, "member"); err != nil {
		t.Fatal(err)
	}
	f := repo.listCalls[0]
	if f.ProjectID != "p1" || f.WorkspaceID != "" || f.ActorID != "actor-9" || f.ActionType != "UPDATE" || f.Days != 7 {
		t.Fatalf("filter list = %+v", f)
	}
	if _, err := svc.ExportCSV(context.Background(), nil, "p1", "pm-1", "", "", 30, "member"); err != nil {
		t.Fatal(err)
	}
	if repo.listCalls[1].Limit != WorkspaceAuditCSVExportLimit {
		t.Fatalf("ekspor harus memakai batas ekspor, got %d", repo.listCalls[1].Limit)
	}
	if len(repo.exports) != 1 || repo.exports[0] != "project:project_manager:1" {
		t.Fatalf("ekspor project harus tercatat dengan role efektif PM: %v", repo.exports)
	}
	// ditolak -> tidak tercatat
	if _, err := newTestProjectAuditService(repo, "pm-1", "editor").ExportCSV(context.Background(), nil, "p1", "x", "", "", 30, "member"); !errors.Is(err, domain.ErrForbidden) || len(repo.exports) != 1 {
		t.Fatalf("ekspor non-PM harus ditolak tanpa audit: err=%v exports=%v", err, repo.exports)
	}
}
