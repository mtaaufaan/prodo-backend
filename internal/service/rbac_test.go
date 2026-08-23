package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type stubWorkspaceMemberRepository struct {
	getRoleResult string
	getRoleErr    error
	getRoleCalls  int

	assignErr          error
	assignedRole       string
	assignedInvitedBy  *string
	assignedBefore     map[string]string
	assignedAfter      map[string]string
	assignedNotifTitle string
	assignedNotifBody  string

	listMembersResult []repository.Member
	listMembersErr    error

	orgID    string
	orgIDErr error

	removeErr        error
	removedUserID    string
	removedWorkspace string
}

func (f *stubWorkspaceMemberRepository) GetRole(_ context.Context, _ db.Executor, _, _ string) (string, error) {
	f.getRoleCalls++
	return f.getRoleResult, f.getRoleErr
}

func (f *stubWorkspaceMemberRepository) AssignRole(_ context.Context, _ db.Executor, _, _, role string, invitedBy *string, _, _ string, before, after map[string]string, notifTitle, notifBody string) error {
	f.assignedRole = role
	f.assignedInvitedBy = invitedBy
	f.assignedBefore = before
	f.assignedAfter = after
	f.assignedNotifTitle = notifTitle
	f.assignedNotifBody = notifBody
	return f.assignErr
}

func (f *stubWorkspaceMemberRepository) ListMembers(_ context.Context, _ db.Executor, _ string) ([]repository.Member, error) {
	return f.listMembersResult, f.listMembersErr
}

func (f *stubWorkspaceMemberRepository) GetWorkspaceOrgID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return f.orgID, f.orgIDErr
}

func (f *stubWorkspaceMemberRepository) RemoveMember(_ context.Context, _ db.Executor, workspaceID, userID, _, _ string) error {
	f.removedWorkspace = workspaceID
	f.removedUserID = userID
	return f.removeErr
}

func strPtr(s string) *string { return &s }

func TestRBACService_AssignRole_NewMember_NoPreviousRole(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleErr: pgx.ErrNoRows}
	svc := NewRBACService(repo, newStubCache())

	result, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "editor", strPtr("inviter-1"), "actor-1", "admin_workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PreviousRole != "" {
		t.Errorf("PreviousRole = %q, want empty (member baru)", result.PreviousRole)
	}
	if result.NewRole != "editor" {
		t.Errorf("NewRole = %q, want editor", result.NewRole)
	}
	if repo.assignedRole != "editor" {
		t.Errorf("AssignRole dipanggil dengan role=%q, want editor", repo.assignedRole)
	}
	if repo.assignedBefore != nil {
		t.Errorf("assignedBefore = %v, want nil (member baru, tidak ada state sebelumnya)", repo.assignedBefore)
	}
	if repo.assignedAfter["role"] != "editor" {
		t.Errorf("assignedAfter[role] = %q, want editor", repo.assignedAfter["role"])
	}
	if repo.assignedNotifTitle == "" || repo.assignedNotifBody == "" {
		t.Error("notifikasi title/body harusnya terisi")
	}
}

func TestRBACService_AssignRole_ExistingMember_RoleChanged(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "viewer"}
	svc := NewRBACService(repo, newStubCache())

	result, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "project_manager", nil, "actor-1", "admin_workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PreviousRole != "viewer" {
		t.Errorf("PreviousRole = %q, want viewer", result.PreviousRole)
	}
	if result.NewRole != "project_manager" {
		t.Errorf("NewRole = %q, want project_manager", result.NewRole)
	}
	if repo.assignedBefore["role"] != "viewer" {
		t.Errorf("assignedBefore[role] = %q, want viewer", repo.assignedBefore["role"])
	}
}

func TestRBACService_AssignRole_InvalidatesCache(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "viewer"}
	c := newStubCache()
	c.store[roleCacheKey("user-1", "ws-1")] = "viewer"

	svc := NewRBACService(repo, c)
	if _, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "editor", nil, "actor-1", "admin_workspace"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := c.store[roleCacheKey("user-1", "ws-1")]; ok {
		t.Error("cache key role:<user>:<workspace> harusnya di-delete setelah AssignRole, tapi masih ada")
	}
}

func TestRBACService_AssignRole_GetRoleRealError_PropagatesAndSkipsWrite(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleErr: errors.New("connection refused")}
	svc := NewRBACService(repo, newStubCache())

	_, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "editor", nil, "actor-1", "admin_workspace")
	if err == nil {
		t.Fatal("harusnya error, tapi nil")
	}
	if repo.assignedRole != "" {
		t.Error("AssignRole (repo) tidak boleh dipanggil kalau cek role lama gagal dengan error asli (bukan ErrNoRows)")
	}
}

