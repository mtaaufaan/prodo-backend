package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/keycloak"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeAccountRepository struct {
	captured repository.CreateGroupAdminInvitationParams
	returnID string
	err      error

	contact           *repository.UserContact
	contactErr        error
	regenErr          error
	regeneratedEmail  string
	regeneratedActor  string
	regeneratedTarget string
	groupAdmins       []repository.GroupAdminSummary
}

func (f *fakeAccountRepository) CreateGroupAdminInvitation(_ context.Context, p *repository.CreateGroupAdminInvitationParams) (string, error) {
	f.captured = *p
	if f.err != nil {
		return "", f.err
	}
	return f.returnID, nil
}

func (f *fakeAccountRepository) FindUserIDByProviderSub(_ context.Context, _ string) (string, error) {
	return f.returnID, f.err
}

func (f *fakeAccountRepository) FindUserContactByID(_ context.Context, _ string) (*repository.UserContact, error) {
	if f.contactErr != nil {
		return nil, f.contactErr
	}
	return f.contact, nil
}

func (f *fakeAccountRepository) RegenerateInvitationToken(_ context.Context, targetUserID, email, _ string, _ time.Time, actorUserID string) error {
	f.regeneratedTarget = targetUserID
	f.regeneratedEmail = email
	f.regeneratedActor = actorUserID
	return f.regenErr
}

func (f *fakeAccountRepository) ListGroupAdmins(_ context.Context, _, _ int) ([]repository.GroupAdminSummary, int, error) {
	return f.groupAdmins, len(f.groupAdmins), f.err
}

type fakeKeycloakClient struct {
	userID string
	err    error

	attributesErr        error
	lastAttributesUserID string
	lastAttributes       map[string][]string
}

func (f *fakeKeycloakClient) CreateDisabledUser(_ context.Context, _, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.userID, nil
}

func (f *fakeKeycloakClient) SetPassword(_ context.Context, _, _ string) error {
	return f.err
}

func (f *fakeKeycloakClient) EnableUser(_ context.Context, _ string) error {
	return f.err
}

func (f *fakeKeycloakClient) SetUserAttributes(_ context.Context, keycloakUserID string, attributes map[string][]string) error {
	f.lastAttributesUserID = keycloakUserID
	f.lastAttributes = attributes
	return f.attributesErr
}

