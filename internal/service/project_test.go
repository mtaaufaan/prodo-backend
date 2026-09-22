package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type recordedPMSet struct {
	projectID, userID string
}

type fakeProjectRepo struct {
	workspaceID map[string]string
	getWsIDErr  error
	createErr   error
	listResult  []repository.Project
	updateErr   error
	updateCalls []struct {
		name, status string
		endDate      *time.Time
	}
	archiveErr error
	deleteErr  error
	restoreErr error
	nameExists bool

	assignPendingPMErr   error
	assignPendingPMCalls []recordedPMSet

	activePMUserID string
	removePMErr    error
	removePMCalls  int

	setPMErr   error
	setPMCalls []recordedPMSet

	pendingInvitationID    string
	pendingInvitationIDErr error
}

func (f *fakeProjectRepo) GetWorkspaceID(_ context.Context, _ db.Executor, projectID string) (string, error) {
	if f.getWsIDErr != nil {
		return "", f.getWsIDErr
	}
	ws, ok := f.workspaceID[projectID]
	if !ok {
		return "", domain.ErrProjectNotFound
	}
	return ws, nil
}

func (f *fakeProjectRepo) Create(_ context.Context, _ db.Executor, workspaceID, name, code, pmUserID, _, _ string) (*repository.Project, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	p := &repository.Project{WorkspaceID: workspaceID, Name: name, Code: code}
	if pmUserID != "" {
		p.PMUserID = &pmUserID
	}
	return p, nil
}

func (f *fakeProjectRepo) List(_ context.Context, _ db.Executor, _ string) ([]repository.Project, error) {
	return f.listResult, nil
}

func (f *fakeProjectRepo) NameExists(_ context.Context, _ db.Executor, _, _, _ string) (bool, error) {
	return f.nameExists, nil
}

func (f *fakeProjectRepo) Update(_ context.Context, _ db.Executor, _, name, status, _, _, _ string, endDate *time.Time) error {
	f.updateCalls = append(f.updateCalls, struct {
		name, status string
		endDate      *time.Time
	}{name, status, endDate})
	return f.updateErr
}

func (f *fakeProjectRepo) SetArchived(_ context.Context, _ db.Executor, _ string, _ bool, _, _ string) error {
	return f.archiveErr
}

func (f *fakeProjectRepo) SoftDelete(_ context.Context, _ db.Executor, _, _, _ string) error {
	return f.deleteErr
}

func (f *fakeProjectRepo) Restore(_ context.Context, _ db.Executor, _, _, _ string) error {
	return f.restoreErr
}

func (f *fakeProjectRepo) SetAllowEditorStoryPoints(_ context.Context, _ db.Executor, _ string, _ bool) error {
	return nil
}

func (f *fakeProjectRepo) AssignPendingPM(_ context.Context, _ db.Executor, projectID, userID string) error {
	if f.assignPendingPMErr != nil {
		return f.assignPendingPMErr
	}
	f.assignPendingPMCalls = append(f.assignPendingPMCalls, recordedPMSet{projectID, userID})
	return nil
}

func (f *fakeProjectRepo) GetPMUserID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return f.activePMUserID, nil
}

func (f *fakeProjectRepo) RemovePM(_ context.Context, _ db.Executor, _, _, _ string) error {
	if f.removePMErr != nil {
		return f.removePMErr
	}
	f.removePMCalls++
	return nil
}

func (f *fakeProjectRepo) SetPM(_ context.Context, _ db.Executor, projectID, userID, _, _ string) error {
	if f.setPMErr != nil {
		return f.setPMErr
	}
	f.setPMCalls = append(f.setPMCalls, recordedPMSet{projectID, userID})
	return nil
}

func (f *fakeProjectRepo) GetPendingPMInvitationID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return f.pendingInvitationID, f.pendingInvitationIDErr
}

// fakeProjectPMInviter -- projectPMInviter palsu (CreateInvitation/
// CancelInvitation/GetWorkspaceName), dipakai resolvePM/invitePM jalur
// "undang PM baru".
type fakeProjectPMInviter struct {
	createErr    error
	createCalls  []struct{ email, workspaceID, role, projectID, displayName string }
	cancelErr    error
	cancelCalls  []struct{ workspaceID, invitationID string }
	workspaceErr error
}

