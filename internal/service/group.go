package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// groupRepository -- interface didefinisikan di consumer, §3.9.
type groupRepository interface {
	IsProjectManagerInGroup(ctx context.Context, exec db.Executor, groupID string) (bool, error)
	SearchAccounts(ctx context.Context, exec db.Executor, groupID, query string) ([]repository.Account, error)
}

// groupAdminChecker -- interface didefinisikan di consumer, §3.9.
// Diimplementasikan *OrganizationService (IsGroupAdminOfGroup).
type groupAdminChecker interface {
	IsGroupAdminOfGroup(ctx context.Context, exec db.Executor, userID, groupID string) (bool, error)
}

// GroupService -- S3-20, US-009b. Otorisasi Platform Admin (bypass) ATAU
// Group Admin pengelola grup target ATAU Project Manager yang punya
// workspace di grup target -- SEMUANYA dicek di sini (bukan middleware),
// karena tidak ada gerbang platform-role/workspace-role tunggal yang pas:
// target scope-nya groupID, bukan :wsId/:orgId yang sudah punya middleware
// khusus.
type GroupService struct {
	repo groupRepository
	orgs groupAdminChecker
}

func NewGroupService(repo groupRepository, orgs groupAdminChecker) *GroupService {
	return &GroupService{repo: repo, orgs: orgs}
}

// SearchAccounts mencari user lintas organisasi dalam satu grup (S3-20).
func (s *GroupService) SearchAccounts(ctx context.Context, exec db.Executor, groupID, query, actorID, actorRole string) ([]repository.Account, error) {
	if groupID == "" {
		return nil, fmt.Errorf("service.SearchAccounts: %w", domain.ErrInvalidInput)
	}

	if actorRole != "platform_admin" {
		isGA, err := s.orgs.IsGroupAdminOfGroup(ctx, exec, actorID, groupID)
		if err != nil {
			return nil, fmt.Errorf("service.SearchAccounts: %w", err)
		}
		if !isGA {
			isPM, err := s.repo.IsProjectManagerInGroup(ctx, exec, groupID)
			if err != nil {
				return nil, fmt.Errorf("service.SearchAccounts: %w", err)
			}
			if !isPM {
				return nil, fmt.Errorf("service.SearchAccounts: %w", domain.ErrForbidden)
			}
		}
	}

	accounts, err := s.repo.SearchAccounts(ctx, exec, groupID, query)
	if err != nil {
		return nil, fmt.Errorf("service.SearchAccounts: %w", err)
	}
	return accounts, nil
}
