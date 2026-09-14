package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/cache"
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

	countAdminsResult int
	countAdminsErr    error

	candidatesResult []repository.Member
	candidatesErr    error
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

func (f *stubWorkspaceMemberRepository) ListOrgCandidates(_ context.Context, _ db.Executor, _ string) ([]repository.Member, error) {
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

func (f *stubWorkspaceMemberRepository) CountAdminsExcluding(_ context.Context, _ db.Executor, _, _ string) (int, error) {
	return f.countAdminsResult, f.countAdminsErr
}

func (f *stubWorkspaceMemberRepository) ListWorkspaceMemberCandidates(_ context.Context, _ db.Executor, _, _ string) ([]repository.Member, error) {
	return f.candidatesResult, f.candidatesErr
}

func strPtr(s string) *string { return &s }

// stubProjectPMRepo -- projectPMRepository palsu (Kelola Member & Roles,
// S4W susulan role restructuring 2026-09-14) -- workspaceIDFor kosong
// berarti GetWorkspaceID mengembalikan projectID APA ADANYA dianggap
// cocok (test yang tidak peduli validasi lintas-workspace bisa biarkan
// kosong); isi eksplisit kalau test ingin menguji mismatch.
type stubProjectPMRepo struct {
	workspaceIDFor    map[string]string
	getWorkspaceIDErr error

	pmProjectsResult []repository.PMProjectRef
	pmProjectsErr    error

	setPMErr  error
	setPMCall []struct{ projectID, userID string }
}

func (r *stubProjectPMRepo) GetWorkspaceID(_ context.Context, _ db.Executor, projectID string) (string, error) {
	if r.getWorkspaceIDErr != nil {
		return "", r.getWorkspaceIDErr
	}
	if r.workspaceIDFor != nil {
		if ws, ok := r.workspaceIDFor[projectID]; ok {
			return ws, nil
		}
	}
	return "ws-1", nil
}

func (r *stubProjectPMRepo) ListPMProjectNames(_ context.Context, _ db.Executor, _, _ string) ([]repository.PMProjectRef, error) {
	return r.pmProjectsResult, r.pmProjectsErr
}

func (r *stubProjectPMRepo) SetPM(_ context.Context, _ db.Executor, projectID, userID, _, _ string) error {
	if r.setPMErr != nil {
		return r.setPMErr
	}
	r.setPMCall = append(r.setPMCall, struct{ projectID, userID string }{projectID, userID})
	return nil
}

// stubProjectMembershipRepo -- projectMembershipRepository palsu.
type stubProjectMembershipRepo struct {
	existingProjectIDs []string
	listErr            error

	removedProjectIDs []string
	removeErr         error

	addCall []struct{ projectID, userID, role string }
	addErr  error
}

func (r *stubProjectMembershipRepo) ListProjectIDsForUserInWorkspace(_ context.Context, _ db.Executor, _, _ string) ([]string, error) {
	return r.existingProjectIDs, r.listErr
}

func (r *stubProjectMembershipRepo) RemoveMember(_ context.Context, _ db.Executor, projectID, _, _, _ string) error {
	if r.removeErr != nil {
		return r.removeErr
	}
	r.removedProjectIDs = append(r.removedProjectIDs, projectID)
	return nil
}

func (r *stubProjectMembershipRepo) AddMember(_ context.Context, _ db.Executor, projectID, _, userID, role string, _ bool, _, _ string) error {
	if r.addErr != nil {
		return r.addErr
	}
	r.addCall = append(r.addCall, struct{ projectID, userID, role string }{projectID, userID, role})
	return nil
}

// newTestRBACService -- helper konstruksi standar, projects/projectMembers
// opsional (variadic) supaya test lama yang tidak peduli fitur project
// tidak perlu diubah -- stub kosong dipakai sebagai default.
func newTestRBACService(repo workspaceMemberRepository, c cache.Cache, deps ...any) *RBACService {
	var projects projectPMRepository = &stubProjectPMRepo{}
	var projectMembers projectMembershipRepository = &stubProjectMembershipRepo{}
	for _, d := range deps {
		switch v := d.(type) {
		case *stubProjectPMRepo:
			projects = v
		case *stubProjectMembershipRepo:
			projectMembers = v
		}
	}
	return NewRBACService(repo, c, projects, projectMembers)
}

func TestRBACService_AssignRole_NewMember_NoPreviousRole(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleErr: pgx.ErrNoRows}
	svc := newTestRBACService(repo, newStubCache())

	result, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "editor", strPtr("inviter-1"), "actor-1", "admin_workspace", "")
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
	svc := newTestRBACService(repo, newStubCache())

	result, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "project_manager", nil, "actor-1", "admin_workspace", "")
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

	svc := newTestRBACService(repo, c)
	if _, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "editor", nil, "actor-1", "admin_workspace", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := c.store[roleCacheKey("user-1", "ws-1")]; ok {
		t.Error("cache key role:<user>:<workspace> harusnya di-delete setelah AssignRole, tapi masih ada")
	}
}

