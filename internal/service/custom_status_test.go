package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeCustomStatusRepo struct {
	listResult []repository.CustomStatus
	listErr    error

	getByID map[string]*repository.CustomStatus
	getErr  error

	nameExistsResult bool
	nameExistsErr    error

	createErr   error
	createCalls []struct {
		workspaceID, name, colorToken string
		position                      int
	}

	updateNameColorErr   error
	updateNameColorCalls []struct{ statusID, name, colorToken string }

	moveErr   error
	moveCalls []struct {
		workspaceID, statusID string
		direction             int
	}

	setUndefinedErr   error
	setUndefinedCalls []struct {
		statusID  string
		undefined bool
	}

	setRequireErr   error
	setRequireCalls []struct {
		statusID string
		require  bool
	}
}

func (f *fakeCustomStatusRepo) ListForWorkspace(_ context.Context, _ db.Executor, _ string) ([]repository.CustomStatus, error) {
	return f.listResult, f.listErr
}

func (f *fakeCustomStatusRepo) Get(_ context.Context, _ db.Executor, statusID string) (*repository.CustomStatus, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	s, ok := f.getByID[statusID]
	if !ok {
		return nil, domain.ErrCustomStatusNotFound
	}
	return s, nil
}

func (f *fakeCustomStatusRepo) NameExists(_ context.Context, _ db.Executor, _, _, _ string) (bool, error) {
	return f.nameExistsResult, f.nameExistsErr
}

func (f *fakeCustomStatusRepo) Create(_ context.Context, _ db.Executor, workspaceID, name, colorToken string, position int, _, _ string) (*repository.CustomStatus, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.createCalls = append(f.createCalls, struct {
		workspaceID, name, colorToken string
		position                      int
	}{workspaceID, name, colorToken, position})
	return &repository.CustomStatus{ID: "new-status", ScopeType: "workspace", ScopeID: workspaceID, Name: name, ColorToken: &colorToken, Position: position}, nil
}

func (f *fakeCustomStatusRepo) UpdateNameColor(_ context.Context, _ db.Executor, statusID, name, colorToken, _, _ string) error {
	if f.updateNameColorErr != nil {
		return f.updateNameColorErr
	}
	f.updateNameColorCalls = append(f.updateNameColorCalls, struct{ statusID, name, colorToken string }{statusID, name, colorToken})
	return nil
}

func (f *fakeCustomStatusRepo) Move(_ context.Context, _ db.Executor, workspaceID, statusID string, direction int, _, _ string) error {
	if f.moveErr != nil {
		return f.moveErr
	}
	f.moveCalls = append(f.moveCalls, struct {
		workspaceID, statusID string
		direction             int
	}{workspaceID, statusID, direction})
	return nil
}

func (f *fakeCustomStatusRepo) SetUndefined(_ context.Context, _ db.Executor, statusID string, undefined bool, _, _ string) error {
	if f.setUndefinedErr != nil {
		return f.setUndefinedErr
	}
	f.setUndefinedCalls = append(f.setUndefinedCalls, struct {
		statusID  string
		undefined bool
	}{statusID, undefined})
	return nil
}

func (f *fakeCustomStatusRepo) SetRequireStartConfirmation(_ context.Context, _ db.Executor, statusID string, require bool, _, _ string) error {
	if f.setRequireErr != nil {
		return f.setRequireErr
	}
	f.setRequireCalls = append(f.setRequireCalls, struct {
		statusID string
		require  bool
	}{statusID, require})
	return nil
}

type fakeCustomStatusRoleChecker struct {
	role string
	err  error
}

func (f *fakeCustomStatusRoleChecker) GetMemberRole(_ context.Context, _ db.Executor, _, _ string) (string, error) {
	return f.role, f.err
}

func fullList12() []repository.CustomStatus {
	list := make([]repository.CustomStatus, 12)
	for i := range list {
		list[i] = repository.CustomStatus{ID: "s" + string(rune('a'+i))}
	}
	return list
}

func TestCustomStatusService_Create_ValidationErrors(t *testing.T) {
	cases := []struct {
		name       string
		statusName string
		color      string
		list       []repository.CustomStatus
		nameTaken  bool
	}{
		{"nama terlalu pendek", "AB", "signal", nil, false},
		{"nama terlalu panjang", "ABCDEFGHIJKLMNOPQRSTUVWXY", "signal", nil, false},
		{"warna tidak dikenal", "MENUNGGU VENDOR", "oklch-random", nil, false},
		{"nama sudah dipakai", "MENUNGGU VENDOR", "signal", nil, true},
		{"batas 12 status tercapai", "MENUNGGU VENDOR", "signal", fullList12(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeCustomStatusRepo{listResult: tc.list, nameExistsResult: tc.nameTaken}
			svc := NewCustomStatusService(repo, &fakeCustomStatusRoleChecker{role: "admin_workspace"})
			_, err := svc.Create(context.Background(), nil, "ws-1", tc.statusName, tc.color, 0, "aw-1", "member")
			if !errors.Is(err, domain.ErrInvalidInput) && !errors.Is(err, domain.ErrCustomStatusNameTaken) && !errors.Is(err, domain.ErrCustomStatusLimitReached) {
				t.Errorf("err = %v, want validation error", err)
			}
			if len(repo.createCalls) != 0 {
				t.Errorf("createCalls = %d, want 0 (ditolak sebelum repo terpanggil)", len(repo.createCalls))
			}
		})
	}
}

