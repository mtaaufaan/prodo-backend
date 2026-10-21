package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// attSvcOwned -- layanan lampiran dengan kepemilikan task diatur: owned=false meniru task orang lain.
func attSvcOwned(repo *fakeAttachmentRepo, role string, owned bool) *TaskAttachmentService {
	return NewTaskAttachmentService(
		repo,
		&fakeAttachmentTaskResolver{projectID: "proj-1", notOwner: !owned},
		&fakeAttachmentProjectResolver{workspaceID: "ws-1"},
		&fakeAttachmentWorkspaceInfo{ws: &repository.Workspace{ID: "ws-1", Name: "Marketing"}},
		&fakeAttachmentOrgQuota{info: &repository.AttachmentQuotaInfo{OrgID: "org-1", OrgName: "Org 1", GroupID: "group-1", QuotaBytes: 100 * 1024 * 1024, RetentionDays: 90}},
		&fakeAttachmentRoleChecker{role: role},
		&fakeAttachmentProjectRoleChecker{},
		&fakeAttachmentStorage{},
		&fakeAttachmentRefresher{},
	)
}

// S7A-10 (IG-156; matriks RBAC No 20-21): Approver melampirkan pada task project yang diikuti -- bukan hanya miliknya --
// sedangkan Editor hanya task miliknya. Audit memakai role efektif.
func TestUpload_RoleByTaskOwnership(t *testing.T) {
	cases := []struct {
		role      string
		owned     bool
		wantErr   error
		wantAudit string
	}{
		{"approver", false, nil, "approver"}, // task orang lain, boleh
		{"approver", true, nil, "approver"},
		{"editor", true, nil, "editor"},
		{"editor", false, domain.ErrNotYourTask, ""},
		{"viewer", true, domain.ErrForbidden, ""},
		{"division_viewer", true, domain.ErrForbidden, ""},
		{"project_manager", false, nil, "project_manager"},
		{"admin_workspace", false, nil, "admin_workspace"},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			repo := &fakeAttachmentRepo{}
			_, err := attSvcOwned(repo, tc.role, tc.owned).Upload(context.Background(), nil, "task-1", "berkas.txt", []byte("data"), "user-1", "member")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && repo.createRole != tc.wantAudit {
				t.Fatalf("role audit = %q, want %q (role efektif, bukan role rute)", repo.createRole, tc.wantAudit)
			}
			if tc.wantErr != nil && repo.createCalls != 0 {
				t.Fatalf("createCalls = %d, want 0", repo.createCalls)
			}
		})
	}
}

// Approver mengelola lampiran SENDIRI saja (No 21): rename/hapus/pulihkan milik orang lain ditolak, milik sendiri boleh,
// dan setiap aksi diaudit dengan role efektif.
func TestApproverAttachment_OwnOnlyWithEffectiveRoleAudit(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		uploader string
		wantErr  error
	}{{"user-1", nil}, {"orang-lain", domain.ErrForbidden}} {
		repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{"att-1": {ID: "att-1", TaskID: "task-1", UploaderID: tc.uploader}}}
		svc := attSvcOwned(repo, "approver", false)
		if err := svc.Rename(ctx, nil, "att-1", "baru.pdf", "user-1", ""); !errors.Is(err, tc.wantErr) {
			t.Errorf("rename uploader=%s: err = %v, want %v", tc.uploader, err, tc.wantErr)
		}
		if err := svc.Delete(ctx, nil, "att-1", "user-1", ""); !errors.Is(err, tc.wantErr) {
			t.Errorf("delete uploader=%s: err = %v, want %v", tc.uploader, err, tc.wantErr)
		}
		if tc.wantErr == nil && (len(repo.softDeleteRoles) != 1 || repo.softDeleteRoles[0] != "approver") {
			t.Errorf("audit hapus = %v, want [approver]", repo.softDeleteRoles)
		}
	}
}
