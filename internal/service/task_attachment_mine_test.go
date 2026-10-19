package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// S6E-12 (IG-143): Lampiran Saya Editor -- daftar milik sendiri, gerbang rename/hapus, audit unduhan.

func TestListMine_ForcesUploaderToActor(t *testing.T) {
	repo := &fakeAttachmentRepo{listResult: []repository.TaskAttachment{{ID: "a1"}}}
	svc := newTestAttachmentService(repo, "editor", nil, nil, nil)

	// uploader_id dari klien (mencoba melihat file orang lain) harus ditimpa dengan aktor.
	list, err := svc.ListMine(context.Background(), nil, "ws-1", &repository.AttachmentFilter{
		ProjectID: "p1", Status: "all", Ext: "pdf", Sort: "newest", UploaderID: "someone-else",
	}, "user-1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	got := repo.lastFilter
	if got.UploaderID != "user-1" {
		t.Fatalf("UploaderID = %q, want user-1 (aktor, bukan dari klien)", got.UploaderID)
	}
	if got.ProjectID != "p1" || got.Status != "all" || got.Ext != "pdf" || got.Sort != "newest" {
		t.Fatalf("filter lain tidak diteruskan: %+v", got)
	}
}

func TestListMine_RejectsMissingWorkspaceOrActor(t *testing.T) {
	for _, tc := range []struct{ ws, actor string }{{"", "u1"}, {"ws-1", ""}} {
		repo := &fakeAttachmentRepo{}
		svc := newTestAttachmentService(repo, "editor", nil, nil, nil)
		_, err := svc.ListMine(context.Background(), nil, tc.ws, &repository.AttachmentFilter{}, tc.actor)
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("ws=%q actor=%q: err = %v, want ErrInvalidInput", tc.ws, tc.actor, err)
		}
		if len(repo.listFilters) != 0 {
			t.Errorf("query dijalankan padahal input tidak valid")
		}
	}
}

// Editor hanya boleh rename/hapus/pulihkan lampiran yang ia unggah; PM/AW boleh semua.
func TestEditorAttachmentMutations_OwnOnly(t *testing.T) {
	type op func(svc *TaskAttachmentService) error
	ops := map[string]op{
		"rename": func(s *TaskAttachmentService) error {
			return s.Rename(context.Background(), nil, "att-1", "baru.pdf", "user-1", "member")
		},
		"delete": func(s *TaskAttachmentService) error {
			return s.Delete(context.Background(), nil, "att-1", "user-1", "member")
		},
		"restore": func(s *TaskAttachmentService) error {
			return s.Restore(context.Background(), nil, "att-1", "user-1", "member")
		},
	}
	cases := []struct {
		role     string
		uploader string
		wantErr  error
	}{
		{"editor", "user-1", nil},
		{"editor", "orang-lain", domain.ErrForbidden},
		{"approver", "orang-lain", domain.ErrForbidden},
		{"viewer", "orang-lain", domain.ErrForbidden},
		{"project_manager", "orang-lain", nil},
		{"admin_workspace", "orang-lain", nil},
	}
	for name, run := range ops {
		for _, tc := range cases {
			t.Run(name+"/"+tc.role+"/uploader="+tc.uploader, func(t *testing.T) {
				repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{
					"att-1": {ID: "att-1", TaskID: "task-1", UploaderID: tc.uploader},
				}}
				svc := newTestAttachmentService(repo, tc.role, nil, nil, nil)
				err := run(svc)
				if tc.wantErr == nil && err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			})
		}
	}
}

func TestDownload_IsAuditedWithEffectiveRole(t *testing.T) {
	repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{
		"att-1": {ID: "att-1", TaskID: "task-1", UploaderID: "orang-lain", StorageKey: "k", DisplayName: "a.pdf"},
	}}
	svc := newTestAttachmentService(repo, "editor", nil, nil, nil)

	// Siapa pun yang dapat melihat task boleh mengunduh (US-064b), bukan hanya pengunggah.
	att, data, err := svc.Download(context.Background(), nil, "att-1", "user-1", "")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if att.ID != "att-1" || string(data) != "data" {
		t.Fatalf("hasil unduh tidak sesuai: %v %q", att.ID, data)
	}
	if len(repo.downloadLogs) != 1 {
		t.Fatalf("audit unduhan = %d, want 1", len(repo.downloadLogs))
	}
	if l := repo.downloadLogs[0]; l.id != "att-1" || l.role != "editor" || l.workspaceID != "ws-1" {
		t.Fatalf("audit = %+v, want id att-1, role efektif editor (bukan role rute yang kosong), workspace ws-1", l)
	}
}

func TestDownload_NotFoundWritesNoAudit(t *testing.T) {
	repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{}}
	svc := newTestAttachmentService(repo, "editor", nil, nil, nil)
	if _, _, err := svc.Download(context.Background(), nil, "tidak-ada", "user-1", ""); !errors.Is(err, domain.ErrAttachmentNotFound) {
		t.Fatalf("err = %v, want ErrAttachmentNotFound", err)
	}
	if len(repo.downloadLogs) != 0 {
		t.Fatalf("audit ditulis padahal lampiran tidak ada")
	}
}