func (f *fakeProjectPMInviter) CreateInvitation(_ context.Context, _ db.Executor, email, workspaceID, role, _, _, _, _, projectID, displayName string) (*Invitation, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.createCalls = append(f.createCalls, struct{ email, workspaceID, role, projectID, displayName string }{email, workspaceID, role, projectID, displayName})
	return &Invitation{Email: email, WorkspaceID: workspaceID, Role: role}, nil
}

func (f *fakeProjectPMInviter) CancelInvitation(_ context.Context, _ db.Executor, workspaceID, invitationID, _, _ string) error {
	if f.cancelErr != nil {
		return f.cancelErr
	}
	f.cancelCalls = append(f.cancelCalls, struct{ workspaceID, invitationID string }{workspaceID, invitationID})
	return nil
}

func (f *fakeProjectPMInviter) GetWorkspaceName(_ context.Context, _ db.Executor, _ string) (string, error) {
	if f.workspaceErr != nil {
		return "", f.workspaceErr
	}
	return "Test Workspace", nil
}

// newTestProjectService -- helper konstruksi standar untuk seluruh test di
// file ini, supaya menambah dependency baru (contacts/invites) cuma perlu
// diubah SATU tempat.
func newTestProjectService(repo *fakeProjectRepo, orgs *fakeOrgAuthorizer, rbac *fakeProjectRoleChecker, contacts *stubExistingUserFinder, invites *fakeProjectPMInviter) *ProjectService {
	return NewProjectService(repo, orgs, rbac, nil, contacts, invites, nil)
}

func TestProjectService_Create_PlatformAdminBypass(t *testing.T) {
	repo := &fakeProjectRepo{}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: "project_manager"}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	p, err := svc.Create(context.Background(), nil, "ws-1", "Rilis Q4", "ril", "pm-1", "", "", "pa-1", "platform_admin", "Admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Code != "RIL" {
		t.Fatalf("expected code uppercased to RIL, got %q", p.Code)
	}
}

func TestProjectService_Create_RejectsMissingFields(t *testing.T) {
	repo := &fakeProjectRepo{}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: "project_manager"}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	cases := []struct {
		name, code, pm string
	}{
		{"", "RIL", "pm-1"},
		{"Rilis Q4", "", "pm-1"},
		{"Rilis Q4", "RIL", ""},
		{"Rilis Q4", "R1L", "pm-1"}, // kode harus huruf saja
		{"Rilis Q4", "R", "pm-1"},   // kode minimal 2 huruf
	}
	for _, c := range cases {
		_, err := svc.Create(context.Background(), nil, "ws-1", c.name, c.code, c.pm, "", "", "aw-1", "member", "Admin")
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("Create(%q,%q,%q): expected ErrInvalidInput, got %v", c.name, c.code, c.pm, err)
		}
	}
}

// TestProjectService_Create_PMUserID_PromotesAnyExistingMember -- S4W
// susulan: pmUserID TIDAK LAGI harus sudah project_manager (beda dari AC
// lama) -- member workspace apa pun boleh dipilih, rolenya dinaikkan lewat
// AssignRole.
func TestProjectService_Create_PMUserID_PromotesAnyExistingMember(t *testing.T) {
	rbac := &fakeProjectRoleChecker{role: "editor"}
	repo := &fakeProjectRepo{}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, rbac, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	p, err := svc.Create(context.Background(), nil, "ws-1", "Rilis Q4", "RIL", "user-1", "", "", "aw-1", "member", "Admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.PMUserID == nil || *p.PMUserID != "user-1" {
		t.Errorf("PMUserID = %v, want user-1", p.PMUserID)
	}
}

// TestProjectService_Create_PMUserID_RejectsNonMember -- pmUserID yang
// BUKAN member workspace ini sama sekali (GetMemberRole -> "") ditolak.
func TestProjectService_Create_PMUserID_RejectsNonMember(t *testing.T) {
	repo := &fakeProjectRepo{}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: ""}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	_, err := svc.Create(context.Background(), nil, "ws-1", "Rilis Q4", "RIL", "bukan-member", "", "", "aw-1", "member", "Admin")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput untuk pmUserID bukan member, got %v", err)
	}
}

