package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

var projectCodePattern = regexp.MustCompile(`^[A-Z]{2,5}$`)

// projectRepository -- interface didefinisikan di consumer, §3.9.
type projectRepository interface {
	GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error)
	Create(ctx context.Context, exec db.Executor, workspaceID, name, code, pmUserID, actorID, actorRole string) (*repository.Project, error)
	List(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.Project, error)
	Update(ctx context.Context, exec db.Executor, projectID, name, pmUserID, actorID, actorRole string) error
	SetArchived(ctx context.Context, exec db.Executor, projectID string, archive bool, actorID, actorRole string) error
	SoftDelete(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error
	Restore(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error
}

// ProjectService -- S4-02/03, US-012. Route POST/GET /workspaces/:wsId/projects
// digerbangi middleware.RequireRole (punya :wsId). Route PUT/DELETE/archive
// /projects/:id TIDAK (tidak ada :wsId di path) -- otorisasi penuh lewat
// authorize() di sini, sama pola ProjectMemberService.
type ProjectService struct {
	repo projectRepository
	orgs orgAuthorizer
	rbac projectRoleChecker
}

func NewProjectService(repo projectRepository, orgs orgAuthorizer, rbac projectRoleChecker) *ProjectService {
	return &ProjectService{repo: repo, orgs: orgs, rbac: rbac}
}

// authorize menolak actor yang bukan PA/GA-of-org/AW/PM di workspace
// pemilik projectID -- dipakai Update/SetArchived/SoftDelete (soft-delete,
// BUKAN hard-delete, jadi AW/PM ikut boleh -- lihat komentar
// ProjectRepository.SoftDelete). Mengembalikan workspaceID untuk caller.
func (s *ProjectService) authorize(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) (string, error) {
	workspaceID, err := s.repo.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return "", err
	}
	orgID, err := s.rbac.GetWorkspaceOrgID(ctx, exec, workspaceID)
	if err != nil {
		return "", fmt.Errorf("service.authorize: %w", err)
	}
	if err := s.orgs.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err == nil {
		return workspaceID, nil
	}

	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return "", fmt.Errorf("service.authorize: %w", err)
	}
	if role != "admin_workspace" && role != "project_manager" {
		return "", fmt.Errorf("service.authorize: %w", domain.ErrForbidden)
	}
	return workspaceID, nil
}

// authorizeOrgOnly -- Restore (S4-02): desain asli cuma bilang "dapat
// dipulihkan Group Admin", TIDAK menyebut AW/PM -- pemulihan sengaja lebih
// ketat dari hapusnya sendiri (siapa saja yang boleh hapus tidak otomatis
// boleh pulihkan, mencegah AW/PM menutupi kesalahannya sendiri tanpa jejak
// GA).
func (s *ProjectService) authorizeOrgOnly(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	workspaceID, err := s.repo.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return err
	}
	orgID, err := s.rbac.GetWorkspaceOrgID(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("service.authorizeOrgOnly: %w", err)
	}
	if err := s.orgs.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.authorizeOrgOnly: %w", domain.ErrForbidden)
	}
	return nil
}

// Create membuat project baru (S4-02). code dan pmUserID WAJIB (AW Add
// Project.dc.html) -- pmUserID divalidasi harus workspace member ber-role
// project_manager di workspace target (project_scoped_role TIDAK punya
// nilai project_manager, lihat komentar migrasi
// 20260909090000_projects_code_pm_softdelete).
func (s *ProjectService) Create(ctx context.Context, exec db.Executor, workspaceID, name, code, pmUserID, actorID, actorRole string) (*repository.Project, error) {
	name = strings.TrimSpace(name)
	code = strings.ToUpper(strings.TrimSpace(code))
	if workspaceID == "" || name == "" || pmUserID == "" {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrInvalidInput)
	}
	if !projectCodePattern.MatchString(code) {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrInvalidInput)
	}

	pmRole, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, pmUserID)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	if pmRole != "project_manager" {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrInvalidInput)
	}

	p, err := s.repo.Create(ctx, exec, workspaceID, name, code, pmUserID, actorID, actorRole)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	return p, nil
}

// List mengembalikan seluruh project workspace (S4-04 ProjectListPage) --
// TIDAK ada pengecekan otorisasi tambahan, scoping penuh lewat RLS
// projects_select + middleware.RequireRole di routing (semua workspace
// role boleh lihat).
func (s *ProjectService) List(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.Project, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.List: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.List(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.List: %w", err)
	}
	return list, nil
}

// Update mengubah nama dan/atau PM penanggung jawab (S4-02). pmUserID
// kosong berarti tidak diubah; kalau diisi, divalidasi sama seperti Create.
func (s *ProjectService) Update(ctx context.Context, exec db.Executor, projectID, name, pmUserID, actorID, actorRole string) error {
	name = strings.TrimSpace(name)
	if projectID == "" || name == "" {
		return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
	}
	workspaceID, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return err
	}
	if pmUserID != "" {
		pmRole, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, pmUserID)
		if err != nil {
			return fmt.Errorf("service.Update: %w", err)
		}
		if pmRole != "project_manager" {
			return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
		}
	}
	if err := s.repo.Update(ctx, exec, projectID, name, pmUserID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	return nil
}

// SetArchived mengarsipkan/batal-arsip project (S4-03).
func (s *ProjectService) SetArchived(ctx context.Context, exec db.Executor, projectID string, archive bool, actorID, actorRole string) error {
	if projectID == "" {
		return fmt.Errorf("service.SetArchived: %w", domain.ErrInvalidInput)
	}
	if _, err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.SetArchived(ctx, exec, projectID, archive, actorID, actorRole); err != nil {
		return fmt.Errorf("service.SetArchived: %w", err)
	}
	return nil
}

// Delete melakukan soft-delete (S4-02) -- lihat komentar
// ProjectRepository.SoftDelete kenapa ini bukan hard-delete.
func (s *ProjectService) Delete(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	if projectID == "" {
		return fmt.Errorf("service.Delete: %w", domain.ErrInvalidInput)
	}
	if _, err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.SoftDelete(ctx, exec, projectID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	return nil
}

// Restore membatalkan soft-delete -- Group Admin/Platform Admin saja.
func (s *ProjectService) Restore(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	if projectID == "" {
		return fmt.Errorf("service.Restore: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeOrgOnly(ctx, exec, projectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.Restore(ctx, exec, projectID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.Restore: %w", err)
	}
	return nil
}
