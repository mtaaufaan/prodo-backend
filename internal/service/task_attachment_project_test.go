package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// newProjectDocsService -- layanan dengan peran workspace `wsRole`, flag PM
// project, dan peta task->project untuk menguji batas project.
func newProjectDocsService(repo *fakeAttachmentRepo, wsRole string, isPM bool, byTask map[string]string) *TaskAttachmentService {
	return NewTaskAttachmentService(
		repo,
		&fakeAttachmentTaskResolver{projectID: "proj-1", byTask: byTask},
		&fakeAttachmentProjectResolver{workspaceID: "ws-1", pm: isPM},
		&fakeAttachmentWorkspaceInfo{ws: &repository.Workspace{ID: "ws-1", Name: "Marketing"}},
		&fakeAttachmentOrgQuota{info: &repository.AttachmentQuotaInfo{OrgID: "org-1", OrgName: "Org 1", GroupID: "group-1", QuotaBytes: 100 << 20, RetentionDays: 30}},
		&fakeAttachmentRoleChecker{role: wsRole},
		&fakeAttachmentProjectRoleChecker{},
		&fakeAttachmentStorage{},
		&fakeAttachmentRefresher{},
	)
}

func TestListForProject_AuthorizesPMAndAdminOnly(t *testing.T) {
	cases := []struct {
		name      string
		wsRole    string
		isPM      bool
		actorRole string
		wantErr   bool
	}{
		{"PM project", "editor", true, "", false},
		{"admin workspace", "admin_workspace", false, "", false},
		{"group admin bypass", "", false, "group_admin", false},
		{"editor ditolak", "editor", false, "", true},
		{"viewer ditolak", "viewer", false, "", true},
		{"PM workspace lain (bukan PM project ini) ditolak", "project_manager", false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newProjectDocsService(&fakeAttachmentRepo{}, tc.wsRole, tc.isPM, nil)
			_, err := svc.ListForProject(context.Background(), nil, "proj-1", &repository.AttachmentFilter{}, "u-1", tc.actorRole)
			if tc.wantErr != errors.Is(err, domain.ErrForbidden) {
				t.Errorf("err = %v, wantForbidden = %v", err, tc.wantErr)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected err: %v", err)
			}
		})
	}
}

// Filter project_id dari klien tidak boleh membocorkan project lain.
func TestListForProject_ForcesProjectFromPath(t *testing.T) {
	repo := &fakeAttachmentRepo{}
	svc := newProjectDocsService(repo, "editor", true, nil)
	if _, err := svc.ListForProject(context.Background(), nil, "proj-1", &repository.AttachmentFilter{ProjectID: "proj-lain", Status: "orphan"}, "u-1", ""); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if repo.lastFilter.ProjectID != "proj-1" || repo.lastFilter.Status != "orphan" {
		t.Errorf("filter = %+v, want ProjectID proj-1 dan Status orphan tetap", repo.lastFilter)
	}
}

func TestBulkDeleteForProject_RetentionOnlyAndScopedToProject(t *testing.T) {
	deleted := time.Now()
	repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{
		"a-ok":      {ID: "a-ok", TaskID: "t-1"},
		"a-other":   {ID: "a-other", TaskID: "t-other"},
		"a-deleted": {ID: "a-deleted", TaskID: "t-1", DeletedAt: &deleted},
	}}
	svc := newProjectDocsService(repo, "editor", true, map[string]string{"t-other": "proj-2"})

	n, err := svc.BulkDeleteForProject(context.Background(), nil, "proj-1", []string{"a-ok", "a-other", "a-deleted", "a-hilang"}, "u-1", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if n != 1 || len(repo.softDeleteCalls) != 1 || repo.softDeleteCalls[0].id != "a-ok" {
		t.Errorf("succeeded = %d, calls = %+v, want hanya a-ok", n, repo.softDeleteCalls)
	}
	if len(repo.permanentDeleteCalls) != 0 {
		t.Errorf("hapus permanen dipanggil: %v", repo.permanentDeleteCalls)
	}
	if repo.softDeleteCalls[0].purgeAt.Before(time.Now().Add(29 * 24 * time.Hour)) {
		t.Errorf("purgeAt = %v, want ~30 hari ke depan (retensi organisasi)", repo.softDeleteCalls[0].purgeAt)
	}
	// Rute project tanpa RequireRole: audit harus memakai role EFEKTIF, bukan string kosong (IG-92).
	if repo.softDeleteRoles[0] != "project_manager" {
		t.Errorf("role audit = %q, want project_manager", repo.softDeleteRoles[0])
	}
}

func TestBulkDeleteForProject_ForbiddenForNonPM(t *testing.T) {
	repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{"a-1": {ID: "a-1", TaskID: "t-1"}}}
	svc := newProjectDocsService(repo, "editor", false, nil)
	if _, err := svc.BulkDeleteForProject(context.Background(), nil, "proj-1", []string{"a-1"}, "u-1", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
	if len(repo.softDeleteCalls) != 0 {
		t.Errorf("tidak boleh ada penghapusan: %+v", repo.softDeleteCalls)
	}
}

func TestProjectQuotaOverview_ReturnsOnlyThisProject(t *testing.T) {
	svc := newProjectDocsService(&fakeAttachmentRepo{}, "editor", true, nil)
	ov, err := svc.ProjectQuotaOverview(context.Background(), nil, "proj-1", "u-1", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(ov.PerProject) != 1 || ov.PerProject[0].ProjectID != "proj-1" || ov.QuotaBytes != 100<<20 || ov.RetentionDays != 30 {
		t.Errorf("overview = %+v", ov)
	}
}
