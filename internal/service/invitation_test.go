package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// stubExecutor -- db.Executor palsu untuk test yang benar-benar memanggil
// exec.Exec() (mis. withSavepoint di CreateBulkInvitations) -- nil akan
// panic karena db.Executor adalah interface, bukan pointer yang aman
// dipanggil saat nil.
type stubExecutor struct{}

func (stubExecutor) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (stubExecutor) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (stubExecutor) QueryRow(context.Context, string, ...any) pgx.Row        { return nil }

type recordedInvitation struct {
	email, workspaceID, role, invitedByUserID, tokenHash string
	expiresAt                                            time.Time
}

type stubInvitationRepo struct {
	createErr     error
	failCreateFor string // kalau diisi, CreateInvitation gagal HANYA untuk email ini
	created       []recordedInvitation
	nextID        int

	findPendingResult *repository.InvitationTarget
	findPendingErr    error

	acceptedUserID string
	acceptErr      error

	cancelErr error

	resendResult *repository.ResendTarget
	resendErr    error

	listPendingResult []repository.PendingInvitation
	listPendingErr    error
}

func (r *stubInvitationRepo) CreateInvitation(_ context.Context, _ db.Executor, email, workspaceID, role, invitedByUserID, tokenHash, projectID string, expiresAt time.Time) (string, error) {
	if r.createErr != nil {
		return "", r.createErr
	}
	if r.failCreateFor != "" && email == r.failCreateFor {
		return "", domain.ErrInvitationAlreadyPending
	}
	r.nextID++
	r.created = append(r.created, recordedInvitation{email, workspaceID, role, invitedByUserID, tokenHash, expiresAt})
	return fmt.Sprintf("inv-%d", r.nextID), nil
}

func (r *stubInvitationRepo) CreateExecutiveInvitation(_ context.Context, _ db.Executor, email, groupID, invitedByUserID, tokenHash string, expiresAt time.Time) (string, error) {
	if r.createErr != nil {
		return "", r.createErr
	}
	if r.failCreateFor != "" && email == r.failCreateFor {
		return "", domain.ErrInvitationAlreadyPending
	}
	r.nextID++
	r.created = append(r.created, recordedInvitation{email, groupID, "", invitedByUserID, tokenHash, expiresAt})
	return fmt.Sprintf("inv-%d", r.nextID), nil
}

func (r *stubInvitationRepo) FindPendingByTokenHash(_ context.Context, _ db.Executor, _ string) (*repository.InvitationTarget, error) {
	return r.findPendingResult, r.findPendingErr
}

func (r *stubInvitationRepo) AcceptInvitation(_ context.Context, _ db.Executor, _, _, _, _, _, _, _ string) (string, error) {
	return r.acceptedUserID, r.acceptErr
}

func (r *stubInvitationRepo) AcceptExecutiveInvitation(_ context.Context, _ db.Executor, _, _, _, _, _, _ string) (string, error) {
	return r.acceptedUserID, r.acceptErr
}

func (r *stubInvitationRepo) Cancel(_ context.Context, _ db.Executor, _, _, _ string) error {
	return r.cancelErr
}

func (r *stubInvitationRepo) Resend(_ context.Context, _ db.Executor, _, _, _ string, _ time.Time) (*repository.ResendTarget, error) {
	return r.resendResult, r.resendErr
}

func (r *stubInvitationRepo) CancelExecutive(_ context.Context, _ db.Executor, _, _, _ string) error {
	return r.cancelErr
}

func (r *stubInvitationRepo) ResendExecutive(_ context.Context, _ db.Executor, _, _, _ string, _ time.Time) (string, error) {
	if r.resendErr != nil {
		return "", r.resendErr
	}
	if r.resendResult != nil {
		return r.resendResult.Email, nil
	}
	return "", nil
}

func (r *stubInvitationRepo) UpdateExecutiveIdentity(_ context.Context, _ db.Executor, _, _, _, _, _ string) error {
	return nil
}

func (r *stubInvitationRepo) GetWorkspaceName(_ context.Context, _ db.Executor, _ string) (string, error) {
	return "Test Workspace", nil
}

