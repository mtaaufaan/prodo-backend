// Package repository -- GroupRepository (S3-20, US-009b). Query lewat
// function SQL SECURITY DEFINER (prodo_is_project_manager_in_group,
// prodo_search_accounts_in_group) -- lihat komentar migrasi
// 20260828090000_group_account_search_functions untuk alasan bypass RLS
// yang disengaja (PM harus bisa lihat user lintas org DALAM GRUP yang
// sama, RLS normal sengaja membatasi visibility per-org).
package repository

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type GroupRepository struct{}

func NewGroupRepository() *GroupRepository {
	return &GroupRepository{}
}

// Account -- satu baris hasil SearchAccounts.
type Account struct {
	UserID      string
	Email       string
	DisplayName string
	OrgID       string
	OrgName     string
}

// GetName -- dipakai isi email undangan Eksekutif (Members & Roles, Track
// S4G) supaya subjek email pakai nama grup, bukan UUID. groups TIDAK
// ber-RLS (tabel level-platform), query polos aman lewat exec mana pun.
func (r *GroupRepository) GetName(ctx context.Context, exec db.Executor, groupID string) (string, error) {
	var name string
	if err := exec.QueryRow(ctx, `SELECT name FROM groups WHERE id = $1`, groupID).Scan(&name); err != nil {
		return "", fmt.Errorf("repository.GetName: %w", err)
	}
	return name, nil
}

// IsProjectManagerInGroup mengecek apakah actor (dari session RLS, exec)
// punya role project_manager di SALAH SATU workspace dalam grup groupID.
func (r *GroupRepository) IsProjectManagerInGroup(ctx context.Context, exec db.Executor, groupID string) (bool, error) {
	var isPM bool
	if err := exec.QueryRow(ctx, `SELECT prodo_is_project_manager_in_group($1)`, groupID).Scan(&isPM); err != nil {
		return false, fmt.Errorf("repository.IsProjectManagerInGroup: %w", err)
	}
	return isPM, nil
}

// SearchAccounts mencari user lintas organisasi DALAM SATU GRUP (S3-20) --
// query kosong mengembalikan seluruh member grup (dibatasi LIMIT 50 di
// function SQL).
func (r *GroupRepository) SearchAccounts(ctx context.Context, exec db.Executor, groupID, query string) ([]Account, error) {
	rows, err := exec.Query(ctx, `SELECT user_id, email, display_name, org_id, org_name FROM prodo_search_accounts_in_group($1, $2)`, groupID, query)
	if err != nil {
		return nil, fmt.Errorf("repository.SearchAccounts: %w", err)
	}
	defer rows.Close()

	accounts := make([]Account, 0)
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.UserID, &a.Email, &a.DisplayName, &a.OrgID, &a.OrgName); err != nil {
			return nil, fmt.Errorf("repository.SearchAccounts: scan: %w", err)
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.SearchAccounts: %w", err)
	}
	return accounts, nil
}

// GroupLocale -- format tanggal/waktu/zona waktu/angka LEVEL GRUP (S4G-27,
// US-010 lanjutan), migrasi 20261004090000. Beda dari
// organizations.default_language (S3-29-31, per-organisasi).
type GroupLocale struct {
	DateFormat   string
	TimeFormat   string
	Timezone     string
	NumberFormat string
}

// GetLocale -- groups TIDAK ber-RLS (tabel level-platform, lihat komentar
// GetName), query polos aman lewat exec mana pun.
func (r *GroupRepository) GetLocale(ctx context.Context, exec db.Executor, groupID string) (*GroupLocale, error) {
	var l GroupLocale
	err := exec.QueryRow(ctx, `
		SELECT locale_date_format, locale_time_format, locale_timezone, locale_number_format
		FROM groups WHERE id = $1
	`, groupID).Scan(&l.DateFormat, &l.TimeFormat, &l.Timezone, &l.NumberFormat)
	if err != nil {
		return nil, fmt.Errorf("repository.GetLocale: %w", err)
	}
	return &l, nil
}

// UpdateLocale mencatat before/after ke Audit Trail GA (pola sama
// insertWebhookAudit -- entitas ini milik GRUP, bukan organisasi tunggal,
// jadi org_id NULL + group_id di metadata supaya tetap ketemu lewat policy
// audit_logs_select_group_admin, IG-45).
func (r *GroupRepository) UpdateLocale(ctx context.Context, exec db.Executor, groupID string, locale GroupLocale, actorID, actorRole string) error {
	before, err := r.GetLocale(ctx, exec, groupID)
	if err != nil {
		return fmt.Errorf("repository.UpdateLocale: %w", err)
	}

	tag, err := exec.Exec(ctx, `
		UPDATE groups SET
			locale_date_format = $2::group_date_format,
			locale_time_format = $3::group_time_format,
			locale_timezone = $4::group_timezone,
			locale_number_format = $5::group_number_format,
			updated_at = NOW()
		WHERE id = $1
	`, groupID, locale.DateFormat, locale.TimeFormat, locale.Timezone, locale.NumberFormat)
	if err != nil {
		return fmt.Errorf("repository.UpdateLocale: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.UpdateLocale: %w", domain.ErrGroupNotFound)
	}

	stateBefore := map[string]any{"date_format": before.DateFormat, "time_format": before.TimeFormat, "timezone": before.Timezone, "number_format": before.NumberFormat}
	stateAfter := map[string]any{"date_format": locale.DateFormat, "time_format": locale.TimeFormat, "timezone": locale.Timezone, "number_format": locale.NumberFormat}
	if err := insertGroupAudit(ctx, exec, actorID, actorRole, "group.locale_updated", groupID, stateBefore, stateAfter); err != nil {
		return fmt.Errorf("repository.UpdateLocale: audit: %w", err)
	}
	return nil
}

// insertGroupAudit -- reuse writeAuditLog (chokepoint audit_logs, lihat
// insertWebhookAudit) untuk aksi level GRUP (bukan organisasi tunggal):
// org_id NULL, group_id di metadata.
func insertGroupAudit(ctx context.Context, exec execer, actorID, actorRole, action, groupID string, stateBefore, stateAfter map[string]any) error {
	return writeAuditLog(ctx, exec, "audit_logs", actorID, actorRole, action, "group", &groupID,
		map[string]any{"group_id": groupID}, stateBefore, stateAfter)
}
