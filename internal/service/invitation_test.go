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

func (r *stubInvitationRepo) CreateInvitation(_ context.Context, _ db.Executor, email, workspaceID, role, invitedByUserID, tokenHash string, expiresAt time.Time) (string, error) {
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

func (r *stubInvitationRepo) AcceptInvitation(_ context.Context, _ db.Executor, _, _, _, _, _, _ string) (string, error) {
	return r.acceptedUserID, r.acceptErr
}

func (r *stubInvitationRepo) AcceptExecutiveInvitation(_ context.Context, _ db.Executor, _, _, _, _, _ string) (string, error) {
	return r.acceptedUserID, r.acceptErr
}

func (r *stubInvitationRepo) Cancel(_ context.Context, _ db.Executor, _, _, _ string) error {
	return r.cancelErr
}

func (r *stubInvitationRepo) Resend(_ context.Context, _ db.Executor, _, _, _ string, _ time.Time) (*repository.ResendTarget, error) {
	return r.resendResult, r.resendErr
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

func newTestInvitationService(repo *stubInvitationRepo, emailer *stubInvitationEmailer, kc *fakeKeycloakClient, users *stubExistingUserFinder, assigner *stubWorkspaceAssigner) *InvitationService {
	return NewInvitationService(repo, emailer, kc, users, assigner, zap.NewNop(), "http://localhost:5173")
}

func TestInvitationService_CreateInvitation_Success(t *testing.T) {
	repo := &stubInvitationRepo{}
	emailer := &stubInvitationEmailer{}
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

	inv, err := svc.CreateInvitation(context.Background(), nil, "budi@example.com", "ws-1", "editor", "actor-1", "Tim Marketing", "Siti Aminah")
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
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

	if _, err := svc.CreateInvitation(context.Background(), nil, "a@x.com", "ws-1", "editor", "actor-1", "WS", "Actor"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := svc.CreateInvitation(context.Background(), nil, "b@x.com", "ws-1", "editor", "actor-1", "WS", "Actor"); err != nil {
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
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

	if _, err := svc.CreateInvitation(context.Background(), nil, "a@x.com", "ws-1", "editor", "actor-1", "WS", "Actor"); err == nil {
		t.Fatal("harusnya error, tapi nil")
	}
	if len(emailer.sent) != 0 {
		t.Error("email tidak boleh terkirim kalau simpan DB gagal")
	}
}

func TestInvitationService_CreateInvitation_EmailError_Propagates(t *testing.T) {
	repo := &stubInvitationRepo{}
	emailer := &stubInvitationEmailer{sendErr: errors.New("smtp down")}
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

	if _, err := svc.CreateInvitation(context.Background(), nil, "a@x.com", "ws-1", "editor", "actor-1", "WS", "Actor"); err == nil {
		t.Fatal("harusnya error, tapi nil")
	}
}

func TestInvitationService_CreateBulkInvitations_ValidAndDuplicate(t *testing.T) {
	repo := &stubInvitationRepo{}
	emailer := &stubInvitationEmailer{}
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

	emails := []string{"a@x.com", "b@x.com", "c@x.com", "d@x.com", "e@x.com", "a@x.com"} // a@x.com duplikat
	result, err := svc.CreateBulkInvitations(context.Background(), stubExecutor{}, emails, "ws-1", "editor", "actor-1", "admin_workspace", "WS", "Actor")
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
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

	emails := []string{"a@x.com", "bukan-email", "c@x.com"}
	result, err := svc.CreateBulkInvitations(context.Background(), stubExecutor{}, emails, "ws-1", "editor", "actor-1", "admin_workspace", "WS", "Actor")
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
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

	emails := []string{"sudah-pending@x.com", "valid@x.com"}
	result, err := svc.CreateBulkInvitations(context.Background(), stubExecutor{}, emails, "ws-1", "editor", "actor-1", "admin_workspace", "WS", "Actor")
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
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{userID: "user-existing"}, assigner)

	result, err := svc.CreateBulkInvitations(context.Background(), stubExecutor{}, []string{"sudah-terdaftar@x.com"}, "ws-1", "editor", "actor-1", "admin_workspace", "WS", "Actor")
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

func TestInvitationService_AcceptInvitation_Success(t *testing.T) {
	repo := &stubInvitationRepo{
		findPendingResult: &repository.InvitationTarget{ID: "inv-1", Email: "budi@example.com", WorkspaceID: "ws-1", Role: "editor"},
		acceptedUserID:    "user-new",
	}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{userID: "kc-sub-1"}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

	result, err := svc.AcceptInvitation(context.Background(), nil, "raw-token", "Budi Santoso", "Password123!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.UserID != "user-new" || result.Email != "budi@example.com" || result.WorkspaceID != "ws-1" || result.Role != "editor" {
		t.Errorf("hasil salah: %+v", result)
	}
}

func TestInvitationService_AcceptInvitation_TokenNotFound(t *testing.T) {
	repo := &stubInvitationRepo{findPendingErr: fmt.Errorf("repository.FindPendingByTokenHash: %w", domain.ErrInvitationNotFound)}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

	_, err := svc.AcceptInvitation(context.Background(), nil, "raw-token", "Budi Santoso", "Password123!")
	if !errors.Is(err, domain.ErrInvitationNotFound) {
		t.Errorf("err = %v, want domain.ErrInvitationNotFound", err)
	}
}

func TestInvitationService_AcceptInvitation_DisplayNameTooShort(t *testing.T) {
	repo := &stubInvitationRepo{findPendingResult: &repository.InvitationTarget{ID: "inv-1", Email: "a@x.com", WorkspaceID: "ws-1", Role: "editor"}}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

	if _, err := svc.AcceptInvitation(context.Background(), nil, "raw-token", "A", "Password123!"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want domain.ErrInvalidInput", err)
	}
}

func TestInvitationService_AcceptInvitation_KeycloakError_Propagates(t *testing.T) {
	repo := &stubInvitationRepo{findPendingResult: &repository.InvitationTarget{ID: "inv-1", Email: "a@x.com", WorkspaceID: "ws-1", Role: "editor"}}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{err: errors.New("keycloak down")}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

	if _, err := svc.AcceptInvitation(context.Background(), nil, "raw-token", "Budi Santoso", "Password123!"); err == nil {
		t.Fatal("harusnya error, tapi nil")
	}
}

func TestInvitationService_CancelInvitation_NotFound(t *testing.T) {
	repo := &stubInvitationRepo{cancelErr: fmt.Errorf("repository.Cancel: %w", domain.ErrInvitationNotFound)}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

	err := svc.CancelInvitation(context.Background(), nil, "ws-1", "inv-1", "actor-1")
	if !errors.Is(err, domain.ErrInvitationNotFound) {
		t.Errorf("err = %v, want domain.ErrInvitationNotFound", err)
	}
}

func TestInvitationService_CancelInvitation_Success(t *testing.T) {
	repo := &stubInvitationRepo{}
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

	if err := svc.CancelInvitation(context.Background(), nil, "ws-1", "inv-1", "actor-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInvitationService_ResendInvitation_Success(t *testing.T) {
	repo := &stubInvitationRepo{resendResult: &repository.ResendTarget{Email: "budi@example.com", Role: "editor"}}
	emailer := &stubInvitationEmailer{}
	svc := newTestInvitationService(repo, emailer, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

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
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

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
	svc := newTestInvitationService(repo, &stubInvitationEmailer{}, &fakeKeycloakClient{}, &stubExistingUserFinder{}, &stubWorkspaceAssigner{})

	err := svc.ResendInvitation(context.Background(), nil, "ws-1", "inv-1", "WS", "Actor")
	if !errors.Is(err, domain.ErrInvitationNotFound) {
		t.Errorf("err = %v, want domain.ErrInvitationNotFound", err)
	}
}
