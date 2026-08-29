package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// HealthMetrics -- S4P-24, GET /platform/health-metrics.
type HealthMetrics struct {
	ActiveGACount        int
	ActiveOrgCount       int
	TotalStorageUsedByte int64
	TierDistribution     map[string]int
}

// TrendPoint -- satu titik S4P-25, GET /platform/trends.
type TrendPoint struct {
	Date        time.Time
	NewGACount  int
	NewOrgCount int
}

// StorageAnomaly -- S4P-26, grup yang total pemakaian storage organisasi
// di dalamnya mendekati/melewati plafon (storage_quota_gb grup, fallback
// ke service_tiers.max_storage_gb kalau grup belum override manual --
// pola sama dengan groupAdminSummaryQuery/S4P-12). Threshold 80%/95%
// (dikonfirmasi user 2026-08-29) mengikuti persis peringatan kuota di
// docs/prd.md ("80% peringatan awal, 95% peringatan kritis") -- tetap
// di level GRUP (agregat seluruh organisasi di dalamnya), bukan per
// organisasi, karena scope dashboard Platform Admin memang grup;
// >100% sengaja tidak dijadikan syarat sendiri karena upload real
// diblokir sebelum kuota organisasi penuh (lihat DATABASE_SCHEMA.md
// §5.28), jadi ambang 95% sudah menangkap kasus itu juga.
type StorageAnomaly struct {
	GroupID   string
	GroupName string
	UsedMB    int64
	QuotaGB   int
	Severity  string // "warning" (>=80%) atau "critical" (>=95%)
}

// ContractEndingAnomaly -- S4P-26, organisasi dengan contract_end_at
// dalam <30 hari (termasuk yang SUDAH lewat -- lihat komentar Anomalies).
type ContractEndingAnomaly struct {
	OrgID         string
	OrgName       string
	GroupName     string
	ContractEndAt time.Time
}

type PlatformDashboardRepository struct {
	db *pgxpool.Pool
}

func NewPlatformDashboardRepository(db *pgxpool.Pool) *PlatformDashboardRepository {
	return &PlatformDashboardRepository{db: db}
}

