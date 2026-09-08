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