// TestProjectService_Create_PMEmail_ExistingUser_ResolvedImmediately --
// email yang sudah terdaftar (di mana pun) langsung jadi PM aktif, TANPA
// undangan -- efisiensi utama yang diminta user.
func TestProjectService_Create_PMEmail_ExistingUser_ResolvedImmediately(t *testing.T) {
	repo := &fakeProjectRepo{}
	invites := &fakeProjectPMInviter{}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: "editor"}, &stubExistingUserFinder{userID: "user-existing"}, invites)

	p, err := svc.Create(context.Background(), nil, "ws-1", "Rilis Q4", "RIL", "", "sudah@terdaftar.com", "", "aw-1", "member", "Admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.PMUserID == nil || *p.PMUserID != "user-existing" {
		t.Errorf("PMUserID = %v, want user-existing", p.PMUserID)
	}
	if len(invites.createCalls) != 0 {
		t.Errorf("CreateInvitation dipanggil %d kali, want 0 (email sudah terdaftar tidak perlu undangan)", len(invites.createCalls))
	}
}

// TestProjectService_Create_PMEmail_NewUser_CreatesProjectAwaitingPM --
// email yang BELUM terdaftar sama sekali: project TETAP dibuat (pm_user_id
// kosong, "menunggu PM"), undangan project_manager dibuat tertaut project
// ini.
func TestProjectService_Create_PMEmail_NewUser_CreatesProjectAwaitingPM(t *testing.T) {
	repo := &fakeProjectRepo{}
	invites := &fakeProjectPMInviter{}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{}, &stubExistingUserFinder{}, invites)

	p, err := svc.Create(context.Background(), nil, "ws-1", "Rilis Q4", "RIL", "", "baru@example.com", "Budi Baru", "aw-1", "member", "Admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.PMUserID != nil {
		t.Errorf("PMUserID = %v, want nil (menunggu PM)", p.PMUserID)
	}
	if len(invites.createCalls) != 1 || invites.createCalls[0].email != "baru@example.com" || invites.createCalls[0].role != "project_manager" {
		t.Errorf("createCalls = %+v, want satu entri baru@example.com/project_manager", invites.createCalls)
	}
	// Ditemukan user 2026-09-14: pmName ("Budi Baru") diisi saat undang PM
	// baru tapi tidak pernah muncul di form aktivasi -- root cause: dibuang
	// begitu saja, tidak pernah diteruskan sebagai display_name undangan.
	if invites.createCalls[0].displayName != "Budi Baru" {
		t.Errorf("createCalls[0].displayName = %q, want %q (nama PM harus diteruskan sebagai display_name undangan)", invites.createCalls[0].displayName, "Budi Baru")
	}
}

// TestProjectService_Create_PMEmail_NewUser_RequiresName -- email belum
// terdaftar TANPA pmName tidak bisa membuat undangan (sama pola
// WorkspaceService.CreateWorkspace admin_workspace_name wajib).
func TestProjectService_Create_PMEmail_NewUser_RequiresName(t *testing.T) {
	repo := &fakeProjectRepo{}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	_, err := svc.Create(context.Background(), nil, "ws-1", "Rilis Q4", "RIL", "", "baru@example.com", "", "aw-1", "member", "Admin")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput tanpa pmName, got %v", err)
	}
}

func TestProjectService_Create_RejectsDuplicateName(t *testing.T) {
	repo := &fakeProjectRepo{nameExists: true}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: "project_manager"}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	_, err := svc.Create(context.Background(), nil, "ws-1", "Rilis Q4", "RIL", "pm-1", "", "", "aw-1", "member", "Admin")
	if !errors.Is(err, domain.ErrProjectNameTaken) {
		t.Fatalf("expected ErrProjectNameTaken, got %v", err)
	}
}

func TestProjectService_Update_RejectsDuplicateName(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}, nameExists: true}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: "admin_workspace"}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	err := svc.Update(context.Background(), nil, "proj-1", "Nama Bentrok", "not_started", "aw-1", "member", nil)
	if !errors.Is(err, domain.ErrProjectNameTaken) {
		t.Fatalf("expected ErrProjectNameTaken, got %v", err)
	}
}

