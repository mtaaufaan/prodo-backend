package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

type recordedInvitation struct {
	email, workspaceID, role, invitedByUserID, tokenHash string
	expiresAt                                            time.Time
}

type stubInvitationRepo struct {
	createErr error
	created   []recordedInvitation
	nextID    int
}

func (r *stubInvitationRepo) CreateInvitation(_ context.Context, _ db.Executor, email, workspaceID, role, invitedByUserID, tokenHash string, expiresAt time.Time) (string, error) {
	if r.createErr != nil {
		return "", r.createErr
	}
	r.nextID++
	r.created = append(r.created, recordedInvitation{email, workspaceID, role, invitedByUserID, tokenHash, expiresAt})
	return fmt.Sprintf("inv-%d", r.nextID), nil
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

func TestInvitationService_CreateInvitation_Success(t *testing.T) {
	repo := &stubInvitationRepo{}
	emailer := &stubInvitationEmailer{}
	svc := NewInvitationService(repo, emailer, "http://localhost:5173")

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
	svc := NewInvitationService(repo, emailer, "http://localhost:5173")

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
	svc := NewInvitationService(repo, emailer, "http://localhost:5173")

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
	svc := NewInvitationService(repo, emailer, "http://localhost:5173")

	if _, err := svc.CreateInvitation(context.Background(), nil, "a@x.com", "ws-1", "editor", "actor-1", "WS", "Actor"); err == nil {
		t.Fatal("harusnya error, tapi nil")
	}
}

func TestInvitationService_CreateBulkInvitations_ValidAndDuplicate(t *testing.T) {
	repo := &stubInvitationRepo{}
	emailer := &stubInvitationEmailer{}
	svc := NewInvitationService(repo, emailer, "http://localhost:5173")

	emails := []string{"a@x.com", "b@x.com", "c@x.com", "d@x.com", "e@x.com", "a@x.com"} // a@x.com duplikat
	result, err := svc.CreateBulkInvitations(context.Background(), nil, emails, "ws-1", "editor", "actor-1", "WS", "Actor")
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
	svc := NewInvitationService(repo, emailer, "http://localhost:5173")

	emails := []string{"a@x.com", "bukan-email", "c@x.com"}
	result, err := svc.CreateBulkInvitations(context.Background(), nil, emails, "ws-1", "editor", "actor-1", "WS", "Actor")
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
