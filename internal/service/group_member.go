package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// groupMemberReader/groupMemberWriter -- interface didefinisikan di
// consumer, diimplementasikan *GroupMemberRepository.
type groupMemberReader interface {
	ListMembers(ctx context.Context, exec db.Executor, groupID string) ([]repository.GroupMemberRow, error)
	ListMemberWorkspaceRoles(ctx context.Context, exec db.Executor, groupID string) ([]repository.MemberWorkspaceRole, error)
}

type groupMemberWriter interface {
	AssignExecutive(ctx context.Context, exec db.Executor, userID, groupID, actorID string) error
	RevokeExecutive(ctx context.Context, exec db.Executor, userID, groupID, actorID string) error
	UpdateIdentity(ctx context.Context, exec db.Executor, userID, groupID, displayName, title string) error
	SetAccess(ctx context.Context, exec db.Executor, userID, groupID, actorID string, active bool) error
}

// groupPendingLister -- interface didefinisikan di consumer, diimplementasikan
// *InvitationRepository.
type groupPendingLister interface {
	ListPendingForGroup(ctx context.Context, exec db.Executor, groupID string) ([]repository.GroupPendingInvite, error)
}

// groupAdminChecker -- didefinisikan di group.go (sama interface, dipakai
// ulang di sini -- diimplementasikan *OrganizationService).

// executiveInviter -- interface didefinisikan di consumer, diimplementasikan
// *InvitationService.
type executiveInviter interface {
	CreateExecutiveInvitation(ctx context.Context, exec db.Executor, email, groupID, invitedByUserID, groupName, inviterName string) (*Invitation, error)
}

// GroupMemberService -- Members & Roles (forward-pull US-086, Track S4G):
// direktori member group-wide + mutasi Eksekutif/identitas/akses. Setiap
// method di-gerbang authorizeGroup (Platform Admin ATAU Group Admin
// pengelola grup ini) -- pola sama WorkspaceService.ListWorkspacesByGroup.
// RLS user_invitations (migrasi 20260915090400) SUDAH menggerbangi INSERT
// undangan eksekutif juga -- authorizeGroup di sini lapisan tambahan
// supaya GA yang bukan pengelola grup dapat 403 rapi, bukan error RLS
// generik (4-layer defense, RLS_DESIGN.md §1).
type GroupMemberService struct {
	reader  groupMemberReader
	writer  groupMemberWriter
	pending groupPendingLister
	groups  groupAdminChecker
	invites executiveInviter
}

func NewGroupMemberService(reader groupMemberReader, writer groupMemberWriter, pending groupPendingLister, groups groupAdminChecker, invites executiveInviter) *GroupMemberService {
	return &GroupMemberService{reader: reader, writer: writer, pending: pending, groups: groups, invites: invites}
}

// InviteExecutive membuat undangan Eksekutif murni (email baru, tanpa
// workspace) untuk grup ini.
func (s *GroupMemberService) InviteExecutive(ctx context.Context, exec db.Executor, groupID, actorID, actorRole, email, groupName, inviterName string) (*Invitation, error) {
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return nil, fmt.Errorf("service.GroupMemberService.InviteExecutive: %w", err)
	}
	inv, err := s.invites.CreateExecutiveInvitation(ctx, exec, email, groupID, actorID, groupName, inviterName)
	if err != nil {
		return nil, fmt.Errorf("service.GroupMemberService.InviteExecutive: %w", err)
	}
	return inv, nil
}

func (s *GroupMemberService) authorizeGroup(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) error {
	if actorRole == "platform_admin" {
		return nil
	}
	isGA, err := s.groups.IsGroupAdminOfGroup(ctx, exec, actorID, groupID)
	if err != nil {
		return fmt.Errorf("authorizeGroup: %w", err)
	}
	if !isGA {
		return fmt.Errorf("authorizeGroup: %w", domain.ErrForbidden)
	}
	return nil
}

// GroupMember -- satu baris member (SUDAH punya akun) di direktori, roles
// diisi dari ListMemberWorkspaceRoles yang di-join Go-side (hindari N+1).
type GroupMember struct {
	UserID         string
	Email          string
	DisplayName    string
	IsActive       bool
	Suspended      bool
	IsGroupAdmin   bool
	IsExecutive    bool
	ExecutiveTitle string
	WorkspaceRoles []repository.MemberWorkspaceRole
}

