package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

const (
	myTaskDefaultPerPage = 10
	myTaskMaxPerPage     = 50
)

// myTaskStore -- interface didefinisikan di consumer, diimplementasikan *repository.MyTaskRepository.
type myTaskStore interface {
	List(ctx context.Context, exec db.Executor, projectID, userID, view string, limit, offset int) ([]repository.MyTaskRow, int, error)
	Stats(ctx context.Context, exec db.Executor, projectID, userID string) (repository.MyTaskStats, error)
}

// MyTaskService -- GET /me/tasks (Task Saya Editor, S6E-04, IG-135).
type MyTaskService struct{ store myTaskStore }

func NewMyTaskService(store myTaskStore) *MyTaskService { return &MyTaskService{store: store} }

// MyTaskPage -- satu halaman baris + total + statistik 4 kartu.
type MyTaskPage struct {
	Rows    []repository.MyTaskRow
	Total   int
	Page    int
	PerPage int
	Stats   repository.MyTaskStats
}

// NormalizePaging -- page minimal 1; perPage default 10, maksimal 50.
func NormalizePaging(page, perPage int) (normPage, normPerPage int) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = myTaskDefaultPerPage
	}
	if perPage > myTaskMaxPerPage {
		perPage = myTaskMaxPerPage
	}
	return page, perPage
}

// List -- view "assigned" (assignee atau PIC aktif) atau "following" (follower). view lain ditolak.
func (s *MyTaskService) List(ctx context.Context, exec db.Executor, projectID, userID, view string, page, perPage int) (*MyTaskPage, error) {
	if projectID == "" || (view != repository.MyTaskViewAssigned && view != repository.MyTaskViewFollowing) {
		return nil, fmt.Errorf("service.MyTasks: %w", domain.ErrInvalidInput)
	}
	page, perPage = NormalizePaging(page, perPage)
	rows, total, err := s.store.List(ctx, exec, projectID, userID, view, perPage, (page-1)*perPage)
	if err != nil {
		return nil, fmt.Errorf("service.MyTasks: %w", err)
	}
	stats, err := s.store.Stats(ctx, exec, projectID, userID)
	if err != nil {
		return nil, fmt.Errorf("service.MyTasks: %w", err)
	}
	return &MyTaskPage{Rows: rows, Total: total, Page: page, PerPage: perPage, Stats: stats}, nil
}
