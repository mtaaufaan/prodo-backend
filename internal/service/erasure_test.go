package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeErasureRepository struct {
	createID         string
	createErr        error
	list             []repository.ErasureRequest
	listErr          error
	sharedRole       bool
	sharedRoleErr    error
	executeErr       error
	rejectErr        error
	lastRequesterID  string
	lastTargetUserID string
}

func (f *fakeErasureRepository) Create(_ context.Context, userID, _, requestedBy, _ string) (string, error) {
	f.lastTargetUserID = userID
	f.lastRequesterID = requestedBy
	return f.createID, f.createErr
}

func (f *fakeErasureRepository) List(_ context.Context) ([]repository.ErasureRequest, error) {
	return f.list, f.listErr
}

func (f *fakeErasureRepository) HasSharedWorkspaceAdminRole(_ context.Context, _, _, _ string) (bool, error) {
	return f.sharedRole, f.sharedRoleErr
}

func (f *fakeErasureRepository) Execute(_ context.Context, _, _ string) error {
	return f.executeErr
}

func (f *fakeErasureRepository) Reject(_ context.Context, _, _ string) error {
	return f.rejectErr
}

func TestErasureService_CreateRequest_SelfAlwaysAllowed(t *testing.T) {
	repo := &fakeErasureRepository{createID: "req-1"}
	svc := NewErasureService(repo)

	id, err := svc.CreateRequest(context.Background(), "user-1", "member", "", "org-1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "req-1" {
		t.Errorf("id = %q, want req-1", id)
	}
	if repo.lastTargetUserID != "user-1" {
		t.Errorf("target defaulted to %q, want self user-1", repo.lastTargetUserID)
	}
}

func TestErasureService_CreateRequest_PlatformAdminCanRequestForOthers(t *testing.T) {
	repo := &fakeErasureRepository{createID: "req-2"}
	svc := NewErasureService(repo)

	_, err := svc.CreateRequest(context.Background(), "pa-1", "platform_admin", "user-2", "org-1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestErasureService_CreateRequest_MemberWithoutSharedWorkspaceForbidden(t *testing.T) {
	repo := &fakeErasureRepository{sharedRole: false}
	svc := NewErasureService(repo)

	_, err := svc.CreateRequest(context.Background(), "member-1", "member", "user-2", "org-1", "")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestErasureService_CreateRequest_AWWithSharedWorkspaceAllowed(t *testing.T) {
	repo := &fakeErasureRepository{sharedRole: true, createID: "req-3"}
	svc := NewErasureService(repo)

	id, err := svc.CreateRequest(context.Background(), "aw-1", "member", "user-2", "org-1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "req-3" {
		t.Errorf("id = %q, want req-3", id)
	}
}

func TestErasureService_CreateRequest_MissingOrgIDRejected(t *testing.T) {
	svc := NewErasureService(&fakeErasureRepository{})
	if _, err := svc.CreateRequest(context.Background(), "user-1", "member", "", "", ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestErasureService_Execute_RequiresExactConfirmationPhrase(t *testing.T) {
	svc := NewErasureService(&fakeErasureRepository{})
	if err := svc.Execute(context.Background(), "req-1", "pa-1", "ya eksekusi"); !errors.Is(err, domain.ErrErasureConfirmationRequired) {
		t.Fatalf("err = %v, want ErrErasureConfirmationRequired", err)
	}
}

func TestErasureService_Execute_CorrectConfirmationPassesThrough(t *testing.T) {
	repo := &fakeErasureRepository{}
	svc := NewErasureService(repo)
	if err := svc.Execute(context.Background(), "req-1", "pa-1", "KONFIRMASI"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestErasureService_Execute_PropagatesAlreadyProcessed(t *testing.T) {
	repo := &fakeErasureRepository{executeErr: domain.ErrErasureRequestAlreadyProcessed}
	svc := NewErasureService(repo)
	if err := svc.Execute(context.Background(), "req-1", "pa-1", "KONFIRMASI"); !errors.Is(err, domain.ErrErasureRequestAlreadyProcessed) {
		t.Fatalf("err = %v, want ErrErasureRequestAlreadyProcessed", err)
	}
}

func TestErasureService_Reject_PropagatesNotFound(t *testing.T) {
	repo := &fakeErasureRepository{rejectErr: domain.ErrErasureRequestNotFound}
	svc := NewErasureService(repo)
	if err := svc.Reject(context.Background(), "req-1", "pa-1"); !errors.Is(err, domain.ErrErasureRequestNotFound) {
		t.Fatalf("err = %v, want ErrErasureRequestNotFound", err)
	}
}