func TestRBACService_AssignRole_GetRoleRealError_PropagatesAndSkipsWrite(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleErr: errors.New("connection refused")}
	svc := newTestRBACService(repo, newStubCache())

	_, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "editor", nil, "actor-1", "admin_workspace", "")
	if err == nil {
		t.Fatal("harusnya error, tapi nil")
	}
	if repo.assignedRole != "" {
		t.Error("AssignRole (repo) tidak boleh dipanggil kalau cek role lama gagal dengan error asli (bukan ErrNoRows)")
	}
}

func TestRBACService_GetMemberRole_NotAMember_ReturnsEmpty(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleErr: pgx.ErrNoRows}
	svc := newTestRBACService(repo, newStubCache())

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
	svc := newTestRBACService(repo, newStubCache())

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
	svc := newTestRBACService(repo, c)

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
	svc := newTestRBACService(repo, c)

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
	svc := newTestRBACService(repo, c)

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
	svc := newTestRBACService(repo, newStubCache())

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
	svc := newTestRBACService(repo, newStubCache())

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
	svc := newTestRBACService(repo, c)

	if err := svc.RemoveMember(context.Background(), nil, "ws-1", "user-1", "actor-1", "admin_workspace"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := c.store[roleCacheKey("user-1", "ws-1")]; ok {
		t.Error("cache key role:<user>:<workspace> harusnya di-delete setelah RemoveMember, tapi masih ada")
	}
}

func TestRBACService_RemoveMember_NotFound(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{removeErr: domain.ErrMemberNotFound}
	svc := newTestRBACService(repo, newStubCache())

	err := svc.RemoveMember(context.Background(), nil, "ws-1", "user-missing", "actor-1", "admin_workspace")
	if !errors.Is(err, domain.ErrMemberNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrMemberNotFound", err)
	}
}

// S4W-01: target satu-satunya admin_workspace -- CountAdminsExcluding = 0
// berarti tidak ada admin lain tersisa, RemoveMember harus ditolak SEBELUM
// repo.RemoveMember (DELETE) sempat dipanggil.
func TestRBACService_RemoveMember_LastAdmin_Rejected(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "admin_workspace", countAdminsResult: 0}
	svc := newTestRBACService(repo, newStubCache())

	err := svc.RemoveMember(context.Background(), nil, "ws-1", "user-1", "actor-1", "group_admin")
	if !errors.Is(err, domain.ErrCannotRemoveLastWorkspaceAdmin) {
		t.Errorf("err = %v, want domain.ErrCannotRemoveLastWorkspaceAdmin", err)
	}
	if repo.removedUserID != "" {
		t.Error("repo.RemoveMember (DELETE) tidak boleh terpanggil kalau guard menolak")
	}
}