func TestAccountService_CreateGroupAdmin_Success(t *testing.T) {
	repo := &fakeAccountRepository{returnID: "user-123"}
	kc := &fakeKeycloakClient{userID: "kc-sub-456"}
	svc := NewAccountService(repo, kc, zap.NewNop())

	req := CreateGroupAdminRequest{
		Email:           "ga@example.com",
		DisplayName:     "Group Admin",
		InvitedByUserID: "platform-admin-1",
	}

	before := time.Now()
	result, err := svc.CreateGroupAdmin(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.UserID != "user-123" {
		t.Errorf("UserID = %q, want %q", result.UserID, "user-123")
	}
	if result.ActivationToken == "" {
		t.Error("ActivationToken kosong, seharusnya token acak")
	}
	if !result.ExpiresAt.After(before.Add(71 * time.Hour)) {
		t.Errorf("ExpiresAt = %v, seharusnya ~72 jam dari sekarang", result.ExpiresAt)
	}

	if repo.captured.KeycloakUserID != "kc-sub-456" {
		t.Errorf("repo menerima KeycloakUserID = %q, want %q", repo.captured.KeycloakUserID, "kc-sub-456")
	}
	if repo.captured.TokenHash == "" || repo.captured.TokenHash == result.ActivationToken {
		t.Error("TokenHash harus terisi dan berbeda dari raw token (bukti hashing, bukan plaintext)")
	}
}

func TestAccountService_CreateGroupAdmin_InvalidInput(t *testing.T) {
	svc := NewAccountService(&fakeAccountRepository{}, &fakeKeycloakClient{}, zap.NewNop())

	_, err := svc.CreateGroupAdmin(context.Background(), CreateGroupAdminRequest{})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}

func TestAccountService_CreateGroupAdmin_EmailAlreadyExistsInKeycloak(t *testing.T) {
	kc := &fakeKeycloakClient{err: keycloak.ErrUserAlreadyExists}
	svc := NewAccountService(&fakeAccountRepository{}, kc, zap.NewNop())

	_, err := svc.CreateGroupAdmin(context.Background(), CreateGroupAdminRequest{
		Email:           "dup@example.com",
		DisplayName:     "Dup",
		InvitedByUserID: "platform-admin-1",
	})
	if !errors.Is(err, domain.ErrEmailAlreadyExists) {
		t.Errorf("err = %v, want wrapped domain.ErrEmailAlreadyExists", err)
	}
}

func TestAccountService_CreateGroupAdmin_RepoFailureAfterKeycloakCreate(t *testing.T) {
	repo := &fakeAccountRepository{err: errors.New("db down")}
	kc := &fakeKeycloakClient{userID: "kc-sub-orphan"}
	svc := NewAccountService(repo, kc, zap.NewNop())

	_, err := svc.CreateGroupAdmin(context.Background(), CreateGroupAdminRequest{
		Email:           "ga@example.com",
		DisplayName:     "Group Admin",
		InvitedByUserID: "platform-admin-1",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestAccountService_ResendActivation_Success(t *testing.T) {
	repo := &fakeAccountRepository{contact: &repository.UserContact{
		Email:       "ga@example.com",
		DisplayName: "Group Admin",
	}}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	result, err := svc.ResendActivation(context.Background(), "user-1", "platform-admin-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Email != "ga@example.com" {
		t.Errorf("Email = %q, want ga@example.com", result.Email)
	}
	if result.ActivationToken == "" {
		t.Error("ActivationToken kosong")
	}
	if repo.regeneratedTarget != "user-1" {
		t.Errorf("regeneratedTarget = %q, want user-1 (bukan actor -- audit entity_id harus GA target, bukan Platform Admin)", repo.regeneratedTarget)
	}
	if repo.regeneratedEmail != "ga@example.com" {
		t.Errorf("regeneratedEmail = %q, want ga@example.com", repo.regeneratedEmail)
	}
	if repo.regeneratedActor != "platform-admin-1" {
		t.Errorf("regeneratedActor = %q, want platform-admin-1", repo.regeneratedActor)
	}
}

func TestAccountService_ResendActivation_NoUserFound(t *testing.T) {
	repo := &fakeAccountRepository{contactErr: domain.ErrUserNotFound}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	_, err := svc.ResendActivation(context.Background(), "missing-user", "platform-admin-1")
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrUserNotFound", err)
	}
}

func TestAccountService_ResendActivation_NoPendingInvitation(t *testing.T) {
	repo := &fakeAccountRepository{
		contact:  &repository.UserContact{Email: "ga@example.com", DisplayName: "GA"},
		regenErr: domain.ErrInvitationNotFound,
	}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	_, err := svc.ResendActivation(context.Background(), "user-1", "platform-admin-1")
	if !errors.Is(err, domain.ErrInvitationNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrInvitationNotFound", err)
	}
}

func TestAccountService_ListGroupAdmins(t *testing.T) {
	repo := &fakeAccountRepository{groupAdmins: []repository.GroupAdminSummary{
		{ID: "user-1", Email: "ga1@example.com", DisplayName: "GA 1", IsActive: true},
		{ID: "user-2", Email: "ga2@example.com", DisplayName: "GA 2", IsActive: false},
	}}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	summaries, total, err := svc.ListGroupAdmins(context.Background(), 50, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 || len(summaries) != 2 {
		t.Errorf("total=%d len=%d, want 2/2", total, len(summaries))
	}
}