// HealthMetrics -- S4P-24. organizations kena RLS (FORCE ROW LEVEL
// SECURITY sejak S3-42), jadi WAJIB withPlatformAdminRLS -- pola sama
// IG-24, tanpa ini active_org_count/TotalStorageUsedByte diam-diam 0.
func (r *PlatformDashboardRepository) HealthMetrics(ctx context.Context) (HealthMetrics, error) {
	var m HealthMetrics
	err := withPlatformAdminRLS(ctx, r.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM users
			WHERE platform_role = 'group_admin' AND is_active = TRUE AND suspended_at IS NULL AND deleted_at IS NULL
		`).Scan(&m.ActiveGACount); err != nil {
			return fmt.Errorf("active ga count: %w", err)
		}

		if err := tx.QueryRow(ctx, `
			SELECT count(*), COALESCE(sum(storage_used_mb), 0) FROM organizations WHERE deactivated_at IS NULL
		`).Scan(&m.ActiveOrgCount, &m.TotalStorageUsedByte); err != nil {
			return fmt.Errorf("org metrics: %w", err)
		}
		m.TotalStorageUsedByte *= 1024 * 1024 // MB -> byte, sesuai nama field AC (total_storage_used_bytes)

		rows, err := tx.Query(ctx, `
			SELECT st.name, count(g.id)
			FROM service_tiers st
			LEFT JOIN groups g ON g.tier_id = st.id
			GROUP BY st.name
		`)
		if err != nil {
			return fmt.Errorf("tier distribution: %w", err)
		}
		defer rows.Close()
		m.TierDistribution = make(map[string]int)
		for rows.Next() {
			var name string
			var count int
			if err := rows.Scan(&name, &count); err != nil {
				return fmt.Errorf("tier distribution scan: %w", err)
			}
			m.TierDistribution[name] = count
		}
		return rows.Err()
	})
	if err != nil {
		return HealthMetrics{}, fmt.Errorf("repository.PlatformDashboardRepository.HealthMetrics: %w", err)
	}
	return m, nil
}

// Trends -- S4P-25. Menggabungkan hitungan GA baru (users, tidak ber-RLS)
// dan organisasi baru (organizations, ber-RLS) per hari dalam `days`
// terakhir, termasuk hari tanpa kejadian (count 0) supaya chart FE tidak
// perlu mengisi celah tanggal sendiri.
func (r *PlatformDashboardRepository) Trends(ctx context.Context, days int) ([]TrendPoint, error) {
	byDate := make(map[string]*TrendPoint)
	err := withPlatformAdminRLS(ctx, r.db, func(tx pgx.Tx) error {
		gaRows, err := tx.Query(ctx, `
			SELECT date_trunc('day', created_at)::date, count(*)
			FROM users
			WHERE platform_role = 'group_admin' AND created_at >= NOW() - make_interval(days => $1)
			GROUP BY 1
		`, days)
		if err != nil {
			return fmt.Errorf("ga trend: %w", err)
		}
		defer gaRows.Close()
		for gaRows.Next() {
			var d time.Time
			var c int
			if err := gaRows.Scan(&d, &c); err != nil {
				return fmt.Errorf("ga trend scan: %w", err)
			}
			byDate[d.Format("2006-01-02")] = &TrendPoint{Date: d, NewGACount: c}
		}
		if err := gaRows.Err(); err != nil {
			return err
		}

		orgRows, err := tx.Query(ctx, `
			SELECT date_trunc('day', created_at)::date, count(*)
			FROM organizations
			WHERE created_at >= NOW() - make_interval(days => $1)
			GROUP BY 1
		`, days)
		if err != nil {
			return fmt.Errorf("org trend: %w", err)
		}
		defer orgRows.Close()
		for orgRows.Next() {
			var d time.Time
			var c int
			if err := orgRows.Scan(&d, &c); err != nil {
				return fmt.Errorf("org trend scan: %w", err)
			}
			key := d.Format("2006-01-02")
			if p, ok := byDate[key]; ok {
				p.NewOrgCount = c
			} else {
				byDate[key] = &TrendPoint{Date: d, NewOrgCount: c}
			}
		}
		return orgRows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("repository.PlatformDashboardRepository.Trends: %w", err)
	}

	// Isi celah tanggal (hari tanpa kejadian sama sekali tidak muncul di
	// query GROUP BY manapun) supaya chart FE dapat deret tanggal utuh.
	points := make([]TrendPoint, 0, days)
	now := time.Now().UTC().Truncate(24 * time.Hour)
	for i := days - 1; i >= 0; i-- {
		d := now.AddDate(0, 0, -i)
		key := d.Format("2006-01-02")
		if p, ok := byDate[key]; ok {
			points = append(points, *p)
		} else {
			points = append(points, TrendPoint{Date: d})
		}
	}
	return points, nil
}

// Anomalies -- S4P-26.
func (r *PlatformDashboardRepository) StorageAnomalies(ctx context.Context) ([]StorageAnomaly, error) {
	var result []StorageAnomaly
	err := withPlatformAdminRLS(ctx, r.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT g.id, g.name, COALESCE(org_agg.used_mb, 0), COALESCE(g.storage_quota_gb, st.max_storage_gb),
				CASE WHEN COALESCE(org_agg.used_mb, 0) >= COALESCE(g.storage_quota_gb, st.max_storage_gb) * 1024 * 0.95
					THEN 'critical' ELSE 'warning' END
			FROM groups g
			JOIN service_tiers st ON st.id = g.tier_id
			LEFT JOIN LATERAL (
				SELECT sum(o.storage_used_mb) AS used_mb FROM organizations o WHERE o.group_id = g.id
			) org_agg ON true
			WHERE COALESCE(org_agg.used_mb, 0) >= COALESCE(g.storage_quota_gb, st.max_storage_gb) * 1024 * 0.80
		`)
		if err != nil {
			return fmt.Errorf("query: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var a StorageAnomaly
			if err := rows.Scan(&a.GroupID, &a.GroupName, &a.UsedMB, &a.QuotaGB, &a.Severity); err != nil {
				return fmt.Errorf("scan: %w", err)
			}
			result = append(result, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("repository.PlatformDashboardRepository.StorageAnomalies: %w", err)
	}
	return result, nil
}

// ContractEndingAnomalies -- S4P-26 AC: "org dengan contract_end_date <30
// hari". Jendela simetris [-days, +days] dari sekarang (dikonfirmasi user
// 2026-08-29): sisi depan tetap membatasi lookahead, sisi belakang
// mencegah kontrak yang sudah lama kedaluwarsa & tak pernah ditindak
// menumpuk selamanya di alert -- sebelumnya sengaja tanpa batas bawah,
// keputusan itu dibalik atas permintaan user karena jadi noise.
func (r *PlatformDashboardRepository) ContractEndingAnomalies(ctx context.Context, days int) ([]ContractEndingAnomaly, error) {
	var result []ContractEndingAnomaly
	err := withPlatformAdminRLS(ctx, r.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT o.id, o.name, g.name, o.contract_end_at
			FROM organizations o
			JOIN groups g ON g.id = o.group_id
			WHERE o.contract_end_at IS NOT NULL
				AND o.contract_end_at BETWEEN NOW() - make_interval(days => $1) AND NOW() + make_interval(days => $1)
			ORDER BY o.contract_end_at
		`, days)
		if err != nil {
			return fmt.Errorf("query: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var a ContractEndingAnomaly
			if err := rows.Scan(&a.OrgID, &a.OrgName, &a.GroupName, &a.ContractEndAt); err != nil {
				return fmt.Errorf("scan: %w", err)
			}
			result = append(result, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("repository.PlatformDashboardRepository.ContractEndingAnomalies: %w", err)
	}
	return result, nil
}
