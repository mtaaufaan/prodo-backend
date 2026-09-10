// Package service -- GroupSummaryService (Dashboard Ringkasan/Landing Page
// GA, Track S4G S4G-29/30, desain "GA Ringkasan.dc.html"). Murni agregasi
// dari repository yang SUDAH ADA (OrganizationRepository.List,
// GroupAuditRepository.List, RetentionRepository.ListSchedule,
// InvitationRepository.ListPendingForGroup) -- TIDAK ADA query baru, TIDAK
// ADA tabel baru.
package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

const overQuotaThreshold = 0.8

type groupSummaryOrgLister interface {
	List(ctx context.Context, exec db.Executor, groupID string) ([]repository.Organization, int64, error)
}

type groupSummaryAuditLister interface {
	List(ctx context.Context, exec db.Executor, f repository.GroupAuditLogFilter) ([]repository.GroupAuditLogEntry, int, error)
}

type groupSummaryRetentionLister interface {
	ListSchedule(ctx context.Context, exec db.Executor, groupID string) ([]repository.RetentionScheduleItem, error)
}

type groupSummaryInviteLister interface {
	ListPendingForGroup(ctx context.Context, exec db.Executor, groupID string) ([]repository.GroupPendingInvite, error)
}

// groupSummaryAuthorizer -- reuse OrganizationRepository, pola sama
// groupPerformanceAuthorizer/groupLocaleAuthorizer.
type groupSummaryAuthorizer interface {
	IsGroupAdminOfGroup(ctx context.Context, exec db.Executor, userID, groupID string) (bool, error)
}

type GroupSummaryResult struct {
	OrgTotal            int
	OrgActive           int
	OrgInactive         int
	WorkspaceTotal      int
	MemberTotal         int
	PendingInvitesTotal int
	QuotaAllocatedBytes int64
	QuotaCeilingBytes   int64
	StorageUsedBytes    int64

	Activity        []repository.GroupAuditLogEntry
	OverQuotaOrgs   []repository.Organization
	RetentionSoon   []repository.RetentionScheduleItem
	PendingInvites  []repository.GroupPendingInvite
	InactiveOrgs    []repository.Organization
	OrgDistribution []repository.Organization
}

type GroupSummaryService struct {
	orgs      groupSummaryOrgLister
	audit     groupSummaryAuditLister
	retention groupSummaryRetentionLister
	invites   groupSummaryInviteLister
	auth      groupSummaryAuthorizer
}

func NewGroupSummaryService(orgs groupSummaryOrgLister, audit groupSummaryAuditLister, retention groupSummaryRetentionLister, invites groupSummaryInviteLister, auth groupSummaryAuthorizer) *GroupSummaryService {
	return &GroupSummaryService{orgs: orgs, audit: audit, retention: retention, invites: invites, auth: auth}
}

func (s *GroupSummaryService) authorizeGroup(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) error {
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

// Get -- GET /groups/:groupId/summary. 12 aktivitas terakhir (sama batas
// "hint-placeholder-count" desain), retensi <=10 hari, kuota >=80% dari
// storage_quota_bytes (S3-32/S4G-03), pending invites gabungan
// workspace+eksekutif (ListPendingForGroup sudah menghitung keduanya).
func (s *GroupSummaryService) Get(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) (*GroupSummaryResult, error) {
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return nil, err
	}

	orgs, ceilingBytes, err := s.orgs.List(ctx, exec, groupID)
	if err != nil {
		return nil, fmt.Errorf("service.Get: orgs: %w", err)
	}
	activity, _, err := s.audit.List(ctx, exec, repository.GroupAuditLogFilter{GroupID: groupID, Limit: 12})
	if err != nil {
		return nil, fmt.Errorf("service.Get: activity: %w", err)
	}
	schedule, err := s.retention.ListSchedule(ctx, exec, groupID)
	if err != nil {
		return nil, fmt.Errorf("service.Get: retention: %w", err)
	}
	pending, err := s.invites.ListPendingForGroup(ctx, exec, groupID)
	if err != nil {
		return nil, fmt.Errorf("service.Get: invites: %w", err)
	}

	result := &GroupSummaryResult{
		OrgTotal:            len(orgs),
		QuotaCeilingBytes:   ceilingBytes,
		Activity:            activity,
		PendingInvites:      pending,
		PendingInvitesTotal: len(pending),
		OrgDistribution:     orgs,
	}
	for i := range orgs {
		o := &orgs[i]
		result.WorkspaceTotal += o.WorkspaceCount
		result.MemberTotal += o.MemberCount
		result.QuotaAllocatedBytes += o.StorageQuotaBytes
		result.StorageUsedBytes += o.StorageUsedBytes
		if o.DeactivatedAt == nil {
			result.OrgActive++
		} else {
			result.InactiveOrgs = append(result.InactiveOrgs, *o)
		}
		if o.StorageQuotaBytes > 0 && float64(o.StorageUsedBytes)/float64(o.StorageQuotaBytes) >= overQuotaThreshold {
			result.OverQuotaOrgs = append(result.OverQuotaOrgs, *o)
		}
	}
	result.OrgInactive = result.OrgTotal - result.OrgActive
	for i := range schedule {
		if schedule[i].DaysLeft <= 10 {
			result.RetentionSoon = append(result.RetentionSoon, schedule[i])
		}
	}
	return result, nil
}
