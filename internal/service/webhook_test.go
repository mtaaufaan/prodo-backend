package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// fakeWebhookRepo -- pola sama fakeCustomStatusRepo. Fokus test di sini:
// cross-scope leakage (S4W-14 kickoff plan risk) -- CreateForWorkspace/
// loadForMutation/Dispatch harus benar-benar terpisah antara cakupan grup
// dan workspace.
type fakeWebhookRepo struct {
	byID map[string]*repository.Webhook

	createErr   error
	createCalls []struct {
		groupID, workspaceID string
		orgID, projectID     *string
	}

	activeForEvent          []repository.Webhook
	activeForEventWorkspace []repository.Webhook
}

func (f *fakeWebhookRepo) Create(_ context.Context, _ db.Executor, groupID, workspaceID string, orgID, projectID *string, _, _, _ string, _ []string, _, _ string) (string, error) {
	if f.createErr != nil {
		return "", f.createErr
	}
	f.createCalls = append(f.createCalls, struct {
		groupID, workspaceID string
		orgID, projectID     *string
	}{groupID, workspaceID, orgID, projectID})
	return "new-webhook", nil
}

func (f *fakeWebhookRepo) Get(_ context.Context, _ db.Executor, webhookID string) (*repository.Webhook, error) {
	w, ok := f.byID[webhookID]
	if !ok {
		return nil, domain.ErrWebhookNotFound
	}
	return w, nil
}

func (f *fakeWebhookRepo) ListByGroup(_ context.Context, _ db.Executor, _ string) ([]repository.Webhook, error) {
	return nil, nil
}
func (f *fakeWebhookRepo) ListByWorkspace(_ context.Context, _ db.Executor, _ string) ([]repository.Webhook, error) {
	return nil, nil
}
func (f *fakeWebhookRepo) ListActiveForEvent(_ context.Context, _ db.Executor, _, _, _ string) ([]repository.Webhook, error) {
	return f.activeForEvent, nil
}
func (f *fakeWebhookRepo) ListActiveForEventWorkspace(_ context.Context, _ db.Executor, _, _, _ string) ([]repository.Webhook, error) {
	return f.activeForEventWorkspace, nil
}
func (f *fakeWebhookRepo) DecryptSecret(_ context.Context, _ db.Executor, _ string) (string, error) {
	return "secret", nil
}
func (f *fakeWebhookRepo) Update(_ context.Context, _ db.Executor, _, _, _ string, _, _ *string, _ []string, _, _ string, _ *repository.Webhook) error {
	return nil
}
func (f *fakeWebhookRepo) SetActive(_ context.Context, _ db.Executor, _ string, _ bool, _, _ string, _ *repository.Webhook) error {
	return nil
}
func (f *fakeWebhookRepo) RegenerateSecret(_ context.Context, _ db.Executor, _, _, _, _ string, _ *repository.Webhook) error {
	return nil
}
func (f *fakeWebhookRepo) Delete(_ context.Context, _ db.Executor, _, _, _ string, _ *repository.Webhook) error {
	return nil
}
func (f *fakeWebhookRepo) CreateDelivery(_ context.Context, _ db.Executor, _, _ string, _ []byte, _ int, _ string, _ *int, _, _ *string, _ int) error {
	return nil
}
func (f *fakeWebhookRepo) ListDeliveries(_ context.Context, _ db.Executor, _, _, _ string) ([]repository.WebhookDelivery, error) {
	return nil, nil
}
func (f *fakeWebhookRepo) ListDeliveriesForWorkspace(_ context.Context, _ db.Executor, _, _, _ string) ([]repository.WebhookDelivery, error) {
	return nil, nil
}
func (f *fakeWebhookRepo) GroupAdminContacts(_ context.Context, _ db.Executor, _ string) ([]repository.GroupAdminContact, error) {
	return nil, nil
}
func (f *fakeWebhookRepo) WorkspaceAdminContacts(_ context.Context, _ db.Executor, _ string) ([]repository.GroupAdminContact, error) {
	return nil, nil
}

type fakeWebhookGroupAuthorizer struct {
	isGA  bool
	group string
}

func (f *fakeWebhookGroupAuthorizer) IsGroupAdminOfGroup(_ context.Context, _ db.Executor, _, _ string) (bool, error) {
	return f.isGA, nil
}
func (f *fakeWebhookGroupAuthorizer) GetGroupID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return f.group, nil
}

type fakeWebhookRoleChecker struct{ role string }

func (f *fakeWebhookRoleChecker) GetMemberRole(_ context.Context, _ db.Executor, _, _ string) (string, error) {
	return f.role, nil
}

type fakeWebhookProjectResolver struct{ workspaceID string }