// GroupDirectory -- hasil ListDirectory: member (real user) + pending
// (undangan belum diterima, key by email -- satu email pending bisa punya
// beberapa target workspace+role sekaligus, lihat GroupPendingInvite).
type GroupDirectory struct {
	Members []GroupMember
	Pending []repository.GroupPendingInvite
}

func (s *GroupMemberService) ListDirectory(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) (*GroupDirectory, error) {
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return nil, fmt.Errorf("service.GroupMemberService.ListDirectory: %w", err)
	}
	members, err := s.reader.ListMembers(ctx, exec, groupID)
	if err != nil {
		return nil, fmt.Errorf("service.GroupMemberService.ListDirectory: %w", err)
	}
	roles, err := s.reader.ListMemberWorkspaceRoles(ctx, exec, groupID)
	if err != nil {
		return nil, fmt.Errorf("service.GroupMemberService.ListDirectory: %w", err)
	}
	rolesByUser := make(map[string][]repository.MemberWorkspaceRole, len(members))
	for _, r := range roles {
		rolesByUser[r.UserID] = append(rolesByUser[r.UserID], r)
	}

	result := make([]GroupMember, 0, len(members))
	for _, m := range members {
		title := ""
		if m.ExecutiveTitle != nil {
			title = *m.ExecutiveTitle
		}
		result = append(result, GroupMember{
			UserID:         m.UserID,
			Email:          m.Email,
			DisplayName:    m.DisplayName,
			IsActive:       m.IsActive,
			Suspended:      m.SuspendedAt != nil,
			IsGroupAdmin:   m.IsGroupAdmin,
			IsExecutive:    m.IsExecutive,
			ExecutiveTitle: title,
			WorkspaceRoles: rolesByUser[m.UserID],
		})
	}

	pending, err := s.pending.ListPendingForGroup(ctx, exec, groupID)
	if err != nil {
		return nil, fmt.Errorf("service.GroupMemberService.ListDirectory: %w", err)
	}

	return &GroupDirectory{Members: result, Pending: pending}, nil
}

func (s *GroupMemberService) AssignExecutive(ctx context.Context, exec db.Executor, userID, groupID, actorID, actorRole string) error {
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.GroupMemberService.AssignExecutive: %w", err)
	}
	if err := s.writer.AssignExecutive(ctx, exec, userID, groupID, actorID); err != nil {
		return fmt.Errorf("service.GroupMemberService.AssignExecutive: %w", err)
	}
	return nil
}

func (s *GroupMemberService) RevokeExecutive(ctx context.Context, exec db.Executor, userID, groupID, actorID, actorRole string) error {
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.GroupMemberService.RevokeExecutive: %w", err)
	}
	if err := s.writer.RevokeExecutive(ctx, exec, userID, groupID, actorID); err != nil {
		return fmt.Errorf("service.GroupMemberService.RevokeExecutive: %w", err)
	}
	return nil
}

func (s *GroupMemberService) UpdateIdentity(ctx context.Context, exec db.Executor, userID, groupID, actorID, actorRole, displayName, title string) error {
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.GroupMemberService.UpdateIdentity: %w", err)
	}
	if len(displayName) < 2 {
		return fmt.Errorf("service.GroupMemberService.UpdateIdentity: %w", domain.ErrInvalidInput)
	}
	if err := s.writer.UpdateIdentity(ctx, exec, userID, groupID, displayName, title); err != nil {
		return fmt.Errorf("service.GroupMemberService.UpdateIdentity: %w", err)
	}
	return nil
}

func (s *GroupMemberService) SetAccess(ctx context.Context, exec db.Executor, userID, groupID, actorID, actorRole string, active bool) error {
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.GroupMemberService.SetAccess: %w", err)
	}
	if err := s.writer.SetAccess(ctx, exec, userID, groupID, actorID, active); err != nil {
		return fmt.Errorf("service.GroupMemberService.SetAccess: %w", err)
	}
	return nil
}
