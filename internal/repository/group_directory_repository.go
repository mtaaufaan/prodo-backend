package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GroupDirectoryEntry -- satu baris GET /platform/groups (S4P-34, US-083).
// MinRetentionDays/MaxRetentionDays (S4G-08, Track S4G) -- plafon retensi
// tier grup ini, di-clamp [30,365] (sama logika
// OrganizationRepository.groupRetentionRange) -- dipakai FE menampilkan
// hint "RANGE {min}-{max} (BATAS TIER {nama})" di form Buat/Kelola
// Organisasi (desain "GA Add Organization.dc.html").
type GroupDirectoryEntry struct {
	ID               string
	Name             string
	TierName         string
	GANames          string // gabungan nama GA pengelola, dipisah ", " (bisa lebih dari satu)
	OrgCount         int
	MinRetentionDays int
	MaxRetentionDays int
}

type GroupDirectoryRepository struct {
	db *pgxpool.Pool
}

func NewGroupDirectoryRepository(db *pgxpool.Pool) *GroupDirectoryRepository {
	return &GroupDirectoryRepository{db: db}
}

// List -- S4P-34. Otorisasi ADA DI QUERY (bukan cuma RBAC middleware):
// prodo_is_platform_admin() (lihat semua grup) ATAU
// prodo_is_group_admin_of_group(g.id) (lihat grup yang dia kelola saja,
// via group_admin_assignments) -- fungsi SQL yang SAMA persis dipakai
// policy orgs_select sungguhan, bukan logika baru, supaya org_count di
// bawah otomatis ikut ter-scope benar tanpa risiko IG-14/IG-24 berulang.
// Keputusan disengaja (dikonfirmasi user): GA butuh akses grup miliknya
// sendiri supaya picker grup di form buat organisasi (S4P-36) tetap
// berfungsi untuk GA, bukan cuma Platform Admin.
func (r *GroupDirectoryRepository) List(ctx context.Context, actorUserID, platformRole, query string) ([]GroupDirectoryEntry, error) {
	var result []GroupDirectoryEntry
	err := withRLSContext(ctx, r.db, actorUserID, platformRole, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT g.id, g.name, st.name,
			       COALESCE(ga_agg.ga_names, ''),
			       COALESCE(org_agg.org_count, 0),
			       GREATEST(30, COALESCE(st.min_retention_days, 30)),
			       LEAST(365, COALESCE(st.max_retention_days, 365))
			FROM groups g
			JOIN service_tiers st ON st.id = g.tier_id
			LEFT JOIN LATERAL (
				SELECT string_agg(u.display_name, ', ') AS ga_names
				FROM group_admin_assignments gaa
				JOIN users u ON u.id = gaa.user_id
				WHERE gaa.group_id = g.id
			) ga_agg ON true
			LEFT JOIN LATERAL (
				SELECT count(*) AS org_count FROM organizations o WHERE o.group_id = g.id
			) org_agg ON true
			WHERE (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(g.id))
			  AND ($1 = '' OR g.name ILIKE '%' || $1 || '%' OR EXISTS (
			        SELECT 1 FROM group_admin_assignments gaa2
			        JOIN users u2 ON u2.id = gaa2.user_id
			        WHERE gaa2.group_id = g.id AND u2.display_name ILIKE '%' || $1 || '%'
			      ))
			ORDER BY g.name
		`, query)
		if err != nil {
			return fmt.Errorf("query: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var e GroupDirectoryEntry
			if err := rows.Scan(&e.ID, &e.Name, &e.TierName, &e.GANames, &e.OrgCount, &e.MinRetentionDays, &e.MaxRetentionDays); err != nil {
				return fmt.Errorf("scan: %w", err)
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("repository.GroupDirectoryRepository.List: %w", err)
	}
	return result, nil
}
