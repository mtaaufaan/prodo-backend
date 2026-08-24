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

	suspendErr           error
	reactivateErr        error
	suspendedTarget      string
	reactivatedTarget    string
	sessionTimeoutSecs   int
	sessionTimeoutErr    error
	setSessionTimeoutErr error
	setSessionTimeoutTo  int
	ipAllowlist          []repository.IPAllowlistEntry
	ipAllowlistErr       error
	addIPAllowlistID     string
	addIPAllowlistErr    error
	addedCIDR            string
	deleteIPAllowlistErr error
	deletedEntryID       string

	transferErr         error
	transferCount       int
	transferFromUserID  string
	transferToUserID    string
	deleteGroupAdminErr error
	deletedGroupAdminID string

	groupAdminDetail       *repository.GroupAdminSummary
	groupAdminDetailErr    error
	updateGroupAdminErr    error
	updatedGroupAdminID    string
	updatedGroupAdminParam *repository.UpdateGroupAdminParams
	tiers                  []repository.ServiceTier
	tiersErr               error
}

func (f *fakeAccountRepository) GetGroupAdminDetail(_ context.Context, _ string) (*repository.GroupAdminSummary, error) {
	return f.groupAdminDetail, f.groupAdminDetailErr
}

func (f *fakeAccountRepository) UpdateGroupAdmin(_ context.Context, targetUserID string, p *repository.UpdateGroupAdminParams, _ string) error {
	f.updatedGroupAdminID = targetUserID
	f.updatedGroupAdminParam = p
	return f.updateGroupAdminErr
}

func (f *fakeAccountRepository) ListServiceTiers(_ context.Context) ([]repository.ServiceTier, error) {
	return f.tiers, f.tiersErr
}

func (f *fakeAccountRepository) SuspendGroupAdmin(_ context.Context, targetUserID, _ string) error {
	f.suspendedTarget = targetUserID
	return f.suspendErr
}

func (f *fakeAccountRepository) ReactivateGroupAdmin(_ context.Context, targetUserID, _ string) error {
	f.reactivatedTarget = targetUserID
	return f.reactivateErr
}

func (f *fakeAccountRepository) TransferGroup(_ context.Context, fromUserID, toUserID, _ string) (int, error) {
	f.transferFromUserID = fromUserID
	f.transferToUserID = toUserID
	if f.transferErr != nil {
		return 0, f.transferErr
	}
	return f.transferCount, nil
}

func (f *fakeAccountRepository) DeleteGroupAdmin(_ context.Context, targetUserID, _ string) error {
	f.deletedGroupAdminID = targetUserID
	return f.deleteGroupAdminErr
}

func (f *fakeAccountRepository) GetPASessionIdleTimeoutSeconds(_ context.Context) (int, error) {
	return f.sessionTimeoutSecs, f.sessionTimeoutErr
}

func (f *fakeAccountRepository) SetPASessionIdleTimeoutSeconds(_ context.Context, seconds int, _ string) error {
	f.setSessionTimeoutTo = seconds
	return f.setSessionTimeoutErr
}

func (f *fakeAccountRepository) ListIPAllowlist(_ context.Context, _ string) ([]repository.IPAllowlistEntry, error) {
	return f.ipAllowlist, f.ipAllowlistErr
}

func (f *fakeAccountRepository) AddIPAllowlistEntry(_ context.Context, _, cidr, _ string) (string, error) {
	f.addedCIDR = cidr
	if f.addIPAllowlistErr != nil {
		return "", f.addIPAllowlistErr
	}
	return f.addIPAllowlistID, nil
}

func (f *fakeAccountRepository) DeleteIPAllowlistEntry(_ context.Context, _, entryID, _ string) error {
	f.deletedEntryID = entryID
	return f.deleteIPAllowlistErr
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
		GroupName:       "PT Contoh",
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
		GroupName:       "PT Contoh",
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
		GroupName:       "PT Contoh",
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

func TestAccountService_SuspendGroupAdmin_Success(t *testing.T) {
	repo := &fakeAccountRepository{}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	if err := svc.SuspendGroupAdmin(context.Background(), "ga-1", "pa-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.suspendedTarget != "ga-1" {
		t.Errorf("suspendedTarget = %q, want %q", repo.suspendedTarget, "ga-1")
	}
}

func TestAccountService_SuspendGroupAdmin_NotFound(t *testing.T) {
	repo := &fakeAccountRepository{suspendErr: domain.ErrUserNotFound}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	err := svc.SuspendGroupAdmin(context.Background(), "not-a-ga", "pa-1")
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrUserNotFound", err)
	}
}

