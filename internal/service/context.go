package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/cache"
	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// Nilai active_context (S16-02, forward-pull Track S4G) -- adaptasi Redis,
// BUKAN klaim JWT literal: token PRODO diterbitkan Keycloak (lihat
// AuthService.syncKeycloakClaims), menyuntikkan field per-sesi-browser yang
// sifatnya sementara ke situ berarti tulis Keycloak attribute + minta
// refresh token setiap kali user klik switcher -- 2 round-trip Keycloak
// untuk aksi yang seharusnya instan. Redis (diikat ke jti, pola sama
// stepUpCacheKey) memberi hasil fungsional yang sama (satu context aktif
// per waktu, endpoint tergerbang sesuai context) tanpa menyentuh Keycloak
// sama sekali.
const (
	ContextGAConsole = "ga_console"
	ContextWorkspace = "workspace"
)

func contextCacheKey(jti string) string {
	return "context:" + jti
}

// membershipLister -- interface didefinisikan di consumer, diimplementasikan
// *WorkspaceMemberRepository.
type membershipLister interface {
	ListMembershipsForUser(ctx context.Context, exec db.Executor, userID string) ([]repository.MembershipRow, error)
}

// ContextService menangani switcher context dual-role GA (S16-01/02/03,
// US-085): GA yang juga workspace member bisa berpindah antara "Konsol
// Group Admin" dan workspace tanpa re-login.
type ContextService struct {
	memberships membershipLister
	cache       cache.Cache
}

func NewContextService(memberships membershipLister, c cache.Cache) *ContextService {
	return &ContextService{memberships: memberships, cache: c}
}

// UserContext -- hasil GET /me/context.
type UserContext struct {
	PlatformRole     string
	GAConsoleEnabled bool
	ActiveContext    string
	Workspaces       []repository.MembershipRow
}

// Get mengembalikan context aktif user saat ini + daftar workspace yang dia
// ikuti. Default (belum pernah switch, key Redis belum ada): GA dengan hak
// konsol default ke ga_console, selain itu default workspace -- konsisten
// dengan perilaku SEBELUM fitur ini ada (RoleGuard platform_role saja).
func (s *ContextService) Get(ctx context.Context, exec db.Executor, userID, jti, platformRole string) (*UserContext, error) {
	memberships, err := s.memberships.ListMembershipsForUser(ctx, exec, userID)
	if err != nil {
		return nil, fmt.Errorf("service.ContextService.Get: %w", err)
	}

	gaEnabled := platformRole == "group_admin"

	active, err := s.cache.Get(ctx, contextCacheKey(jti))
	if err != nil {
		if !errors.Is(err, cache.ErrNotFound) {
			return nil, fmt.Errorf("service.ContextService.Get: %w", err)
		}
		if gaEnabled {
			active = ContextGAConsole
		} else {
			active = ContextWorkspace
		}
	}

	return &UserContext{
		PlatformRole:     platformRole,
		GAConsoleEnabled: gaEnabled,
		ActiveContext:    active,
		Workspaces:       memberships,
	}, nil
}

// contextAuditLogger -- interface didefinisikan di consumer, diimplementasikan
// *ContextRepository (insert audit_logs SATU baris, tanpa tabel dedicated
// lain -- pola sama insertProjectMemberAudit: metadata JSONB, bukan kolom
// entity_id/workspace_id yang tidak relevan untuk event ini).
type contextAuditLogger interface {
	LogSwitch(ctx context.Context, exec db.Executor, userID, fromContext, toContext string) error
}

// Switch memvalidasi target context lalu menyetel Redis (TTL = tokenTTL,
// disuplai handler dari sisa umur token JWT yang sedang dipakai) dan
// mencatat audit trail (footer desain "GA Members Roles.dc.html":
// "Perpindahan konteks tercatat di Audit Trail workspace").
func (s *ContextService) Switch(
	ctx context.Context, exec db.Executor, audit contextAuditLogger,
	userID, jti, platformRole, target string, tokenTTL time.Duration,
) error {
	if target != ContextGAConsole && target != ContextWorkspace {
		return fmt.Errorf("service.ContextService.Switch: %w", domain.ErrInvalidInput)
	}
	if target == ContextGAConsole && platformRole != "group_admin" {
		return fmt.Errorf("service.ContextService.Switch: %w", domain.ErrForbidden)
	}

	current, err := s.cache.Get(ctx, contextCacheKey(jti))
	if err != nil && !errors.Is(err, cache.ErrNotFound) {
		return fmt.Errorf("service.ContextService.Switch: %w", err)
	}
	if current == "" {
		if platformRole == "group_admin" {
			current = ContextGAConsole
		} else {
			current = ContextWorkspace
		}
	}

	if tokenTTL <= 0 {
		tokenTTL = time.Minute // token sudah/hampir expired -- key Redis basi cepat, tidak fatal.
	}
	if err := s.cache.Set(ctx, contextCacheKey(jti), target, tokenTTL); err != nil {
		return fmt.Errorf("service.ContextService.Switch: %w", err)
	}

	if err := audit.LogSwitch(ctx, exec, userID, current, target); err != nil {
		return fmt.Errorf("service.ContextService.Switch: %w", err)
	}
	return nil
}
