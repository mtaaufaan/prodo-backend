package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakePlatformDashboardRepository struct {
	metrics          repository.HealthMetrics
	metricsErr       error
	trendDays        int
	trends           []repository.TrendPoint
	trendsErr        error
	storageAnomalies []repository.StorageAnomaly
	storageErr       error
	contractDays     int
	contractEnd      []repository.ContractEndingAnomaly
	contractErr      error
}

func (f *fakePlatformDashboardRepository) HealthMetrics(_ context.Context) (repository.HealthMetrics, error) {
	return f.metrics, f.metricsErr
}

func (f *fakePlatformDashboardRepository) Trends(_ context.Context, days int) ([]repository.TrendPoint, error) {
	f.trendDays = days
	return f.trends, f.trendsErr
}

func (f *fakePlatformDashboardRepository) StorageAnomalies(_ context.Context) ([]repository.StorageAnomaly, error) {
	return f.storageAnomalies, f.storageErr
}

func (f *fakePlatformDashboardRepository) ContractEndingAnomalies(_ context.Context, days int) ([]repository.ContractEndingAnomaly, error) {
	f.contractDays = days
	return f.contractEnd, f.contractErr
}

func TestPlatformDashboardService_HealthMetrics_PropagatesError(t *testing.T) {
	svc := NewPlatformDashboardService(&fakePlatformDashboardRepository{metricsErr: errors.New("db down")})
	_, err := svc.HealthMetrics(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestPlatformDashboardService_Trends_PassesThroughDays(t *testing.T) {
	repo := &fakePlatformDashboardRepository{trends: []repository.TrendPoint{{NewGACount: 2}}}
	svc := NewPlatformDashboardService(repo)

	points, err := svc.Trends(context.Background(), 90)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.trendDays != 90 {
		t.Errorf("trendDays = %d, want 90", repo.trendDays)
	}
	if len(points) != 1 || points[0].NewGACount != 2 {
		t.Errorf("points = %+v, want 1 point with NewGACount=2", points)
	}
}

func TestPlatformDashboardService_Anomalies_CombinesBothKinds(t *testing.T) {
	repo := &fakePlatformDashboardRepository{
		storageAnomalies: []repository.StorageAnomaly{{GroupName: "PT A"}},
		contractEnd:      []repository.ContractEndingAnomaly{{OrgName: "Org B"}},
	}
	svc := NewPlatformDashboardService(repo)

	a, err := svc.Anomalies(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.Storage) != 1 || a.Storage[0].GroupName != "PT A" {
		t.Errorf("Storage = %+v", a.Storage)
	}
	if len(a.ContractEnd) != 1 || a.ContractEnd[0].OrgName != "Org B" {
		t.Errorf("ContractEnd = %+v", a.ContractEnd)
	}
	if repo.contractDays != 7 {
		t.Errorf("contractDays = %d, want 7", repo.contractDays)
	}
}

func TestPlatformDashboardService_Anomalies_PropagatesStorageError(t *testing.T) {
	svc := NewPlatformDashboardService(&fakePlatformDashboardRepository{storageErr: errors.New("boom")})
	if _, err := svc.Anomalies(context.Background(), 7); err == nil {
		t.Fatal("expected error, got nil")
	}
}