func (r *stubInvitationRepo) ListPending(_ context.Context, _ db.Executor, _ string) ([]repository.PendingInvitation, error) {
	return r.listPendingResult, r.listPendingErr
}

type sentInvitationEmail struct {
	to, workspaceName, inviterName, role, acceptLink string
	expiresAt                                        time.Time
}

type stubInvitationEmailer struct {
	sendErr error
	sent    []sentInvitationEmail
}

func (e *stubInvitationEmailer) SendWorkspaceInvitationEmail(_ context.Context, to, workspaceName, inviterName, role, acceptLink string, expiresAt time.Time) error {
	if e.sendErr != nil {
		return e.sendErr
	}
	e.sent = append(e.sent, sentInvitationEmail{to, workspaceName, inviterName, role, acceptLink, expiresAt})
	return nil
}

func (e *stubInvitationEmailer) SendExecutiveInvitationEmail(_ context.Context, to, groupName, inviterName, acceptLink string, expiresAt time.Time) error {
	if e.sendErr != nil {
		return e.sendErr
	}
	e.sent = append(e.sent, sentInvitationEmail{to, groupName, inviterName, "", acceptLink, expiresAt})
	return nil
}

// stubExistingUserFinder -- userID kosong berarti "tidak ditemukan"
// (pgx.ErrNoRows), sama pola dengan repo asli.
type stubExistingUserFinder struct {
	userID string
}

func (f *stubExistingUserFinder) FindUserIDByEmail(_ context.Context, _ string) (string, error) {
	if f.userID == "" {
		return "", pgx.ErrNoRows
	}
	return f.userID, nil
}

type recordedAssignment struct {
	workspaceID, userID, role string
}

type stubWorkspaceAssigner struct {
	assignErr error
	assigned  []recordedAssignment
}

func (a *stubWorkspaceAssigner) AssignRole(_ context.Context, _ db.Executor, workspaceID, userID, role string, _ *string, _, _ string) (*RoleChangeResult, error) {
	if a.assignErr != nil {
		return nil, a.assignErr
	}
	a.assigned = append(a.assigned, recordedAssignment{workspaceID, userID, role})
	return &RoleChangeResult{NewRole: role}, nil
}

type recordedPendingPM struct {
	projectID, userID string
}

type recordedSetPM struct {
	projectID, userID, actorID, actorRole string
}

type stubProjectPMAssigner struct {
	assignErr error
	assigned  []recordedPendingPM

	setPMErr error
	setPM    []recordedSetPM
}

func (a *stubProjectPMAssigner) AssignPendingPM(_ context.Context, _ db.Executor, projectID, userID string) error {
	if a.assignErr != nil {
		return a.assignErr
	}
	a.assigned = append(a.assigned, recordedPendingPM{projectID, userID})
	return nil
}

func (a *stubProjectPMAssigner) SetPM(_ context.Context, _ db.Executor, projectID, userID, actorID, actorRole string) error {
	if a.setPMErr != nil {
		return a.setPMErr
	}
	a.setPM = append(a.setPM, recordedSetPM{projectID, userID, actorID, actorRole})
	return nil
}

type recordedProjectMember struct {
	projectID, workspaceID, userID, role string
	isScoped                             bool
	addedBy, actorRole                   string
}

// stubProjectMemberLinker -- workspaceID kosong berarti GetWorkspaceID
// mengembalikan projectID APA ADANYA dianggap cocok (test yang tidak
// peduli validasi lintas-workspace bisa biarkan kosong); isi eksplisit
// kalau test ingin menguji mismatch (ErrProjectNotFound).
type stubProjectMemberLinker struct {
	workspaceIDFor    map[string]string
	getWorkspaceIDErr error

	addErr error
	added  []recordedProjectMember
}

func (l *stubProjectMemberLinker) GetWorkspaceID(_ context.Context, _ db.Executor, projectID string) (string, error) {
	if l.getWorkspaceIDErr != nil {
		return "", l.getWorkspaceIDErr
	}
	if l.workspaceIDFor != nil {
		if ws, ok := l.workspaceIDFor[projectID]; ok {
			return ws, nil
		}
	}
	return "ws-1", nil
}

