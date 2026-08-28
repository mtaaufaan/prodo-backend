package service

import (
	"context"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakePlatformAuditRepository struct {
	capturedFilter repository.PlatformAuditLogFilter
	entries        []repository.PlatformAuditLogEntry
	total          int
	err            error
}

func (f *fakePlatformAuditRepository) List(_ context.Context, filter repository.PlatformAuditLogFilter) ([]repository.PlatformAuditLogEntry, int, error) {
	f.capturedFilter = filter
	return f.entries, f.total, f.err
}

func TestPlatformAuditService_ListAuditLogs_DefaultsLimit(t *testing.T) {
	repo := &fakePlatformAuditRepository{}
	svc := NewPlatformAuditService(repo)

	if _, _, err := svc.ListAuditLogs(context.Background(), repository.PlatformAuditLogFilter{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.capturedFilter.Limit != defaultAuditLogPerPage {
		t.Errorf("Limit = %d, want %d", repo.capturedFilter.Limit, defaultAuditLogPerPage)
	}
}

func TestPlatformAuditService_ListAuditLogs_ClampsLimitToMax(t *testing.T) {
	repo := &fakePlatformAuditRepository{}
	svc := NewPlatformAuditService(repo)

	if _, _, err := svc.ListAuditLogs(context.Background(), repository.PlatformAuditLogFilter{Limit: 9999}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.capturedFilter.Limit != maxAuditLogPerPage {
		t.Errorf("Limit = %d, want %d", repo.capturedFilter.Limit, maxAuditLogPerPage)
	}
}

func TestPlatformAuditService_ListAuditLogs_ClampsNegativeOffset(t *testing.T) {
	repo := &fakePlatformAuditRepository{}
	svc := NewPlatformAuditService(repo)

	if _, _, err := svc.ListAuditLogs(context.Background(), repository.PlatformAuditLogFilter{Offset: -5}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.capturedFilter.Offset != 0 {
		t.Errorf("Offset = %d, want 0", repo.capturedFilter.Offset)
	}
}

func TestPlatformAuditService_ListAuditLogs_PassesThroughActionTypeFilter(t *testing.T) {
	repo := &fakePlatformAuditRepository{}
	svc := NewPlatformAuditService(repo)

	if _, _, err := svc.ListAuditLogs(context.Background(), repository.PlatformAuditLogFilter{ActionType: "tier.updated"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.capturedFilter.ActionType != "tier.updated" {
		t.Errorf("ActionType = %q, want %q", repo.capturedFilter.ActionType, "tier.updated")
	}
}
