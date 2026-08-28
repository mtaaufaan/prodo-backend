package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// platformDashboardRepository -- subset dibutuhkan service ini, pola sama
// interface-kecil-di-service dengan accountRepository/platformAuditRepository.
type platformDashboardRepository interface {
	HealthMetrics(ctx context.Context) (repository.HealthMetrics, error)
	Trends(ctx context.Context, days int) ([]repository.TrendPoint, error)
	StorageAnomalies(ctx context.Context) ([]repository.StorageAnomaly, error)
	ContractEndingAnomalies(ctx context.Context) ([]repository.ContractEndingAnomaly, error)
}

// PlatformDashboardService -- S4P-24/25/26, US-072: KPI, tren, dan
// deteksi anomali untuk panel "Dashboard Kesehatan Platform".
type PlatformDashboardService struct {
	repo platformDashboardRepository
}

func NewPlatformDashboardService(repo platformDashboardRepository) *PlatformDashboardService {
	return &PlatformDashboardService{repo: repo}
}

func (s *PlatformDashboardService) HealthMetrics(ctx context.Context) (repository.HealthMetrics, error) {
	m, err := s.repo.HealthMetrics(ctx)
	if err != nil {
		return repository.HealthMetrics{}, fmt.Errorf("service.HealthMetrics: %w", err)
	}
	return m, nil
}

// Trends -- S4P-25. Validasi "days harus 7/30/90" dilakukan di handler
// (VALIDATION_ERROR) sebelum sampai sini, bukan di service, supaya
// pesan error HTTP-nya jelas alih-alih dibulatkan diam-diam.
func (s *PlatformDashboardService) Trends(ctx context.Context, days int) ([]repository.TrendPoint, error) {
	points, err := s.repo.Trends(ctx, days)
	if err != nil {
		return nil, fmt.Errorf("service.Trends: %w", err)
	}
	return points, nil
}

// Anomalies -- gabungan kedua jenis deteksi anomali (S4P-26) supaya
// handler cukup satu panggilan untuk mengisi tabel alert FE.
type Anomalies struct {
	Storage     []repository.StorageAnomaly
	ContractEnd []repository.ContractEndingAnomaly
}

func (s *PlatformDashboardService) Anomalies(ctx context.Context) (Anomalies, error) {
	storage, err := s.repo.StorageAnomalies(ctx)
	if err != nil {
		return Anomalies{}, fmt.Errorf("service.Anomalies: storage: %w", err)
	}
	contractEnd, err := s.repo.ContractEndingAnomalies(ctx)
	if err != nil {
		return Anomalies{}, fmt.Errorf("service.Anomalies: contract end: %w", err)
	}
	return Anomalies{Storage: storage, ContractEnd: contractEnd}, nil
}