func TestProjectService_Update_ForbiddenForNonAWPM(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeProjectRoleChecker{role: "viewer"}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	err := svc.Update(context.Background(), nil, "proj-1", "Nama Baru", "not_started", "viewer-1", "member", nil)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

// Susulan 2026-10-18 ("tambahkan status project, dan tanggal berakhir
// project"): status/endDate diteruskan apa adanya ke repo (whole-form
// save, sama kontrak dengan name -- bukan partial patch seperti pm_user_id).
func TestProjectService_Update_PassesStatusAndEndDateThrough(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: "admin_workspace"}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	if err := svc.Update(context.Background(), nil, "proj-1", "Rilis Q4", "in_progress", "aw-1", "member", &end); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.updateCalls) != 1 {
		t.Fatalf("Update (repo) dipanggil %d kali, want 1", len(repo.updateCalls))
	}
	call := repo.updateCalls[0]
	if call.status != "in_progress" || call.endDate == nil || !call.endDate.Equal(end) {
		t.Errorf("updateCalls[0] = %+v, want status=in_progress endDate=%v", call, end)
	}
}

// status kosong ditolak SEBELUM repo.Update terpanggil -- validasi enum
// sendiri ada di handler (validProjectStatuses), tapi service tetap
// menjaga invariant "tidak boleh kosong" untuk pemanggil lain.
func TestProjectService_Update_EmptyStatus_Rejected(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: "admin_workspace"}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	err := svc.Update(context.Background(), nil, "proj-1", "Rilis Q4", "", "aw-1", "member", nil)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want domain.ErrInvalidInput", err)
	}
	if len(repo.updateCalls) != 0 {
		t.Error("repo.Update tidak boleh terpanggil kalau status kosong")
	}
}

func TestProjectService_Delete_AllowedForWorkspacePM(t *testing.T) {
	// Soft-delete (bukan hard-delete) sengaja mengizinkan AW/PM, bukan
	// cuma GA/PA -- lihat komentar ProjectRepository.SoftDelete.
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeProjectRoleChecker{role: "project_manager"}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	if err := svc.Delete(context.Background(), nil, "proj-1", "pm-1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProjectService_Restore_RejectsWorkspacePM(t *testing.T) {
	// Restore sengaja LEBIH ketat dari Delete: cuma GA/PA, AW/PM yang
	// boleh menghapus TIDAK otomatis boleh memulihkan.
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeProjectRoleChecker{role: "project_manager"}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	err := svc.Restore(context.Background(), nil, "proj-1", "pm-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden untuk AW/PM merestore, got %v", err)
	}
}

func TestProjectService_Restore_AllowedForGroupAdmin(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: ""}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	if err := svc.Restore(context.Background(), nil, "proj-1", "ga-1", "group_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestProjectService_AssignPM_ExistingMember_SetsImmediately -- panel
// Kelola menaikkan member existing (bukan cuma yang sudah project_manager)
// jadi PM, entah project sedang "menunggu PM" atau sudah ada PM aktif lain.
func TestProjectService_AssignPM_ExistingMember_SetsImmediately(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: "editor"}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	err := svc.AssignPM(context.Background(), nil, "proj-1", "user-2", "", "", "aw-1", "member", "Admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.setPMCalls) != 1 || repo.setPMCalls[0].projectID != "proj-1" || repo.setPMCalls[0].userID != "user-2" {
		t.Errorf("setPMCalls = %+v, want satu entri proj-1/user-2", repo.setPMCalls)
	}
}

// TestProjectService_AssignPM_NewEmail_ClearsThenInvites -- jalur undang
// email baru: PM aktif (kalau ada) dikosongkan dulu (project balik ke
// "menunggu PM"), baru undangan baru dibuat.
func TestProjectService_AssignPM_NewEmail_ClearsThenInvites(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	invites := &fakeProjectPMInviter{}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{}, &stubExistingUserFinder{}, invites)

	err := svc.AssignPM(context.Background(), nil, "proj-1", "", "baru@example.com", "Budi Baru", "aw-1", "member", "Admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.removePMCalls != 1 {
		t.Errorf("removePMCalls = %d, want 1", repo.removePMCalls)
	}
	if len(invites.createCalls) != 1 || invites.createCalls[0].email != "baru@example.com" {
		t.Errorf("createCalls = %+v, want satu entri baru@example.com", invites.createCalls)
	}
}

// TestProjectService_AssignPM_CancelsExistingPendingInvitation -- undangan
// PM pending LAMA dibatalkan dulu sebelum menetapkan PM baru (satu project
// cuma boleh punya satu undangan PM pending).
func TestProjectService_AssignPM_CancelsExistingPendingInvitation(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}, pendingInvitationID: "inv-old"}
	invites := &fakeProjectPMInviter{}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: "editor"}, &stubExistingUserFinder{}, invites)

	if err := svc.AssignPM(context.Background(), nil, "proj-1", "user-2", "", "", "aw-1", "member", "Admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(invites.cancelCalls) != 1 || invites.cancelCalls[0].invitationID != "inv-old" {
		t.Errorf("cancelCalls = %+v, want satu entri inv-old", invites.cancelCalls)
	}
}