func (f *fakeWebhookProjectResolver) GetWorkspaceID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return f.workspaceID, nil
}

type fakeWebhookEnqueuer struct {
	calls []struct{ webhookID, eventType string }
}

func (f *fakeWebhookEnqueuer) Enqueue(_ context.Context, webhookID, eventType string, _ []byte) error {
	f.calls = append(f.calls, struct{ webhookID, eventType string }{webhookID, eventType})
	return nil
}

func newTestWebhookService(repo *fakeWebhookRepo, role string, projectWorkspaceID string) *WebhookService {
	return NewWebhookService(repo, &fakeWebhookGroupAuthorizer{}, &fakeWebhookRoleChecker{role: role}, &fakeWebhookProjectResolver{workspaceID: projectWorkspaceID}, &fakeWebhookEnqueuer{}, nil, nil)
}

func TestWebhookService_CreateForWorkspace_ForbiddenForNonAdmin(t *testing.T) {
	repo := &fakeWebhookRepo{}
	svc := newTestWebhookService(repo, "editor", "")

	_, _, err := svc.CreateForWorkspace(context.Background(), nil, "ws-1", "", "Notif Ops", "https://hooks.example.com/x", []string{"project.created"}, "user-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want domain.ErrForbidden", err)
	}
	if len(repo.createCalls) != 0 {
		t.Errorf("createCalls = %d, want 0 (ditolak sebelum repo terpanggil)", len(repo.createCalls))
	}
}

func TestWebhookService_CreateForWorkspace_Success(t *testing.T) {
	repo := &fakeWebhookRepo{}
	svc := newTestWebhookService(repo, "admin_workspace", "")

	id, secret, err := svc.CreateForWorkspace(context.Background(), nil, "ws-1", "", "Notif Ops", "https://hooks.example.com/x", []string{"project.created"}, "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == "" || secret == "" {
		t.Errorf("id/secret kosong, want terisi")
	}
	if len(repo.createCalls) != 1 {
		t.Fatalf("createCalls = %d, want 1", len(repo.createCalls))
	}
	call := repo.createCalls[0]
	if call.workspaceID != "ws-1" || call.groupID != "" {
		t.Errorf("call = %+v, want workspaceID=ws-1 groupID='' (TIDAK boleh menulis ke cakupan grup)", call)
	}
	if call.projectID != nil {
		t.Errorf("projectID = %v, want nil (LINGKUP Seluruh workspace)", call.projectID)
	}
}

// TestWebhookService_CreateForWorkspace_ProjectScopeMismatch -- LINGKUP
// "Project X" wajib divalidasi milik workspaceID yang sama (kickoff plan:
// risiko cross-scope kalau kondisi WHERE keliru).
func TestWebhookService_CreateForWorkspace_ProjectScopeMismatch(t *testing.T) {
	repo := &fakeWebhookRepo{}
	svc := newTestWebhookService(repo, "admin_workspace", "ws-OTHER")

	_, _, err := svc.CreateForWorkspace(context.Background(), nil, "ws-1", "proj-1", "Notif Ops", "https://hooks.example.com/x", []string{"project.created"}, "aw-1", "member")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want domain.ErrInvalidInput (project bukan milik workspace ini)", err)
	}
	if len(repo.createCalls) != 0 {
		t.Errorf("createCalls = %d, want 0", len(repo.createCalls))
	}
}

// TestWebhookService_LoadForMutation_WorkspaceScope_ForbiddenForEditor --
// webhook cakupan workspace mewajibkan role admin_workspace di RESOLUSI
// otorisasi (bukan authorizeGroup), walau actor kebetulan group_admin di
// grup lain manapun.
func TestWebhookService_LoadForMutation_WorkspaceScope_ForbiddenForEditor(t *testing.T) {
	wsID := "ws-1"
	repo := &fakeWebhookRepo{byID: map[string]*repository.Webhook{
		"wh-1": {ID: "wh-1", WorkspaceID: &wsID},
	}}
	svc := newTestWebhookService(repo, "editor", "")

	_, err := svc.loadForMutation(context.Background(), nil, "wh-1", "user-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want domain.ErrForbidden", err)
	}
}

func TestWebhookService_LoadForMutation_WorkspaceScope_AllowsAdminWorkspace(t *testing.T) {
	wsID := "ws-1"
	repo := &fakeWebhookRepo{byID: map[string]*repository.Webhook{
		"wh-1": {ID: "wh-1", WorkspaceID: &wsID},
	}}
	svc := newTestWebhookService(repo, "admin_workspace", "")

	w, err := svc.loadForMutation(context.Background(), nil, "wh-1", "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.ID != "wh-1" {
		t.Errorf("w.ID = %q, want wh-1", w.ID)
	}
}