func (l *stubProjectMemberLinker) AddMember(_ context.Context, _ db.Executor, projectID, workspaceID, userID, role string, isScoped bool, addedBy, actorRole string) error {
	if l.addErr != nil {
		return l.addErr
	}
	l.added = append(l.added, recordedProjectMember{projectID, workspaceID, userID, role, isScoped, addedBy, actorRole})
	return nil
}

func newTestInvitationService(repo *stubInvitationRepo, emailer *stubInvitationEmailer, kc *fakeKeycloakClient, users *stubExistingUserFinder, assigner *stubWorkspaceAssigner, projects *stubProjectPMAssigner, projectMembers ...*stubProjectMemberLinker) *InvitationService {
	var pm *stubProjectMemberLinker
	if len(projectMembers) > 0 {
		pm = projectMembers[0]
	} else {
		pm = &stubProjectMemberLinker{}
	}
	return NewInvitationService(repo, emailer, kc, users, assigner, projects, pm, zap.NewNop(), "http://localhost:5173")
}

func TestInvitationService_CreateInvitation_Success(t *testing.T) {
	repo := &stubInvitationRepo{}
	emailer := &stubInvitationEmailer{}
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	inv, err := svc.CreateInvitation(context.Background(), nil, "budi@example.com", "ws-1", "editor", "actor-1", "Tim Marketing", "Siti Aminah", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.Email != "budi@example.com" || inv.WorkspaceID != "ws-1" || inv.Role != "editor" {
		t.Errorf("invitation fields salah: %+v", inv)
	}
	if until := time.Until(inv.ExpiresAt); until < 71*time.Hour || until > 72*time.Hour {
		t.Errorf("ExpiresAt = %v dari sekarang, want ~72 jam", until)
	}
	if len(repo.created) != 1 {
		t.Fatalf("CreateInvitation repo dipanggil %d kali, want 1", len(repo.created))
	}
	if repo.created[0].tokenHash == "" {
		t.Error("tokenHash kosong")
	}
	if len(emailer.sent) != 1 {
		t.Fatalf("email dikirim %d kali, want 1", len(emailer.sent))
	}
	if !strings.Contains(emailer.sent[0].acceptLink, "token=") {
		t.Errorf("acceptLink tidak mengandung token: %s", emailer.sent[0].acceptLink)
	}
}

func TestInvitationService_CreateInvitation_TokenUniquePerCall(t *testing.T) {
	repo := &stubInvitationRepo{}
	emailer := &stubInvitationEmailer{}
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	if _, err := svc.CreateInvitation(context.Background(), nil, "a@x.com", "ws-1", "editor", "actor-1", "WS", "Actor", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := svc.CreateInvitation(context.Background(), nil, "b@x.com", "ws-1", "editor", "actor-1", "WS", "Actor", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.created[0].tokenHash == repo.created[1].tokenHash {
		t.Error("tokenHash dua invitation berbeda seharusnya tidak sama")
	}
	if emailer.sent[0].acceptLink == emailer.sent[1].acceptLink {
		t.Error("acceptLink dua invitation berbeda seharusnya tidak sama (token berbeda)")
	}
}

func TestInvitationService_CreateInvitation_RepoError_Propagates(t *testing.T) {
	repo := &stubInvitationRepo{createErr: errors.New("db down")}
	emailer := &stubInvitationEmailer{}
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	if _, err := svc.CreateInvitation(context.Background(), nil, "a@x.com", "ws-1", "editor", "actor-1", "WS", "Actor", ""); err == nil {
		t.Fatal("harusnya error, tapi nil")
	}
	if len(emailer.sent) != 0 {
		t.Error("email tidak boleh terkirim kalau simpan DB gagal")
	}
}

func TestInvitationService_CreateInvitation_EmailError_Propagates(t *testing.T) {
	repo := &stubInvitationRepo{}
	emailer := &stubInvitationEmailer{sendErr: errors.New("smtp down")}
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	if _, err := svc.CreateInvitation(context.Background(), nil, "a@x.com", "ws-1", "editor", "actor-1", "WS", "Actor", ""); err == nil {
		t.Fatal("harusnya error, tapi nil")
	}
}

func TestInvitationService_CreateBulkInvitations_ValidAndDuplicate(t *testing.T) {
	repo := &stubInvitationRepo{}
	emailer := &stubInvitationEmailer{}
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	emails := []string{"a@x.com", "b@x.com", "c@x.com", "d@x.com", "e@x.com", "a@x.com"} // a@x.com duplikat
	result, err := svc.CreateBulkInvitations(context.Background(), stubExecutor{}, emails, "ws-1", "editor", "actor-1", "admin_workspace", "WS", "Actor", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Created) != 5 {
		t.Errorf("len(Created) = %d, want 5 (duplikat di-dedupe, bukan error)", len(result.Created))
	}
	if len(result.Errors) != 0 {
		t.Errorf("Errors = %v, want kosong", result.Errors)
	}
}

func TestInvitationService_CreateBulkInvitations_InvalidFormat_ErrorPerRow(t *testing.T) {
	repo := &stubInvitationRepo{}
	emailer := &stubInvitationEmailer{}
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	emails := []string{"a@x.com", "bukan-email", "c@x.com"}
	result, err := svc.CreateBulkInvitations(context.Background(), stubExecutor{}, emails, "ws-1", "editor", "actor-1", "admin_workspace", "WS", "Actor", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Created) != 2 {
		t.Errorf("len(Created) = %d, want 2 (email valid tetap dibuat)", len(result.Created))
	}
	if msg, ok := result.Errors["bukan-email"]; !ok || msg == "" {
		t.Errorf("Errors[%q] harusnya berisi pesan error, dapat %q (ok=%v)", "bukan-email", msg, ok)
	}
}

// TestInvitationService_CreateBulkInvitations_OneEmailFails_OthersStillSucceed
// menguji withSavepoint: satu email gagal (mis. sudah pending) TIDAK
// boleh menggagalkan email lain dalam batch yang sama -- bug nyata yang
// ketahuan lewat live testing sebelum savepoint ditambahkan (satu baris
// gagal bikin transaksi Postgres aborted, COMMIT di akhir gagal untuk
// SEMUA email termasuk yang valid).
func TestInvitationService_CreateBulkInvitations_OneEmailFails_OthersStillSucceed(t *testing.T) {
	repo := &stubInvitationRepo{failCreateFor: "sudah-pending@x.com"}
	emailer := &stubInvitationEmailer{}
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	emails := []string{"sudah-pending@x.com", "valid@x.com"}
	result, err := svc.CreateBulkInvitations(context.Background(), stubExecutor{}, emails, "ws-1", "editor", "actor-1", "admin_workspace", "WS", "Actor", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Created) != 1 || result.Created[0].Email != "valid@x.com" {
		t.Errorf("Created = %+v, want satu entri valid@x.com", result.Created)
	}
	if msg, ok := result.Errors["sudah-pending@x.com"]; !ok || msg == "" {
		t.Errorf("Errors[sudah-pending@x.com] harusnya berisi pesan error, dapat %q (ok=%v)", msg, ok)
	}
}

func TestInvitationService_CreateBulkInvitations_ExistingUser_AddedDirectly(t *testing.T) {
	repo := &stubInvitationRepo{}
	emailer := &stubInvitationEmailer{}
	assigner := &stubWorkspaceAssigner{}
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{userID: "user-existing"}, assigner, &stubProjectPMAssigner{})

	result, err := svc.CreateBulkInvitations(context.Background(), stubExecutor{}, []string{"sudah-terdaftar@x.com"}, "ws-1", "editor", "actor-1", "admin_workspace", "WS", "Actor", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.AddedDirectly) != 1 || result.AddedDirectly[0] != "sudah-terdaftar@x.com" {
		t.Errorf("AddedDirectly = %v, want [sudah-terdaftar@x.com]", result.AddedDirectly)
	}
	if len(result.Created) != 0 {
		t.Errorf("Created = %v, want kosong (email sudah terdaftar tidak boleh dapat undangan baru)", result.Created)
	}
	if len(emailer.sent) != 0 {
		t.Error("email undangan tidak boleh terkirim untuk user yang sudah terdaftar (S2-23)")
	}
	if len(assigner.assigned) != 1 || assigner.assigned[0].userID != "user-existing" || assigner.assigned[0].role != "editor" {
		t.Errorf("assigner.assigned = %+v, want satu entri user-existing/editor", assigner.assigned)
	}
}

// TestInvitationService_CreateBulkInvitations_ExistingUser_ProjectManager_SetsPM
// -- role restructuring 2026-09-14: email SUDAH terdaftar diundang sebagai
// project_manager DENGAN project_id harus langsung SetPM (bukan cuma
// AssignRole workspace) -- jalur ini tidak lewat AcceptInvitation sama
// sekali (tidak ada undangan yang perlu diterima).
func TestInvitationService_CreateBulkInvitations_ExistingUser_ProjectManager_SetsPM(t *testing.T) {
	repo := &stubInvitationRepo{}
	projects := &stubProjectPMAssigner{}
	pm := &stubProjectMemberLinker{}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{userID: "user-existing"}, &stubWorkspaceAssigner{}, projects, pm)

	result, err := svc.CreateBulkInvitations(context.Background(), stubExecutor{}, []string{"sudah-terdaftar@x.com"}, "ws-1", "project_manager", "actor-1", "admin_workspace", "WS", "Actor", "proj-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.AddedDirectly) != 1 {
		t.Fatalf("AddedDirectly = %v, want satu entri", result.AddedDirectly)
	}
	if len(projects.setPM) != 1 || projects.setPM[0].projectID != "proj-1" || projects.setPM[0].userID != "user-existing" {
		t.Errorf("projects.setPM = %+v, want satu entri proj-1/user-existing", projects.setPM)
	}
	if len(pm.added) != 0 {
		t.Errorf("pm.added = %+v, want kosong untuk role project_manager (bukan project_members)", pm.added)
	}
}

// TestInvitationService_CreateBulkInvitations_ExistingUser_Editor_AddsProjectMember
// -- varian di atas untuk role project-scoped (editor/approver/viewer):
// AddMember ke project_members, BUKAN SetPM.
func TestInvitationService_CreateBulkInvitations_ExistingUser_Editor_AddsProjectMember(t *testing.T) {
	repo := &stubInvitationRepo{}
	projects := &stubProjectPMAssigner{}
	pm := &stubProjectMemberLinker{}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{userID: "user-existing"}, &stubWorkspaceAssigner{}, projects, pm)

	result, err := svc.CreateBulkInvitations(context.Background(), stubExecutor{}, []string{"sudah-terdaftar@x.com"}, "ws-1", "editor", "actor-1", "admin_workspace", "WS", "Actor", "proj-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.AddedDirectly) != 1 {
		t.Fatalf("AddedDirectly = %v, want satu entri", result.AddedDirectly)
	}
	if len(pm.added) != 1 || pm.added[0].projectID != "proj-1" || pm.added[0].userID != "user-existing" || pm.added[0].role != "editor" || pm.added[0].isScoped {
		t.Errorf("pm.added = %+v, want satu entri proj-1/user-existing/editor/isScoped=false", pm.added)
	}
	if len(projects.setPM) != 0 {
		t.Errorf("projects.setPM = %+v, want kosong untuk role editor", projects.setPM)
	}
}