func TestAccountService_ReactivateGroupAdmin_Success(t *testing.T) {
	repo := &fakeAccountRepository{}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	if err := svc.ReactivateGroupAdmin(context.Background(), "ga-1", "pa-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.reactivatedTarget != "ga-1" {
		t.Errorf("reactivatedTarget = %q, want %q", repo.reactivatedTarget, "ga-1")
	}
}

func TestAccountService_SetPASessionIdleTimeout_TooShort(t *testing.T) {
	// US-070 AC: minimum 10 menit (600 detik).
	repo := &fakeAccountRepository{}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	err := svc.SetPASessionIdleTimeout(context.Background(), 599, "pa-1")
	if !errors.Is(err, domain.ErrSessionTimeoutTooShort) {
		t.Errorf("err = %v, want wrapped domain.ErrSessionTimeoutTooShort", err)
	}
	if repo.setSessionTimeoutTo != 0 {
		t.Error("repo tidak seharusnya dipanggil kalau validasi minimum gagal")
	}
}

func TestAccountService_SetPASessionIdleTimeout_AtMinimum_Accepted(t *testing.T) {
	repo := &fakeAccountRepository{}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	if err := svc.SetPASessionIdleTimeout(context.Background(), 600, "pa-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.setSessionTimeoutTo != 600 {
		t.Errorf("setSessionTimeoutTo = %d, want 600", repo.setSessionTimeoutTo)
	}
}

func TestAccountService_AddIPAllowlistEntry_InvalidCIDR(t *testing.T) {
	repo := &fakeAccountRepository{}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	_, err := svc.AddIPAllowlistEntry(context.Background(), "pa-1", "not-a-cidr", "pa-1")
	if !errors.Is(err, domain.ErrInvalidCIDR) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidCIDR", err)
	}
	if repo.addedCIDR != "" {
		t.Error("repo tidak seharusnya dipanggil kalau CIDR tidak valid")
	}
}

func TestAccountService_AddIPAllowlistEntry_Valid(t *testing.T) {
	repo := &fakeAccountRepository{addIPAllowlistID: "entry-1"}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	id, err := svc.AddIPAllowlistEntry(context.Background(), "pa-1", "10.0.0.0/24", "pa-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "entry-1" {
		t.Errorf("id = %q, want %q", id, "entry-1")
	}
	if repo.addedCIDR != "10.0.0.0/24" {
		t.Errorf("addedCIDR = %q, want %q", repo.addedCIDR, "10.0.0.0/24")
	}
}

func TestAccountService_DeleteIPAllowlistEntry_NotFound(t *testing.T) {
	repo := &fakeAccountRepository{deleteIPAllowlistErr: domain.ErrIPAllowlistEntryNotFound}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	err := svc.DeleteIPAllowlistEntry(context.Background(), "pa-1", "entry-1", "pa-1")
	if !errors.Is(err, domain.ErrIPAllowlistEntryNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrIPAllowlistEntryNotFound", err)
	}
}

func TestAccountService_TransferGroup_Success(t *testing.T) {
	repo := &fakeAccountRepository{transferCount: 2}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	count, err := svc.TransferGroup(context.Background(), "ga-a", "ga-b", "pa-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if repo.transferFromUserID != "ga-a" || repo.transferToUserID != "ga-b" {
		t.Errorf("transfer args = (%q, %q), want (ga-a, ga-b)", repo.transferFromUserID, repo.transferToUserID)
	}
}

func TestAccountService_TransferGroup_InvalidTarget(t *testing.T) {
	repo := &fakeAccountRepository{transferErr: domain.ErrInvalidTransferTarget}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	_, err := svc.TransferGroup(context.Background(), "ga-a", "not-a-ga", "pa-1")
	if !errors.Is(err, domain.ErrInvalidTransferTarget) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidTransferTarget", err)
	}
}

func TestAccountService_DeleteGroupAdmin_Success(t *testing.T) {
	repo := &fakeAccountRepository{}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	if err := svc.DeleteGroupAdmin(context.Background(), "ga-a", "pa-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.deletedGroupAdminID != "ga-a" {
		t.Errorf("deletedGroupAdminID = %q, want %q", repo.deletedGroupAdminID, "ga-a")
	}
}

func TestAccountService_DeleteGroupAdmin_GroupTransferRequired(t *testing.T) {
	repo := &fakeAccountRepository{deleteGroupAdminErr: domain.ErrGroupTransferRequired}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	err := svc.DeleteGroupAdmin(context.Background(), "ga-a", "pa-1")
	if !errors.Is(err, domain.ErrGroupTransferRequired) {
		t.Errorf("err = %v, want wrapped domain.ErrGroupTransferRequired", err)
	}
}