func TestCustomStatusService_Create_Success(t *testing.T) {
	repo := &fakeCustomStatusRepo{listResult: []repository.CustomStatus{{ID: "s1"}, {ID: "s2"}}}
	svc := NewCustomStatusService(repo, &fakeCustomStatusRoleChecker{role: "admin_workspace"})

	created, err := svc.Create(context.Background(), nil, "ws-1", "menunggu vendor", "signal", 1, "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.Name != "MENUNGGU VENDOR" {
		t.Errorf("Name = %q, want uppercased MENUNGGU VENDOR", created.Name)
	}
	if len(repo.createCalls) != 1 || repo.createCalls[0].position != 0 {
		t.Errorf("createCalls = %+v, want posisi 0 (input 1-indexed diubah ke 0-indexed)", repo.createCalls)
	}
}

func TestCustomStatusService_Create_ForbiddenForNonAdmin(t *testing.T) {
	for _, role := range []string{"project_manager", "editor"} {
		t.Run(role, func(t *testing.T) {
			repo := &fakeCustomStatusRepo{}
			svc := NewCustomStatusService(repo, &fakeCustomStatusRoleChecker{role: role})
			_, err := svc.Create(context.Background(), nil, "ws-1", "MENUNGGU VENDOR", "signal", 0, "user-1", "member")
			if !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("err = %v, want domain.ErrForbidden (Create AW-only, %s tidak boleh)", err, role)
			}
		})
	}
}

func TestCustomStatusService_UpdateNameColor_SystemNameLocked(t *testing.T) {
	repo := &fakeCustomStatusRepo{getByID: map[string]*repository.CustomStatus{
		"s1": {ID: "s1", ScopeID: "ws-1", Name: "BACKLOG", IsSystem: true},
	}}
	svc := NewCustomStatusService(repo, &fakeCustomStatusRoleChecker{role: "admin_workspace"})

	if err := svc.UpdateNameColor(context.Background(), nil, "s1", "NAMA BARU", "mint", "aw-1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.updateNameColorCalls) != 1 || repo.updateNameColorCalls[0].name != "BACKLOG" {
		t.Errorf("updateNameColorCalls = %+v, want name tetap BACKLOG (dikunci, mengabaikan input)", repo.updateNameColorCalls)
	}
}

func TestCustomStatusService_UpdateNameColor_CustomValidatesName(t *testing.T) {
	repo := &fakeCustomStatusRepo{getByID: map[string]*repository.CustomStatus{
		"s1": {ID: "s1", ScopeID: "ws-1", Name: "LAMA", IsSystem: false},
	}}
	svc := NewCustomStatusService(repo, &fakeCustomStatusRoleChecker{role: "admin_workspace"})

	err := svc.UpdateNameColor(context.Background(), nil, "s1", "AB", "mint", "aw-1", "member")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want domain.ErrInvalidInput (nama < 3 karakter)", err)
	}
	if len(repo.updateNameColorCalls) != 0 {
		t.Errorf("updateNameColorCalls = %d, want 0", len(repo.updateNameColorCalls))
	}
}

func TestCustomStatusService_Move_InvalidDirection(t *testing.T) {
	repo := &fakeCustomStatusRepo{getByID: map[string]*repository.CustomStatus{"s1": {ID: "s1", ScopeID: "ws-1"}}}
	svc := NewCustomStatusService(repo, &fakeCustomStatusRoleChecker{role: "admin_workspace"})

	if err := svc.Move(context.Background(), nil, "s1", 2, "aw-1", "member"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want domain.ErrInvalidInput", err)
	}
}

func TestCustomStatusService_Undefine_SystemBlocked(t *testing.T) {
	repo := &fakeCustomStatusRepo{getByID: map[string]*repository.CustomStatus{
		"s1": {ID: "s1", ScopeID: "ws-1", Name: "DONE", IsSystem: true},
	}}
	svc := NewCustomStatusService(repo, &fakeCustomStatusRoleChecker{role: "admin_workspace"})

	if err := svc.Undefine(context.Background(), nil, "s1", "aw-1", "member"); !errors.Is(err, domain.ErrCustomStatusIsSystem) {
		t.Errorf("err = %v, want domain.ErrCustomStatusIsSystem", err)
	}
	if len(repo.setUndefinedCalls) != 0 {
		t.Errorf("setUndefinedCalls = %d, want 0", len(repo.setUndefinedCalls))
	}
}

