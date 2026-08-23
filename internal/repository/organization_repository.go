package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

// OrganizationRepository -- tabel organizations BELUM di-RLS (S3-42
// menyusul, lihat implementation_gaps.md IG-10/IG-13), jadi masih pakai pool
// langsung seperti AccountRepository, bukan db.Executor per-panggilan.
// Pola ini SAMA dengan WorkspaceMemberRepository sebelum S2-11 -- kalau
// S3-42 aktif, repository ini perlu direfactor menerima db.Executor persis
// seperti refactor S2-11 dulu.
type OrganizationRepository struct {
	db *pgxpool.Pool
}

func NewOrganizationRepository(db *pgxpool.Pool) *OrganizationRepository {
	return &OrganizationRepository{db: db}
}

// Organization -- subset kolom DATABASE_SCHEMA.md §5.7 yang dipakai response
// S3-02/03/04.
type Organization struct {
	ID            string
	GroupID       string
	Name          string
	Slug          string
	DeactivatedAt *time.Time
	CreatedAt     time.Time
}

// IsGroupAdminOfGroup mengecek apakah userID adalah salah satu GA yang
// di-assign ke groupID (group_admin_assignments, S3-38) -- dasar otorisasi
// scoped Group Admin di S3-02/03/04 (implementation_gaps.md IG-01).
func (r *OrganizationRepository) IsGroupAdminOfGroup(ctx context.Context, userID, groupID string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM group_admin_assignments WHERE user_id = $1 AND group_id = $2)
	`, userID, groupID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("repository.IsGroupAdminOfGroup: %w", err)
	}
	return exists, nil
}

// GetGroupID mengembalikan group_id pemilik orgID -- dipakai service
// meresolve grup mana yang harus dicek IsGroupAdminOfGroup saat Update/
// Deactivate (beda dari Create yang group_id-nya datang dari request body).
func (r *OrganizationRepository) GetGroupID(ctx context.Context, orgID string) (string, error) {
	var groupID string
	err := r.db.QueryRow(ctx, `SELECT group_id FROM organizations WHERE id = $1`, orgID).Scan(&groupID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("repository.GetGroupID: %w", domain.ErrOrganizationNotFound)
		}
		return "", fmt.Errorf("repository.GetGroupID: %w", err)
	}
	return groupID, nil
}

// Create menyimpan organisasi baru + audit trail dalam satu transaksi
// (US-007 AC: "semua aksi admin dicatat di audit trail").
func (r *OrganizationRepository) Create(ctx context.Context, groupID, name, slug, actorID, actorRole string) (*Organization, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("repository.Create: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	org := &Organization{GroupID: groupID, Name: name, Slug: slug}
	err = tx.QueryRow(ctx, `
		INSERT INTO organizations (group_id, name, slug)
		VALUES ($1, $2, $3)
		RETURNING id, created_at
	`, groupID, name, slug).Scan(&org.ID, &org.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("repository.Create: %w", classifyUniqueViolation(err, domain.ErrSlugAlreadyExists))
	}

	if err := insertOrgAudit(ctx, tx, actorID, actorRole, "organization.created", org.ID); err != nil {
		return nil, fmt.Errorf("repository.Create: audit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("repository.Create: commit tx: %w", err)
	}
	return org, nil
}

// Update mengubah name/slug organisasi -- DATABASE_SCHEMA.md §5.7 tidak
// punya kolom logo/domain seperti wording asli S3-03 di sprint_backlog.md,
// lihat catatan di sana.
func (r *OrganizationRepository) Update(ctx context.Context, orgID, name, slug, actorID, actorRole string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.Update: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	tag, err := tx.Exec(ctx, `
		UPDATE organizations SET name = $2, slug = $3, updated_at = NOW()
		WHERE id = $1
	`, orgID, name, slug)
	if err != nil {
		return fmt.Errorf("repository.Update: %w", classifyUniqueViolation(err, domain.ErrSlugAlreadyExists))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Update: %w", domain.ErrOrganizationNotFound)
	}

	if err := insertOrgAudit(ctx, tx, actorID, actorRole, "organization.updated", orgID); err != nil {
		return fmt.Errorf("repository.Update: audit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.Update: commit tx: %w", err)
	}
	return nil
}

// Deactivate menyetel deactivated_at (US-007 AC: akses member diblokir,
// data tetap tersimpan -- soft, bukan DELETE). Idempotent secara struktur
// (mengizinkan re-deactivate) TIDAK divalidasi di sini -- service yang
// menolak kalau perlu; repository murni menulis.
func (r *OrganizationRepository) Deactivate(ctx context.Context, orgID, actorID, actorRole string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.Deactivate: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	tag, err := tx.Exec(ctx, `
		UPDATE organizations SET deactivated_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND deactivated_at IS NULL
	`, orgID)
	if err != nil {
		return fmt.Errorf("repository.Deactivate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Deactivate: %w", domain.ErrOrganizationNotFound)
	}

	if err := insertOrgAudit(ctx, tx, actorID, actorRole, "organization.deactivated", orgID); err != nil {
		return fmt.Errorf("repository.Deactivate: audit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.Deactivate: commit tx: %w", err)
	}
	return nil
}

func insertOrgAudit(ctx context.Context, tx pgx.Tx, actorID, actorRole, action, orgID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, org_id)
		VALUES ($1, $2, $3, 'organization', $4, $4)
	`, actorID, actorRole, action, orgID)
	return err
}