// TestWebhookService_LoadForMutation_GroupScope_ForbiddenWhenNotGA -- baris
// group-scope TIDAK boleh lolos lewat cabang authorizeWorkspace.
func TestWebhookService_LoadForMutation_GroupScope_ForbiddenWhenNotGA(t *testing.T) {
	groupID := "group-1"
	repo := &fakeWebhookRepo{byID: map[string]*repository.Webhook{
		"wh-1": {ID: "wh-1", GroupID: &groupID},
	}}
	svc := NewWebhookService(repo, &fakeWebhookGroupAuthorizer{isGA: false}, &fakeWebhookRoleChecker{role: "admin_workspace"}, &fakeWebhookProjectResolver{}, &fakeWebhookEnqueuer{}, nil, nil)

	_, err := svc.loadForMutation(context.Background(), nil, "wh-1", "user-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want domain.ErrForbidden (bukan GA grup pemilik)", err)
	}
}

// TestWebhookService_UpdateForWorkspace_ProjectScopeMismatch -- sama pola
// CreateForWorkspace, mencegah project dari workspace lain dipasang sebagai
// LINGKUP webhook ini lewat Kelola.
func TestWebhookService_UpdateForWorkspace_ProjectScopeMismatch(t *testing.T) {
	wsID := "ws-1"
	repo := &fakeWebhookRepo{byID: map[string]*repository.Webhook{
		"wh-1": {ID: "wh-1", WorkspaceID: &wsID, Name: "Lama", TargetURL: "https://old.example.com"},
	}}
	svc := newTestWebhookService(repo, "admin_workspace", "ws-OTHER")

	err := svc.UpdateForWorkspace(context.Background(), nil, "wh-1", "proj-1", "Nama Baru", "https://hooks.example.com/x", []string{"project.created"}, "aw-1", "member")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want domain.ErrInvalidInput (project bukan milik workspace ini)", err)
	}
}

func TestWebhookService_UpdateForWorkspace_ForbiddenForEditor(t *testing.T) {
	wsID := "ws-1"
	repo := &fakeWebhookRepo{byID: map[string]*repository.Webhook{
		"wh-1": {ID: "wh-1", WorkspaceID: &wsID, Name: "Lama", TargetURL: "https://old.example.com"},
	}}
	svc := newTestWebhookService(repo, "editor", "")

	err := svc.UpdateForWorkspace(context.Background(), nil, "wh-1", "", "Nama Baru", "https://hooks.example.com/x", []string{"project.created"}, "user-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want domain.ErrForbidden", err)
	}
}

// TestWebhookService_Dispatch_FiresBothScopes -- satu event project.* boleh
// memicu webhook grup DAN webhook workspace sekaligus, dua cakupan
// independen (S4W-14).
func TestWebhookService_Dispatch_FiresBothScopes(t *testing.T) {
	repo := &fakeWebhookRepo{
		activeForEvent:          []repository.Webhook{{ID: "wh-group"}},
		activeForEventWorkspace: []repository.Webhook{{ID: "wh-ws"}},
	}
	enqueuer := &fakeWebhookEnqueuer{}
	svc := NewWebhookService(repo, &fakeWebhookGroupAuthorizer{group: "group-1"}, &fakeWebhookRoleChecker{}, &fakeWebhookProjectResolver{}, enqueuer, nil, nil)

	if err := svc.Dispatch(context.Background(), nil, "org-1", "ws-1", "proj-1", "project.created", map[string]any{"id": "proj-1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(enqueuer.calls) != 2 {
		t.Fatalf("calls = %d, want 2 (grup + workspace)", len(enqueuer.calls))
	}
}

// TestWebhookService_Dispatch_WorkspaceOptional -- workspaceID kosong (mis.
// dipanggil dari jalur non-project) tidak boleh memanggil
// ListActiveForEventWorkspace sama sekali.
func TestWebhookService_Dispatch_WorkspaceOptional(t *testing.T) {
	repo := &fakeWebhookRepo{activeForEvent: []repository.Webhook{{ID: "wh-group"}}}
	enqueuer := &fakeWebhookEnqueuer{}
	svc := NewWebhookService(repo, &fakeWebhookGroupAuthorizer{group: "group-1"}, &fakeWebhookRoleChecker{}, &fakeWebhookProjectResolver{}, enqueuer, nil, nil)

	if err := svc.Dispatch(context.Background(), nil, "org-1", "", "", "project.created", map[string]any{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(enqueuer.calls) != 1 {
		t.Fatalf("calls = %d, want 1 (cuma grup)", len(enqueuer.calls))
	}
}
