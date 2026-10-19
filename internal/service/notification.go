package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// notificationStore -- interface didefinisikan di consumer, diimplementasikan *repository.NotificationRepository.
type notificationStore interface {
	List(ctx context.Context, exec db.Executor, userID, workspaceID string, limit, offset int) ([]repository.NotificationRow, int, error)
	UnreadCount(ctx context.Context, exec db.Executor, userID, workspaceID string) (int, error)
	MarkRead(ctx context.Context, exec db.Executor, userID, id string) error
	MarkAllRead(ctx context.Context, exec db.Executor, userID, workspaceID string) (int, error)
}

// NotificationService -- lonceng notifikasi in-app (US-034, S6E-14, IG-145). Selalu hanya notifikasi MILIK
// pemanggil (tanpa gerbang role -- tiap query ber-scope user); workspace_id wajib supaya cakupan tidak ambigu.
type NotificationService struct{ store notificationStore }

func NewNotificationService(store notificationStore) *NotificationService {
	return &NotificationService{store: store}
}

// NotificationPage -- satu halaman notifikasi + total + hitung belum dibaca (cakupan workspace yang sama).
type NotificationPage struct {
	Items       []repository.NotificationRow
	Total       int
	UnreadCount int
	Page        int
	PerPage     int
}

func (s *NotificationService) List(ctx context.Context, exec db.Executor, userID, workspaceID string, page, perPage int) (*NotificationPage, error) {
	if userID == "" || workspaceID == "" {
		return nil, fmt.Errorf("service.NotificationList: %w", domain.ErrInvalidInput)
	}
	page, perPage = NormalizePaging(page, perPage)
	items, total, err := s.store.List(ctx, exec, userID, workspaceID, perPage, (page-1)*perPage)
	if err != nil {
		return nil, fmt.Errorf("service.NotificationList: %w", err)
	}
	unread, err := s.store.UnreadCount(ctx, exec, userID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.NotificationList: %w", err)
	}
	return &NotificationPage{Items: items, Total: total, UnreadCount: unread, Page: page, PerPage: perPage}, nil
}

func (s *NotificationService) UnreadCount(ctx context.Context, exec db.Executor, userID, workspaceID string) (int, error) {
	if userID == "" || workspaceID == "" {
		return 0, fmt.Errorf("service.NotificationUnreadCount: %w", domain.ErrInvalidInput)
	}
	return s.store.UnreadCount(ctx, exec, userID, workspaceID)
}

func (s *NotificationService) MarkRead(ctx context.Context, exec db.Executor, userID, id string) error {
	if userID == "" || id == "" {
		return fmt.Errorf("service.NotificationMarkRead: %w", domain.ErrInvalidInput)
	}
	return s.store.MarkRead(ctx, exec, userID, id)
}

func (s *NotificationService) MarkAllRead(ctx context.Context, exec db.Executor, userID, workspaceID string) (int, error) {
	if userID == "" || workspaceID == "" {
		return 0, fmt.Errorf("service.NotificationMarkAllRead: %w", domain.ErrInvalidInput)
	}
	return s.store.MarkAllRead(ctx, exec, userID, workspaceID)
}
