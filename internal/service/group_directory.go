package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// groupDirectoryRepository -- subset dibutuhkan service ini.
type groupDirectoryRepository interface {
	List(ctx context.Context, actorUserID, platformRole, query string) ([]repository.GroupDirectoryEntry, error)
}

// GroupDirectoryService -- S4P-34, US-083.
type GroupDirectoryService struct {
	repo groupDirectoryRepository
}

func NewGroupDirectoryService(repo groupDirectoryRepository) *GroupDirectoryService {
	return &GroupDirectoryService{repo: repo}
}

func (s *GroupDirectoryService) List(ctx context.Context, actorUserID, platformRole, query string) ([]repository.GroupDirectoryEntry, error) {
	entries, err := s.repo.List(ctx, actorUserID, platformRole, query)
	if err != nil {
		return nil, fmt.Errorf("service.List: %w", err)
	}
	return entries, nil
}