// TestInvitationService_CreateBulkInvitations_ProjectNotInWorkspace_Rejected
// -- projectID yang DIKLAIM tapi sebenarnya milik workspace LAIN harus
// ditolak (ErrProjectNotFound), bukan diam-diam menaut project lintas
// workspace -- pertahanan berlapis, FE seharusnya tidak pernah kirim ini
// tapi endpoint tidak boleh percaya begitu saja pada project_id dari client.
func TestInvitationService_CreateBulkInvitations_ProjectNotInWorkspace_Rejected(t *testing.T) {
	repo := &stubInvitationRepo{}
	pm := &stubProjectMemberLinker{workspaceIDFor: map[string]string{"proj-other-ws": "ws-lain"}}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{}, pm)

	_, err := svc.CreateBulkInvitations(context.Background(), stubExecutor{}, []string{"a@x.com"}, "ws-1", "editor", "actor-1", "admin_workspace", "WS", "Actor", "proj-other-ws")
	if !errors.Is(err, domain.ErrProjectNotFound) {
		t.Errorf("err = %v, want domain.ErrProjectNotFound", err)
	}
}

func TestInvitationService_AcceptInvitation_Success(t *testing.T) {
	repo := &stubInvitationRepo{
		findPendingResult: &repository.InvitationTarget{ID: "inv-1", Email: "budi@example.com", WorkspaceID: "ws-1", Role: "editor"},
		acceptedUserID:    "user-new",
	}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{userID: "kc-sub-1"}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	result, err := svc.AcceptInvitation(context.Background(), nil, "raw-token", "Budi Santoso", "", "Password123!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.UserID != "user-new" || result.Email != "budi@example.com" || result.WorkspaceID != "ws-1" || result.Role != "editor" {
		t.Errorf("hasil salah: %+v", result)
	}
}