func TestAccountService_CreateGroupAdmin_DefaultsTierToStarter(t *testing.T) {
	repo := &fakeAccountRepository{returnID: "user-1"}
	kc := &fakeKeycloakClient{userID: "kc-1"}
	svc := NewAccountService(repo, kc, zap.NewNop())

	_, err := svc.CreateGroupAdmin(context.Background(), CreateGroupAdminRequest{
		Email: "ga@example.com", DisplayName: "GA", GroupName: "PT Contoh", InvitedByUserID: "pa-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.captured.Tier != "starter" {
		t.Errorf("Tier = %q, want default %q", repo.captured.Tier, "starter")
	}
}

func TestAccountService_CreateGroupAdmin_InvalidTier(t *testing.T) {
	svc := NewAccountService(&fakeAccountRepository{}, &fakeKeycloakClient{}, zap.NewNop())

	_, err := svc.CreateGroupAdmin(context.Background(), CreateGroupAdminRequest{
		Email: "ga@example.com", DisplayName: "GA", GroupName: "PT Contoh", Tier: "gold", InvitedByUserID: "pa-1",
	})
	if !errors.Is(err, domain.ErrInvalidTier) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidTier", err)
	}
}

func TestAccountService_GetGroupAdminDetail_Success(t *testing.T) {
	repo := &fakeAccountRepository{groupAdminDetail: &repository.GroupAdminSummary{ID: "ga-1", Email: "ga@example.com"}}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	detail, err := svc.GetGroupAdminDetail(context.Background(), "ga-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail.ID != "ga-1" {
		t.Errorf("ID = %q, want %q", detail.ID, "ga-1")
	}
}

func TestAccountService_UpdateGroupAdmin_InvalidInput(t *testing.T) {
	svc := NewAccountService(&fakeAccountRepository{}, &fakeKeycloakClient{}, zap.NewNop())

	err := svc.UpdateGroupAdmin(context.Background(), "ga-1", &repository.UpdateGroupAdminParams{Tier: "starter"}, "pa-1")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}

func TestAccountService_UpdateGroupAdmin_InvalidTier(t *testing.T) {
	svc := NewAccountService(&fakeAccountRepository{}, &fakeKeycloakClient{}, zap.NewNop())

	err := svc.UpdateGroupAdmin(context.Background(), "ga-1", &repository.UpdateGroupAdminParams{
		DisplayName: "GA", GroupName: "PT Contoh", Tier: "gold",
	}, "pa-1")
	if !errors.Is(err, domain.ErrInvalidTier) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidTier", err)
	}
}

func TestAccountService_UpdateGroupAdmin_InvalidStatusTransition(t *testing.T) {
	svc := NewAccountService(&fakeAccountRepository{}, &fakeKeycloakClient{}, zap.NewNop())

	err := svc.UpdateGroupAdmin(context.Background(), "ga-1", &repository.UpdateGroupAdminParams{
		DisplayName: "GA", GroupName: "PT Contoh", Tier: "starter", NewStatus: "TIDAK AKTIF",
	}, "pa-1")
	if !errors.Is(err, domain.ErrInvalidStatusTransition) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidStatusTransition", err)
	}
}

func TestAccountService_UpdateGroupAdmin_Success(t *testing.T) {
	repo := &fakeAccountRepository{}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	err := svc.UpdateGroupAdmin(context.Background(), "ga-1", &repository.UpdateGroupAdminParams{
		DisplayName: "GA Baru", GroupName: "PT Contoh Baru", Tier: "business", NewStatus: "SUSPENDED",
	}, "pa-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.updatedGroupAdminID != "ga-1" {
		t.Errorf("updatedGroupAdminID = %q, want %q", repo.updatedGroupAdminID, "ga-1")
	}
	if repo.updatedGroupAdminParam.NewStatus != "SUSPENDED" {
		t.Errorf("NewStatus = %q, want %q", repo.updatedGroupAdminParam.NewStatus, "SUSPENDED")
	}
}

func TestAccountService_ListServiceTiers(t *testing.T) {
	repo := &fakeAccountRepository{tiers: []repository.ServiceTier{{Name: "starter"}, {Name: "business"}, {Name: "enterprise"}}}
	svc := NewAccountService(repo, &fakeKeycloakClient{}, zap.NewNop())

	tiers, err := svc.ListServiceTiers(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tiers) != 3 {
		t.Errorf("len(tiers) = %d, want 3", len(tiers))
	}
}
