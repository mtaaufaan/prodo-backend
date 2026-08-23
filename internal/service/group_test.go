package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeGroupRepo struct {
	isPM         bool
	isPMErr      error
	searchResult []repository.Account
	searchErr    error
}

func (f *fakeGroupRepo) IsProjectManagerInGroup(_ context.Context, _ db.Executor, _ string) (bool, error) {
	return f.isPM, f.isPMErr
}

func (f *fakeGroupRepo) SearchAccounts(_ context.Context, _ db.Executor, _, _ string) ([]repository.Account, error) {
	return f.searchResult, f.searchErr
}

type fakeGroupAdminChecker struct {
	isGA    bool
	isGAErr error
}

func (f *fakeGroupAdminChecker) IsGroupAdminOfGroup(_ context.Context, _ db.Executor, _, _ string) (bool, error) {
	return f.isGA, f.isGAErr
}

func TestGroupService_SearchAccounts_PlatformAdminBypass(t *testing.T) {
	repo := &fakeGroupRepo{searchResult: []repository.Account{{UserID: "user-1"}}}
	svc := NewGroupService(repo, &fakeGroupAdminChecker{})

	accounts, err := svc.SearchAccounts(context.Background(), nil, "group-1", "budi", "pa-1", "platform_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 1 {
		t.Errorf("accounts = %+v, unexpected", accounts)
	}
}

func TestGroupService_SearchAccounts_GroupAdminOfGroup_Allowed(t *testing.T) {
	repo := &fakeGroupRepo{searchResult: []repository.Account{{UserID: "user-1"}}}
	svc := NewGroupService(repo, &fakeGroupAdminChecker{isGA: true})

	_, err := svc.SearchAccounts(context.Background(), nil, "group-1", "", "ga-1", "group_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGroupService_SearchAccounts_ProjectManagerInGroup_Allowed(t *testing.T) {
	repo := &fakeGroupRepo{isPM: true, searchResult: []repository.Account{{UserID: "user-1"}}}
	svc := NewGroupService(repo, &fakeGroupAdminChecker{isGA: false})

	_, err := svc.SearchAccounts(context.Background(), nil, "group-1", "", "pm-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGroupService_SearchAccounts_NeitherGaNorPm_Forbidden(t *testing.T) {
	repo := &fakeGroupRepo{isPM: false}
	svc := NewGroupService(repo, &fakeGroupAdminChecker{isGA: false})

	_, err := svc.SearchAccounts(context.Background(), nil, "group-1", "", "member-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestGroupService_SearchAccounts_MissingGroupID(t *testing.T) {
	svc := NewGroupService(&fakeGroupRepo{}, &fakeGroupAdminChecker{})

	_, err := svc.SearchAccounts(context.Background(), nil, "", "", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}