func TestCustomStatusService_Undefine_Success(t *testing.T) {
	repo := &fakeCustomStatusRepo{getByID: map[string]*repository.CustomStatus{
		"s1": {ID: "s1", ScopeID: "ws-1", Name: "MENUNGGU VENDOR", IsSystem: false},
	}}
	svc := NewCustomStatusService(repo, &fakeCustomStatusRoleChecker{role: "admin_workspace"})

	if err := svc.Undefine(context.Background(), nil, "s1", "aw-1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.setUndefinedCalls) != 1 || !repo.setUndefinedCalls[0].undefined {
		t.Errorf("setUndefinedCalls = %+v, want satu entri undefined=true", repo.setUndefinedCalls)
	}
}

func TestCustomStatusService_Restore_NotUndefinedBlocked(t *testing.T) {
	repo := &fakeCustomStatusRepo{getByID: map[string]*repository.CustomStatus{
		"s1": {ID: "s1", ScopeID: "ws-1", IsUndefined: false},
	}}
	svc := NewCustomStatusService(repo, &fakeCustomStatusRoleChecker{role: "admin_workspace"})

	if err := svc.Restore(context.Background(), nil, "s1", "aw-1", "member"); !errors.Is(err, domain.ErrCustomStatusNotUndefined) {
		t.Errorf("err = %v, want domain.ErrCustomStatusNotUndefined", err)
	}
}

func TestCustomStatusService_Restore_Success(t *testing.T) {
	repo := &fakeCustomStatusRepo{getByID: map[string]*repository.CustomStatus{
		"s1": {ID: "s1", ScopeID: "ws-1", IsUndefined: true},
	}}
	svc := NewCustomStatusService(repo, &fakeCustomStatusRoleChecker{role: "admin_workspace"})

	if err := svc.Restore(context.Background(), nil, "s1", "aw-1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.setUndefinedCalls) != 1 || repo.setUndefinedCalls[0].undefined {
		t.Errorf("setUndefinedCalls = %+v, want satu entri undefined=false", repo.setUndefinedCalls)
	}
}

// TestCustomStatusService_SetRequireStartConfirmation_UntrackedBlocked --
// susulan S4W-05: BACKLOG/DONE/BLOCKED tidak bisa diaktifkan konfirmasi
// mulainya, sebelumnya cuma ditegakkan di UI prototype.
func TestCustomStatusService_SetRequireStartConfirmation_UntrackedBlocked(t *testing.T) {
	repo := &fakeCustomStatusRepo{getByID: map[string]*repository.CustomStatus{
		"s1": {ID: "s1", ScopeID: "ws-1", Name: "BACKLOG", IsSystem: true},
	}}
	svc := NewCustomStatusService(repo, &fakeCustomStatusRoleChecker{role: "admin_workspace"})

	err := svc.SetRequireStartConfirmation(context.Background(), nil, "s1", true, "aw-1", "member")
	if !errors.Is(err, domain.ErrCustomStatusNotTrackable) {
		t.Errorf("err = %v, want domain.ErrCustomStatusNotTrackable", err)
	}
	if len(repo.setRequireCalls) != 0 {
		t.Errorf("setRequireCalls = %d, want 0", len(repo.setRequireCalls))
	}
}

// TestCustomStatusService_SetRequireStartConfirmation_AllowsPM -- perilaku
// existing (Phase 4) TIDAK diubah S4W-05: PM tetap boleh, beda dari
// authorizeAdmin (AW-only) yang dipakai CRUD template baru.
func TestCustomStatusService_SetRequireStartConfirmation_AllowsPM(t *testing.T) {
	repo := &fakeCustomStatusRepo{getByID: map[string]*repository.CustomStatus{
		"s1": {ID: "s1", ScopeID: "ws-1", Name: "IN PROGRESS", IsSystem: true},
	}}
	svc := NewCustomStatusService(repo, &fakeCustomStatusRoleChecker{role: "project_manager"})

	if err := svc.SetRequireStartConfirmation(context.Background(), nil, "s1", true, "pm-1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.setRequireCalls) != 1 {
		t.Errorf("setRequireCalls = %d, want 1", len(repo.setRequireCalls))
	}
}

func TestCustomStatusService_SetRequireStartConfirmation_ForbiddenForEditor(t *testing.T) {
	repo := &fakeCustomStatusRepo{getByID: map[string]*repository.CustomStatus{
		"s1": {ID: "s1", ScopeID: "ws-1", Name: "IN PROGRESS", IsSystem: true},
	}}
	svc := NewCustomStatusService(repo, &fakeCustomStatusRoleChecker{role: "editor"})

	if err := svc.SetRequireStartConfirmation(context.Background(), nil, "s1", true, "user-1", "member"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want domain.ErrForbidden", err)
	}
}