// TestInvitationService_AcceptInvitation_ProjectLinked_AssignsPendingPM --
// S4W susulan: undangan project_manager yang tertaut project TERTENTU
// (target.ProjectID) harus memicu AssignPendingPM begitu diterima, supaya
// project itu otomatis dapat pm_user_id tanpa langkah manual tambahan.
func TestInvitationService_AcceptInvitation_ProjectLinked_AssignsPendingPM(t *testing.T) {
	repo := &stubInvitationRepo{
		findPendingResult: &repository.InvitationTarget{ID: "inv-1", Email: "budi@example.com", WorkspaceID: "ws-1", Role: "project_manager", ProjectID: "proj-1"},
		acceptedUserID:    "user-new",
	}
	projects := &stubProjectPMAssigner{}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{userID: "kc-sub-1"}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, projects)

	if _, err := svc.AcceptInvitation(context.Background(), nil, "raw-token", "Budi Santoso", "", "Password123!"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projects.assigned) != 1 || projects.assigned[0].projectID != "proj-1" || projects.assigned[0].userID != "user-new" {
		t.Errorf("projects.assigned = %+v, want satu entri proj-1/user-new", projects.assigned)
	}
}

// TestInvitationService_AcceptInvitation_NoProjectLink_DoesNotAssignPM --
// undangan BIASA (tanpa ProjectID) tidak boleh memicu AssignPendingPM sama
// sekali -- pastikan cabang baru tidak jadi tidak sengaja aktif untuk
// undangan admin_workspace/editor/dst yang sudah ada sebelumnya.
func TestInvitationService_AcceptInvitation_NoProjectLink_DoesNotAssignPM(t *testing.T) {
	repo := &stubInvitationRepo{
		findPendingResult: &repository.InvitationTarget{ID: "inv-1", Email: "budi@example.com", WorkspaceID: "ws-1", Role: "editor"},
		acceptedUserID:    "user-new",
	}
	projects := &stubProjectPMAssigner{}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{userID: "kc-sub-1"}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, projects)

	if _, err := svc.AcceptInvitation(context.Background(), nil, "raw-token", "Budi Santoso", "", "Password123!"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projects.assigned) != 0 {
		t.Errorf("projects.assigned = %+v, want kosong untuk undangan tanpa project_id", projects.assigned)
	}
}