// Admin BUKAN yang terakhir (masih ada admin lain) -- harus tetap berhasil.
func TestRBACService_RemoveMember_NotLastAdmin_Succeeds(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "admin_workspace", countAdminsResult: 1}
	svc := newTestRBACService(repo, newStubCache())

	if err := svc.RemoveMember(context.Background(), nil, "ws-1", "user-1", "actor-1", "group_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.removedUserID != "user-1" {
		t.Error("repo.RemoveMember harusnya tetap terpanggil kalau masih ada admin lain")
	}
}

// Menurunkan admin_workspace TERAKHIR ke role lain (bukan hapus, ganti
// role) harus ditolak dengan guard yang sama -- root cause sama dengan
// RemoveMember (invariant "minimal 1 admin_workspace").
func TestRBACService_AssignRole_DowngradeLastAdmin_Rejected(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "admin_workspace", countAdminsResult: 0}
	svc := newTestRBACService(repo, newStubCache())

	_, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "editor", nil, "actor-1", "group_admin", "")
	if !errors.Is(err, domain.ErrCannotRemoveLastWorkspaceAdmin) {
		t.Errorf("err = %v, want domain.ErrCannotRemoveLastWorkspaceAdmin", err)
	}
	if repo.assignedRole != "" {
		t.Error("repo.AssignRole tidak boleh terpanggil kalau guard menolak")
	}
}

// Reassign admin_workspace TERAKHIR ke admin_workspace lagi (role sama,
// mis. panggilan idempoten) BUKAN downgrade -- guard tidak boleh ikut
// memblokir ini.
func TestRBACService_AssignRole_SameRoleAdmin_NotBlocked(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "admin_workspace", countAdminsResult: 0}
	svc := newTestRBACService(repo, newStubCache())

	if _, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "admin_workspace", nil, "actor-1", "group_admin", ""); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- S4W susulan role restructuring 2026-09-14 (Kelola Member & Roles,
// dikonfirmasi user): AssignRole gains projectID -- guard "project tidak
// boleh kehilangan PM tanpa pengganti", validasi project_id milik
// workspace ini, dan move semantics project_members. ---

func TestRBACService_AssignRole_ProjectID_NotInWorkspace_Rejected(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "viewer"}
	projects := &stubProjectPMRepo{workspaceIDFor: map[string]string{"proj-other": "ws-lain"}}
	svc := newTestRBACService(repo, newStubCache(), projects)

	_, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "editor", nil, "actor-1", "admin_workspace", "proj-other")
	if !errors.Is(err, domain.ErrProjectNotFound) {
		t.Errorf("err = %v, want domain.ErrProjectNotFound", err)
	}
	if repo.assignedRole != "" {
		t.Error("repo.AssignRole tidak boleh terpanggil kalau project_id tidak valid")
	}
}

// Mengubah role SEORANG PM (pmProjectsResult tidak kosong) ke role
// NON-PM harus ditolak -- project yang dia pimpin akan kehilangan PM
// tanpa pengganti, dikonfirmasi user ("validasi untuk project harus ada
// minimal 1 PM").
func TestRBACService_AssignRole_WouldLoseLastPM_Rejected(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "project_manager"}
	projects := &stubProjectPMRepo{pmProjectsResult: []repository.PMProjectRef{{ID: "proj-1", Name: "Rilis Q4"}}}
	svc := newTestRBACService(repo, newStubCache(), projects)

	_, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "editor", nil, "actor-1", "admin_workspace", "proj-2")
	if !errors.Is(err, domain.ErrProjectWouldLoseLastPM) {
		t.Errorf("err = %v, want domain.ErrProjectWouldLoseLastPM", err)
	}
	if repo.assignedRole != "" {
		t.Error("repo.AssignRole tidak boleh terpanggil kalau guard menolak")
	}
}

