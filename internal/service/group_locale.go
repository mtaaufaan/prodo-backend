// Package service -- GroupLocaleService (Bahasa & Format Regional lanjutan,
// Track S4G S4G-27/28, US-010, desain "GA Bahasa Lokal.dc.html"). Format
// tanggal/waktu/zona waktu/angka LEVEL GRUP -- beda dari
// OrganizationService.UpdateSettings (default_language, PER-ORGANISASI,
// S3-29-31) yang tetap dipakai apa adanya untuk tab "Bahasa Default".
package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

var validDateFormats = map[string]bool{"DD/MM/YYYY": true, "YYYY-MM-DD": true, "DD MMM YYYY": true}
var validTimeFormats = map[string]bool{"24h": true, "12h": true}
var validTimezones = map[string]bool{"Asia/Jakarta": true, "Asia/Makassar": true, "Asia/Jayapura": true, "UTC": true}
var validNumberFormats = map[string]bool{"id-ID": true, "en-US": true}

type groupLocaleRepository interface {
	GetLocale(ctx context.Context, exec db.Executor, groupID string) (*repository.GroupLocale, error)
	UpdateLocale(ctx context.Context, exec db.Executor, groupID string, locale repository.GroupLocale, actorID, actorRole string) error
}

// groupLocaleAuthorizer -- reuse OrganizationRepository, pola sama
// groupPerformanceAuthorizer.
type groupLocaleAuthorizer interface {
	IsGroupAdminOfGroup(ctx context.Context, exec db.Executor, userID, groupID string) (bool, error)
}

type GroupLocaleService struct {
	repo groupLocaleRepository
	auth groupLocaleAuthorizer
}

func NewGroupLocaleService(repo groupLocaleRepository, auth groupLocaleAuthorizer) *GroupLocaleService {
	return &GroupLocaleService{repo: repo, auth: auth}
}

func (s *GroupLocaleService) authorizeGroup(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) error {
	if actorRole == "platform_admin" {
		return nil
	}
	isGA, err := s.auth.IsGroupAdminOfGroup(ctx, exec, actorID, groupID)
	if err != nil {
		return fmt.Errorf("service.authorizeGroup: %w", err)
	}
	if !isGA {
		return fmt.Errorf("service.authorizeGroup: %w", domain.ErrForbidden)
	}
	return nil
}

func (s *GroupLocaleService) Get(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) (*repository.GroupLocale, error) {
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return nil, err
	}
	locale, err := s.repo.GetLocale(ctx, exec, groupID)
	if err != nil {
		return nil, fmt.Errorf("service.Get: %w", err)
	}
	return locale, nil
}

func (s *GroupLocaleService) Update(ctx context.Context, exec db.Executor, groupID string, locale repository.GroupLocale, actorID, actorRole string) error {
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return err
	}
	if !validDateFormats[locale.DateFormat] || !validTimeFormats[locale.TimeFormat] || !validTimezones[locale.Timezone] || !validNumberFormats[locale.NumberFormat] {
		return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
	}
	if err := s.repo.UpdateLocale(ctx, exec, groupID, locale, actorID, actorRole); err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	return nil
}
