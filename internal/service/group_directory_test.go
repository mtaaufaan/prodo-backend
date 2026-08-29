package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeGroupDirectoryRepository struct {
	entries        []repository.GroupDirectoryEntry
	err            error
	lastActorID    string
	lastPlatformRl string
	lastQuery      string
}

func (f *fakeGroupDirectoryRepository) List(_ context.Context, actorUserID, platformRole, query string) ([]repository.GroupDirectoryEntry, error) {
	f.lastActorID = actorUserID
	f.lastPlatformRl = platformRole
	f.lastQuery = query
	return f.entries, f.err
}

func TestGroupDirectoryService_List_PassesActorAndQueryThrough(t *testing.T) {
	repo := &fakeGroupDirectoryRepository{entries: []repository.GroupDirectoryEntry{{Name: "PT Contoh"}}}
	svc := NewGroupDirectoryService(repo)

	entries, err := svc.List(context.Background(), "ga-1", "group_admin", "contoh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "PT Contoh" {
		t.Errorf("entries = %+v", entries)
	}
	if repo.lastActorID != "ga-1" || repo.lastPlatformRl != "group_admin" || repo.lastQuery != "contoh" {
		t.Errorf("repo received actorID=%q role=%q query=%q", repo.lastActorID, repo.lastPlatformRl, repo.lastQuery)
	}
}

func TestGroupDirectoryService_List_PropagatesError(t *testing.T) {
	svc := NewGroupDirectoryService(&fakeGroupDirectoryRepository{err: errors.New("db down")})
	if _, err := svc.List(context.Background(), "pa-1", "platform_admin", ""); err == nil {
		t.Fatal("expected error, got nil")
	}
}
