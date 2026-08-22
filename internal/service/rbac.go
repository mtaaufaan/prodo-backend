package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/cache"
)

// workspaceMemberRepository -- interface didefinisikan di consumer, §3.9.
type workspaceMemberRepository interface {
	GetRole(ctx context.Context, workspaceID, userID string) (string, error)
	UpsertRole(ctx context.Context, workspaceID, userID, role string, invitedBy *string) error
}

// RBACService menangani assignment role per-workspace (S2-03, US-002).
type RBACService struct {
	repo  workspaceMemberRepository
	cache cache.Cache
}

func NewRBACService(repo workspaceMemberRepository, c cache.Cache) *RBACService {
	return &RBACService{repo: repo, cache: c}
}

// roleCacheKey -- dibaca RequireRole middleware (S2-09) untuk hindari query
// DB di setiap request, sama pola dengan session:revoked:<jti> (S1-32).
func roleCacheKey(userID, workspaceID string) string {
	return "role:" + userID + ":" + workspaceID
}

// RoleChangeResult -- hasil AssignRole, dipakai caller (notifikasi S2-05 +
// audit trail S2-06) untuk tahu role sebelum/sesudah. PreviousRole "" kalau
// user sebelumnya belum jadi member workspace ini.
type RoleChangeResult struct {
	PreviousRole string
	NewRole      string
}

// AssignRole menetapkan role user di workspace (S2-03) -- INSERT kalau
// belum member, UPDATE kalau sudah. Cache Redis role:<user>:<workspace>
// di-invalidate setelah write supaya pembaca berikutnya baca dari DB,
// bukan role basi.
func (s *RBACService) AssignRole(ctx context.Context, workspaceID, userID, role string, invitedBy *string) (*RoleChangeResult, error) {
	previousRole, err := s.repo.GetRole(ctx, workspaceID, userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("service.AssignRole: cek role lama: %w", err)
	}

	if err := s.repo.UpsertRole(ctx, workspaceID, userID, role, invitedBy); err != nil {
		return nil, fmt.Errorf("service.AssignRole: %w", err)
	}

	if err := s.cache.Del(ctx, roleCacheKey(userID, workspaceID)); err != nil {
		return nil, fmt.Errorf("service.AssignRole: invalidate cache: %w", err)
	}

	return &RoleChangeResult{PreviousRole: previousRole, NewRole: role}, nil
}