// TestInvitationService_AcceptInvitation_ProjectLinked_EditorAddsProjectMember
// -- role restructuring 2026-09-14: undangan editor/approver/viewer yang
// tertaut project TERTENTU harus menghasilkan baris project_members
// (is_scoped=false, orang ini SUDAH jadi workspace_members lewat undangan
// yang sama), bukan cuma AssignPendingPM (yang khusus project_manager).
func TestInvitationService_AcceptInvitation_ProjectLinked_EditorAddsProjectMember(t *testing.T) {
	repo := &stubInvitationRepo{
		findPendingResult: &repository.InvitationTarget{ID: "inv-1", Email: "budi@example.com", WorkspaceID: "ws-1", Role: "editor", ProjectID: "proj-1"},
		acceptedUserID:    "user-new",
	}
	pm := &stubProjectMemberLinker{}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{userID: "kc-sub-1"}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{}, pm)

	if _, err := svc.AcceptInvitation(context.Background(), nil, "raw-token", "Budi Santoso", "", "Password123!"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pm.added) != 1 {
		t.Fatalf("pm.added = %+v, want satu entri", pm.added)
	}
	got := pm.added[0]
	if got.projectID != "proj-1" || got.workspaceID != "ws-1" || got.userID != "user-new" || got.role != "editor" || got.isScoped {
		t.Errorf("pm.added[0] = %+v, want proj-1/ws-1/user-new/editor/isScoped=false", got)
	}
}

