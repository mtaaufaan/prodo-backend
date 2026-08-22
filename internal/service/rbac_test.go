package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type stubWorkspaceMemberRepository struct {
	getRoleResult string
	getRoleErr    error

	upsertErr         error
	upsertedRole      string
	upsertedInvitedBy *string
}

func (f *stubWorkspaceMemberRepository) GetRole(_ context.Context, _, _ string) (string, error) {
	return f.getRoleResult, f.getRoleErr
}

func (f *stubWorkspaceMemberRepository) UpsertRole(_ context.Context, _, _, role string, invitedBy *string) error {
	f.upsertedRole = role
	f.upsertedInvitedBy = invitedBy
	return f.upsertErr
}

func strPtr(s string) *string { return &s }

func TestRBACService_AssignRole_NewMember_NoPreviousRole(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleErr: pgx.ErrNoRows}
	c := newStubCache()
	svc := NewRBACService(repo, c)

	result, err := svc.AssignRole(context.Background(), "ws-1", "user-1", "editor", strPtr("inviter-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PreviousRole != "" {
		t.Errorf("PreviousRole = %q, want empty (member baru)", result.PreviousRole)
	}
	if result.NewRole != "editor" {
		t.Errorf("NewRole = %q, want editor", result.NewRole)
	}
	if repo.upsertedRole != "editor" {
		t.Errorf("UpsertRole dipanggil dengan role=%q, want editor", repo.upsertedRole)
	}
}

func TestRBACService_AssignRole_ExistingMember_RoleChanged(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "viewer"}
	c := newStubCache()
	svc := NewRBACService(repo, c)

	result, err := svc.AssignRole(context.Background(), "ws-1", "user-1", "project_manager", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PreviousRole != "viewer" {
		t.Errorf("PreviousRole = %q, want viewer", result.PreviousRole)
	}
	if result.NewRole != "project_manager" {
		t.Errorf("NewRole = %q, want project_manager", result.NewRole)
	}
}

func TestRBACService_AssignRole_InvalidatesCache(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "viewer"}
	c := newStubCache()
	c.store[roleCacheKey("user-1", "ws-1")] = "viewer"

	svc := NewRBACService(repo, c)
	if _, err := svc.AssignRole(context.Background(), "ws-1", "user-1", "editor", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := c.store[roleCacheKey("user-1", "ws-1")]; ok {
		t.Error("cache key role:<user>:<workspace> harusnya di-delete setelah AssignRole, tapi masih ada")
	}
}

func TestRBACService_AssignRole_GetRoleRealError_PropagatesAndSkipsWrite(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleErr: errors.New("connection refused")}
	svc := NewRBACService(repo, newStubCache())

	_, err := svc.AssignRole(context.Background(), "ws-1", "user-1", "editor", nil)
	if err == nil {
		t.Fatal("harusnya error, tapi nil")
	}
	if repo.upsertedRole != "" {
		t.Error("UpsertRole tidak boleh dipanggil kalau cek role lama gagal dengan error asli (bukan ErrNoRows)")
	}
}
