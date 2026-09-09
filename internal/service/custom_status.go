// Package service -- CustomStatusService (Task Management Core Phase 1).
// Baca-saja -- lihat komentar package repository.
package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type customStatusRepository interface {
	ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.CustomStatus, error)
}

type CustomStatusService struct {
	repo customStatusRepository
}

func NewCustomStatusService(repo customStatusRepository) *CustomStatusService {
	return &CustomStatusService{repo: repo}
}

func (s *CustomStatusService) ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.CustomStatus, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.ListForWorkspace: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.ListForWorkspace(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.ListForWorkspace: %w", err)
	}
	return list, nil
}