func TestRBACService_GetMemberRole_NotAMember_ReturnsEmpty(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleErr: pgx.ErrNoRows}
	svc := NewRBACService(repo, newStubCache())

	role, err := svc.GetMemberRole(context.Background(), nil, "ws-1", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if role != "" {
		t.Errorf("role = %q, want empty (bukan member)", role)
	}
}

func TestRBACService_GetMemberRole_ReturnsRole(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "admin_workspace"}
	svc := NewRBACService(repo, newStubCache())

	role, err := svc.GetMemberRole(context.Background(), nil, "ws-1", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if role != "admin_workspace" {
		t.Errorf("role = %q, want admin_workspace", role)
	}
}

func TestRBACService_GetMemberRole_CacheMiss_PopulatesCache(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "editor"}
	c := newStubCache()
	svc := NewRBACService(repo, c)

	if _, err := svc.GetMemberRole(context.Background(), nil, "ws-1", "user-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.getRoleCalls != 1 {
		t.Errorf("GetRole dipanggil %d kali, want 1 (cache miss pertama)", repo.getRoleCalls)
	}
	if cached := c.store[roleCacheKey("user-1", "ws-1")]; cached != "editor" {
		t.Errorf("cache[%s] = %q, want editor -- GetMemberRole harusnya populate cache setelah miss", roleCacheKey("user-1", "ws-1"), cached)
	}
}

func TestRBACService_GetMemberRole_CacheHit_SkipsRepo(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "editor"} // kalau ke-panggil, akan mismatch dgn cache
	c := newStubCache()
	c.store[roleCacheKey("user-1", "ws-1")] = "admin_workspace"
	svc := NewRBACService(repo, c)

	role, err := svc.GetMemberRole(context.Background(), nil, "ws-1", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if role != "admin_workspace" {
		t.Errorf("role = %q, want admin_workspace (dari cache, bukan repo)", role)
	}
	if repo.getRoleCalls != 0 {
		t.Errorf("GetRole dipanggil %d kali, want 0 (harusnya cache hit, tidak query DB)", repo.getRoleCalls)
	}
}

func TestRBACService_GetMemberRole_NotAMember_DoesNotCache(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleErr: pgx.ErrNoRows}
	c := newStubCache()
	svc := NewRBACService(repo, c)

	if _, err := svc.GetMemberRole(context.Background(), nil, "ws-1", "user-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := c.store[roleCacheKey("user-1", "ws-1")]; ok {
		t.Error("hasil 'bukan member' sengaja tidak di-cache, tapi ada di store")
	}
}

func TestRBACService_ListMembers_ReturnsMembers(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{listMembersResult: []repository.Member{
		{UserID: "user-1", Email: "a@x.com", DisplayName: "A", Role: "admin_workspace"},
		{UserID: "user-2", Email: "b@x.com", DisplayName: "B", Role: "editor"},
	}}
	svc := NewRBACService(repo, newStubCache())

	members, err := svc.ListMembers(context.Background(), nil, "ws-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("len(members) = %d, want 2", len(members))
	}
}

func TestRBACService_RemoveMember_Success(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{}
	svc := NewRBACService(repo, newStubCache())

	if err := svc.RemoveMember(context.Background(), nil, "ws-1", "user-1", "actor-1", "admin_workspace"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.removedWorkspace != "ws-1" || repo.removedUserID != "user-1" {
		t.Errorf("RemoveMember dipanggil dengan workspace=%q user=%q, unexpected", repo.removedWorkspace, repo.removedUserID)
	}
}

func TestRBACService_RemoveMember_InvalidatesCache(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{}
	c := newStubCache()
	c.store[roleCacheKey("user-1", "ws-1")] = "editor"
	svc := NewRBACService(repo, c)

	if err := svc.RemoveMember(context.Background(), nil, "ws-1", "user-1", "actor-1", "admin_workspace"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := c.store[roleCacheKey("user-1", "ws-1")]; ok {
		t.Error("cache key role:<user>:<workspace> harusnya di-delete setelah RemoveMember, tapi masih ada")
	}
}

func TestRBACService_RemoveMember_NotFound(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{removeErr: domain.ErrMemberNotFound}
	svc := NewRBACService(repo, newStubCache())

	err := svc.RemoveMember(context.Background(), nil, "ws-1", "user-missing", "actor-1", "admin_workspace")
	if !errors.Is(err, domain.ErrMemberNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrMemberNotFound", err)
	}
}
