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

// LogSwitch -- state_before/state_after (dipindah dari metadata "from"/"to"
// yang sebelumnya TIDAK PERNAH ditampilkan GA Audit Trail -- panel NILAI
// SEBELUM/SESUDAH cuma baca entry.state_before/state_after, bukan
// metadata) + actor_ip (implementation_gaps.md IG-64).
func (r *ContextRepository) LogSwitch(ctx context.Context, exec db.Executor, userID, fromContext, toContext string) error {
	ip, path := requestMetaFromContext(ctx)
	var metaJSON []byte
	if path != "" {
		encoded, err := marshalIfNotEmpty(map[string]any{"request_path": path})
		if err != nil {
			return fmt.Errorf("repository.LogSwitch: encode metadata: %w", err)
		}
		metaJSON = encoded
	}
	beforeJSON, err := marshalIfNotEmpty(map[string]any{"context": fromContext})
	if err != nil {
		return fmt.Errorf("repository.LogSwitch: encode state_before: %w", err)
	}
	afterJSON, err := marshalIfNotEmpty(map[string]any{"context": toContext})
	if err != nil {
		return fmt.Errorf("repository.LogSwitch: encode state_after: %w", err)
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, entity_type, entity_id, actor_ip, state_before, state_after, metadata)
		VALUES ($1, 'user.context_switch', 'user', $1, $2::inet, $3, $4, $5)
	`, userID, ip, beforeJSON, afterJSON, metaJSON)
	if err != nil {
		return fmt.Errorf("repository.LogSwitch: %w", err)
	}
	return nil
}