// TestProjectService_RemovePM_ClearsAndCancelsPending -- project TANPA PM
// aktif (cuma undangan pending mengambang, kasus jarang) tetap boleh
// dibatalkan lewat jalur ini -- guard "cabut PM terakhir" cuma menyala
// kalau ADA PM aktif.
func TestProjectService_RemovePM_ClearsAndCancelsPending(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}, pendingInvitationID: "inv-1"}
	invites := &fakeProjectPMInviter{}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{}, &stubExistingUserFinder{}, invites)

	if err := svc.RemovePM(context.Background(), nil, "proj-1", "aw-1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.removePMCalls != 1 {
		t.Errorf("removePMCalls = %d, want 1", repo.removePMCalls)
	}
	if len(invites.cancelCalls) != 1 || invites.cancelCalls[0].invitationID != "inv-1" {
		t.Errorf("cancelCalls = %+v, want satu entri inv-1", invites.cancelCalls)
	}
}

// TestProjectService_RemovePM_ActivePM_Rejected (susulan 2026-09-15,
// ditemukan user: "kenapa pada project PM bisa dicabut sampai habis?
// ... bertentangan dengan validasi wajib PM di Tambah Project") -- project
// cuma punya SATU slot PM, jadi PM aktif = PM terakhir. "Cabut" ditolak,
// AW harus pakai "+ Tetapkan PM" (ganti langsung) supaya project tidak
// pernah kosong PM setelah pernah punya satu.
func TestProjectService_RemovePM_ActivePM_Rejected(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}, activePMUserID: "pm-1"}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	err := svc.RemovePM(context.Background(), nil, "proj-1", "aw-1", "member")
	if !errors.Is(err, domain.ErrCannotRemoveLastProjectManager) {
		t.Errorf("err = %v, want domain.ErrCannotRemoveLastProjectManager", err)
	}
	if repo.removePMCalls != 0 {
		t.Errorf("removePMCalls = %d, want 0 (ditolak sebelum repo terpanggil)", repo.removePMCalls)
	}
}

// Susulan 2026-10-18 ("saat input tambah PM, apabila sudah pernah
// dimasukkan, setelah selesai input email, agar memunculkan nama di
// input nama") -- LookupPMByEmail preview baca-saja, tidak menetapkan
// apa pun (repo.Update/AssignPM tidak boleh terpanggil).
func TestProjectService_LookupPMByEmail_Found(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: "admin_workspace"}, &stubExistingUserFinder{userID: "user-existing"}, &fakeProjectPMInviter{})

	userID, err := svc.LookupPMByEmail(context.Background(), nil, "proj-1", "existing@contoh.co.id", "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userID != "user-existing" {
		t.Errorf("userID = %q, want user-existing", userID)
	}
}

// email belum terdaftar -- userID kosong, BUKAN error (kasus normal, AW
// lanjut isi Nama manual untuk jalur undang-baru).
func TestProjectService_LookupPMByEmail_NotFound(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: "admin_workspace"}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})

	userID, err := svc.LookupPMByEmail(context.Background(), nil, "proj-1", "belum-terdaftar@contoh.co.id", "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userID != "" {
		t.Errorf("userID = %q, want kosong (belum terdaftar)", userID)
	}
}

// Otorisasi sama seperti AssignPM -- actor yang bukan AW/PM/org-access
// ditolak SEBELUM lookup email terpanggil sama sekali.
func TestProjectService_LookupPMByEmail_ForbiddenForNonAWPM(t *testing.T) {
	repo := &fakeProjectRepo{workspaceID: map[string]string{"proj-1": "ws-1"}}
	contacts := &stubExistingUserFinder{userID: "user-existing"}
	svc := newTestProjectService(repo, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeProjectRoleChecker{role: "viewer"}, contacts, &fakeProjectPMInviter{})

	_, err := svc.LookupPMByEmail(context.Background(), nil, "proj-1", "existing@contoh.co.id", "viewer-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want domain.ErrForbidden", err)
	}
}
