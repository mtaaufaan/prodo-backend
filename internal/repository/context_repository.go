// Package repository -- ContextRepository (S16-02, forward-pull Track S4G):
// satu-satunya tanggung jawabnya mencatat audit trail perpindahan context
// dual-role GA (docs/DATABASE_SCHEMA.md §5.27, tanpa kolom dedicated untuk
// event ini -- metadata JSONB, pola sama insertProjectMemberAudit).
package repository

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

type ContextRepository struct{}

func NewContextRepository() *ContextRepository {
	return &ContextRepository{}
}

func (r *ContextRepository) LogSwitch(ctx context.Context, exec db.Executor, userID, fromContext, toContext string) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, entity_type, entity_id, metadata)
		VALUES ($1, 'user.context_switch', 'user', $1, jsonb_build_object('from', $2::text, 'to', $3::text))
	`, userID, fromContext, toContext)
	if err != nil {
		return fmt.Errorf("repository.LogSwitch: %w", err)
	}
	return nil
}
