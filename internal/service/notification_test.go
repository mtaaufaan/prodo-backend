package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// S6E-14 (IG-145): lonceng notifikasi -- validasi, paginasi, dan scope pemanggil.

type fakeNotificationStore struct {
	listCalls []struct {
		user, ws      string
		limit, offset int
	}
	unreadCalls []struct{ user, ws string }
	markRead    []struct{ user, id string }
	markAll     []struct{ user, ws string }

	rows      []repository.NotificationRow
	total     int
	unread    int
	markErr   error
	markedAll int
}

func (f *fakeNotificationStore) List(_ context.Context, _ db.Executor, userID, workspaceID string, limit, offset int) ([]repository.NotificationRow, int, error) {
	f.listCalls = append(f.listCalls, struct {
		user, ws      string
		limit, offset int
	}{userID, workspaceID, limit, offset})
	return f.rows, f.total, nil
}

func (f *fakeNotificationStore) UnreadCount(_ context.Context, _ db.Executor, userID, workspaceID string) (int, error) {
	f.unreadCalls = append(f.unreadCalls, struct{ user, ws string }{userID, workspaceID})
	return f.unread, nil
}

func (f *fakeNotificationStore) MarkRead(_ context.Context, _ db.Executor, userID, id string) error {
	f.markRead = append(f.markRead, struct{ user, id string }{userID, id})
	return f.markErr
}

func (f *fakeNotificationStore) MarkAllRead(_ context.Context, _ db.Executor, userID, workspaceID string) (int, error) {
	f.markAll = append(f.markAll, struct{ user, ws string }{userID, workspaceID})
	return f.markedAll, nil
}

func TestNotificationList_ScopedToCallerAndPaged(t *testing.T) {
	store := &fakeNotificationStore{rows: []repository.NotificationRow{{ID: "n1"}}, total: 13, unread: 4}
	svc := NewNotificationService(store)

	page, err := svc.List(context.Background(), nil, "user-1", "ws-1", 2, 10)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if page.Total != 13 || page.UnreadCount != 4 || page.Page != 2 || page.PerPage != 10 || len(page.Items) != 1 {
		t.Fatalf("halaman = %+v", page)
	}
	c := store.listCalls[0]
	if c.user != "user-1" || c.ws != "ws-1" || c.limit != 10 || c.offset != 10 {
		t.Fatalf("query = %+v, want user-1/ws-1 limit 10 offset 10", c)
	}
	if u := store.unreadCalls[0]; u.user != "user-1" || u.ws != "ws-1" {
		t.Fatalf("hitung belum dibaca harus memakai cakupan yang sama: %+v", u)
	}
}

func TestNotificationList_PagingBounds(t *testing.T) {
	for _, tc := range []struct{ page, perPage, wantPage, wantPer, wantOffset int }{
		{0, 0, 1, 10, 0},
		{-3, 5, 1, 5, 0},
		{1, 500, 1, 50, 0},
		{3, 20, 3, 20, 40},
	} {
		store := &fakeNotificationStore{}
		if _, err := NewNotificationService(store).List(context.Background(), nil, "u", "ws", tc.page, tc.perPage); err != nil {
			t.Fatal(err)
		}
		c := store.listCalls[0]
		if c.limit != tc.wantPer || c.offset != tc.wantOffset {
			t.Errorf("page=%d perPage=%d: limit=%d offset=%d, want %d/%d", tc.page, tc.perPage, c.limit, c.offset, tc.wantPer, tc.wantOffset)
		}
	}
}

func TestNotification_RejectsMissingWorkspaceOrUser(t *testing.T) {
	store := &fakeNotificationStore{}
	svc := NewNotificationService(store)
	ctx := context.Background()
	for name, err := range map[string]error{
		"list tanpa workspace":     second(svc.List(ctx, nil, "u", "", 1, 10)),
		"list tanpa user":          second(svc.List(ctx, nil, "", "ws", 1, 10)),
		"unread tanpa workspace":   second(svc.UnreadCount(ctx, nil, "u", "")),
		"read-all tanpa workspace": second(svc.MarkAllRead(ctx, nil, "u", "")),
		"mark read tanpa id":       svc.MarkRead(ctx, nil, "u", ""),
		"mark read tanpa user":     svc.MarkRead(ctx, nil, "", "n1"),
	} {
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
	if len(store.listCalls)+len(store.unreadCalls)+len(store.markRead)+len(store.markAll) != 0 {
		t.Fatal("query dijalankan padahal input tidak valid")
	}
}

func TestNotificationMarkRead_PassesCallerAndMapsNotFound(t *testing.T) {
	store := &fakeNotificationStore{}
	svc := NewNotificationService(store)
	if err := svc.MarkRead(context.Background(), nil, "user-1", "n1"); err != nil {
		t.Fatalf("err = %v", err)
	}
	if m := store.markRead[0]; m.user != "user-1" || m.id != "n1" {
		t.Fatalf("mark = %+v", m)
	}
	store.markErr = domain.ErrNotificationNotFound
	if err := svc.MarkRead(context.Background(), nil, "user-1", "milik-orang-lain"); !errors.Is(err, domain.ErrNotificationNotFound) {
		t.Fatalf("err = %v, want ErrNotificationNotFound", err)
	}
}

func TestNotificationMarkAllRead_ReturnsCount(t *testing.T) {
	store := &fakeNotificationStore{markedAll: 4}
	n, err := NewNotificationService(store).MarkAllRead(context.Background(), nil, "user-1", "ws-1")
	if err != nil || n != 4 {
		t.Fatalf("n=%d err=%v, want 4 nil", n, err)
	}
	if m := store.markAll[0]; m.user != "user-1" || m.ws != "ws-1" {
		t.Fatalf("mark all = %+v", m)
	}
}

// second -- ambil hanya error dari fungsi ber-2 hasil.
func second[T any](_ T, err error) error { return err }