func TestInvitationService_AcceptInvitation_TokenNotFound(t *testing.T) {
	repo := &stubInvitationRepo{findPendingErr: fmt.Errorf("repository.FindPendingByTokenHash: %w", domain.ErrInvitationNotFound)}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	_, err := svc.AcceptInvitation(context.Background(), nil, "raw-token", "Budi Santoso", "", "Password123!")
	if !errors.Is(err, domain.ErrInvitationNotFound) {
		t.Errorf("err = %v, want domain.ErrInvitationNotFound", err)
	}
}

func TestInvitationService_AcceptInvitation_DisplayNameTooShort(t *testing.T) {
	repo := &stubInvitationRepo{findPendingResult: &repository.InvitationTarget{ID: "inv-1", Email: "a@x.com", WorkspaceID: "ws-1", Role: "editor"}}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	if _, err := svc.AcceptInvitation(context.Background(), nil, "raw-token", "A", "", "Password123!"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want domain.ErrInvalidInput", err)
	}
}

func TestInvitationService_AcceptInvitation_KeycloakError_Propagates(t *testing.T) {
	repo := &stubInvitationRepo{findPendingResult: &repository.InvitationTarget{ID: "inv-1", Email: "a@x.com", WorkspaceID: "ws-1", Role: "editor"}}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{err: errors.New("keycloak down")}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	if _, err := svc.AcceptInvitation(context.Background(), nil, "raw-token", "Budi Santoso", "", "Password123!"); err == nil {
		t.Fatal("harusnya error, tapi nil")
	}
}

func TestInvitationService_CancelInvitation_NotFound(t *testing.T) {
	repo := &stubInvitationRepo{cancelErr: fmt.Errorf("repository.Cancel: %w", domain.ErrInvitationNotFound)}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	err := svc.CancelInvitation(context.Background(), nil, "ws-1", "inv-1", "actor-1")
	if !errors.Is(err, domain.ErrInvitationNotFound) {
		t.Errorf("err = %v, want domain.ErrInvitationNotFound", err)
	}
}

func TestInvitationService_CancelInvitation_Success(t *testing.T) {
	repo := &stubInvitationRepo{}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	if err := svc.CancelInvitation(context.Background(), nil, "ws-1", "inv-1", "actor-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInvitationService_ResendInvitation_Success(t *testing.T) {
	repo := &stubInvitationRepo{resendResult: &repository.ResendTarget{Email: "budi@example.com", Role: "editor"}}
	emailer := &stubInvitationEmailer{}
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	if err := svc.ResendInvitation(context.Background(), nil, "ws-1", "inv-1", "WS", "Actor"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(emailer.sent) != 1 || emailer.sent[0].to != "budi@example.com" {
		t.Errorf("email terkirim = %+v, want satu ke budi@example.com", emailer.sent)
	}
}

func TestInvitationService_ListPendingInvitations_ReturnsList(t *testing.T) {
	repo := &stubInvitationRepo{listPendingResult: []repository.PendingInvitation{
		{ID: "inv-1", Email: "a@x.com", Role: "editor", ExpiresAt: time.Now().Add(72 * time.Hour)},
	}}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	invitations, err := svc.ListPendingInvitations(context.Background(), nil, "ws-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(invitations) != 1 || invitations[0].Email != "a@x.com" {
		t.Errorf("invitations = %+v, want satu entri a@x.com", invitations)
	}
}

func TestInvitationService_ResendInvitation_NotFound(t *testing.T) {
	repo := &stubInvitationRepo{resendErr: fmt.Errorf("repository.Resend: %w", domain.ErrInvitationNotFound)}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{}, &stubProjectPMAssigner{})

	err := svc.ResendInvitation(context.Background(), nil, "ws-1", "inv-1", "WS", "Actor")
	if !errors.Is(err, domain.ErrInvitationNotFound) {
		t.Errorf("err = %v, want domain.ErrInvitationNotFound", err)
	}
}