// Tetap mengangkat PM ke role project_manager (pmProjectsResult TIDAK
// dicek karena role baru == project_manager) harus tetap berhasil --
// guard cuma berlaku saat role BARU bukan project_manager.
func TestRBACService_AssignRole_StillPM_NotBlockedByGuard(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "project_manager"}
	projects := &stubProjectPMRepo{pmProjectsResult: []repository.PMProjectRef{{ID: "proj-1", Name: "Rilis Q4"}}}
	svc := newTestRBACService(repo, newStubCache(), projects)

	if _, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "project_manager", nil, "actor-1", "admin_workspace", "proj-2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projects.setPMCall) != 1 || projects.setPMCall[0].projectID != "proj-2" {
		t.Errorf("setPMCall = %+v, want satu entri proj-2", projects.setPMCall)
	}
}

// Role project_manager -> SetPM dipanggil, TIDAK ADA AddMember (PM tidak
// butuh baris project_members).
func TestRBACService_AssignRole_ProjectManager_CallsSetPM(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "editor"}
	projects := &stubProjectPMRepo{}
	projectMembers := &stubProjectMembershipRepo{}
	svc := newTestRBACService(repo, newStubCache(), projects, projectMembers)

	if _, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "project_manager", nil, "actor-1", "admin_workspace", "proj-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projects.setPMCall) != 1 || projects.setPMCall[0].projectID != "proj-1" || projects.setPMCall[0].userID != "user-1" {
		t.Errorf("setPMCall = %+v, want satu entri proj-1/user-1", projects.setPMCall)
	}
	if len(projectMembers.addCall) != 0 {
		t.Errorf("addCall = %+v, want kosong (PM tidak butuh project_members)", projectMembers.addCall)
	}
}

// Move semantics (dikonfirmasi user "point 1"): keterkaitan project_members
// LAMA (proj-old) dipindah -- dihapus dulu, baru ditambahkan ke project
// baru (proj-new) dengan role baru.
func TestRBACService_AssignRole_EditorRole_MovesProjectMembership(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "viewer"}
	projects := &stubProjectPMRepo{}
	projectMembers := &stubProjectMembershipRepo{existingProjectIDs: []string{"proj-old"}}
	svc := newTestRBACService(repo, newStubCache(), projects, projectMembers)

	if _, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "editor", nil, "actor-1", "admin_workspace", "proj-new"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projectMembers.removedProjectIDs) != 1 || projectMembers.removedProjectIDs[0] != "proj-old" {
		t.Errorf("removedProjectIDs = %v, want [proj-old]", projectMembers.removedProjectIDs)
	}
	if len(projectMembers.addCall) != 1 || projectMembers.addCall[0].projectID != "proj-new" || projectMembers.addCall[0].role != "editor" {
		t.Errorf("addCall = %+v, want satu entri proj-new/editor", projectMembers.addCall)
	}
}

// projectID kosong (8 pemanggil AssignRole lain -- admin swap, invite
// flow) TIDAK BOLEH menyentuh guard/project sama sekali -- regresi nihil.
func TestRBACService_AssignRole_EmptyProjectID_SkipsProjectLogic(t *testing.T) {
	repo := &stubWorkspaceMemberRepository{getRoleResult: "project_manager"}
	projects := &stubProjectPMRepo{pmProjectsResult: []repository.PMProjectRef{{ID: "proj-1", Name: "Rilis Q4"}}}
	projectMembers := &stubProjectMembershipRepo{existingProjectIDs: []string{"proj-old"}}
	svc := newTestRBACService(repo, newStubCache(), projects, projectMembers)

	if _, err := svc.AssignRole(context.Background(), nil, "ws-1", "user-1", "editor", nil, "actor-1", "admin_workspace", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projectMembers.removedProjectIDs) != 0 || len(projectMembers.addCall) != 0 || len(projects.setPMCall) != 0 {
		t.Error("projectID kosong tidak boleh memicu logika project apa pun (guard/move/SetPM)")
	}
}
