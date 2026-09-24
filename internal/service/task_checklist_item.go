// Package service -- TaskChecklistItemService (SUB-TASK, "PM Task
// Detail.dc.html", IG-97 susulan). Otorisasi murni lewat RLS
// task_checklist_items_select/_write (siapa pun yang boleh akses task
// induk boleh akses checklist item-nya) -- TIDAK ada visibilitas
// per-user berbeda seperti Timesheet (semua project member lihat
// checklist yang sama), jadi tidak perlu authorize() app-layer tambahan.
package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type taskChecklistItemRepository interface {
	Create(ctx context.Context, exec db.Executor, taskID, title, createdBy string) (*repository.TaskChecklistItem, error)
	ListForTask(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskChecklistItem, error)
	GetTaskID(ctx context.Context, exec db.Executor, itemID string) (string, error)
	Update(ctx context.Context, exec db.Executor, itemID string, title *string, isDone *bool) error
	Delete(ctx context.Context, exec db.Executor, itemID string) error
}

type TaskChecklistItemService struct {
	repo taskChecklistItemRepository
}

func NewTaskChecklistItemService(repo taskChecklistItemRepository) *TaskChecklistItemService {
	return &TaskChecklistItemService{repo: repo}
}

func (s *TaskChecklistItemService) Create(ctx context.Context, exec db.Executor, taskID, title, actorID string) (*repository.TaskChecklistItem, error) {
	title = strings.TrimSpace(title)
	if taskID == "" || title == "" {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrInvalidInput)
	}
	item, err := s.repo.Create(ctx, exec, taskID, title, actorID)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	return item, nil
}

func (s *TaskChecklistItemService) ListForTask(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskChecklistItem, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.ListForTask: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.ListForTask(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.ListForTask: %w", err)
	}
	return list, nil
}

// GetTaskID -- dipakai handler untuk invalidasi cache FE lewat taskID
// (route PATCH/DELETE item cuma py :itemId).
func (s *TaskChecklistItemService) GetTaskID(ctx context.Context, exec db.Executor, itemID string) (string, error) {
	return s.repo.GetTaskID(ctx, exec, itemID)
}

func (s *TaskChecklistItemService) Update(ctx context.Context, exec db.Executor, itemID string, title *string, isDone *bool) error {
	if itemID == "" {
		return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
	}
	if title != nil {
		trimmed := strings.TrimSpace(*title)
		if trimmed == "" {
			return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
		}
		title = &trimmed
	}
	if err := s.repo.Update(ctx, exec, itemID, title, isDone); err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	return nil
}

func (s *TaskChecklistItemService) Delete(ctx context.Context, exec db.Executor, itemID string) error {
	if itemID == "" {
		return fmt.Errorf("service.Delete: %w", domain.ErrInvalidInput)
	}
	if err := s.repo.Delete(ctx, exec, itemID); err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	return nil
}
