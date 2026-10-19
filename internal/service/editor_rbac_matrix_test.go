package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

// S6E-15 (IG-146): kolom Editor `rbac_matrix.xlsx` v4.1 -- tiap kasus diberi nomor kolom "No" pada matriks. Gerbang tingkat layanan
// (pure/fake); bukti terhadap RLS sungguhan ada di IG-146 (probe live). Rute workspace: cmd/api/routes_guard_test.go.

func TestEditorMatrix_SprintManagement(t *testing.T) { // No 7 "Buat / kelola sprint"
	cases := []struct {
		role      string
		actorRole string
		allowed   bool
	}{
		{"admin_workspace", "", true},
		{"project_manager", "", true},
		{"", "platform_admin", true},
		{"", "group_admin", true},
		{"editor", "", false}, // sebelum IG-146 lolos
		{"approver", "", false},
		{"viewer", "", false},
		{"division_viewer", "", false},
		{"", "", false}, // bukan anggota
	}
	for _, tc := range cases {
		name := tc.role + "/" + tc.actorRole
		t.Run(name, func(t *testing.T) {
			svc := newSprintServiceForTest(&fakeSprintRepo{}, tc.role)
			_, err := svc.authorize(context.Background(), nil, "proj1", "user1", tc.actorRole)
			if tc.allowed && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if !tc.allowed && !errors.Is(err, domain.ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
		})
	}
}

func TestEditorMatrix_Capabilities(t *testing.T) { // No 3a, 6, 7, 8, 18, 20, 23-24, 26
	c := CapabilitiesForRole("editor")
	if !(c.CreateTask && c.EditOwnTask && c.Comment && c.LogTime && c.Attach) {
		t.Errorf("Editor harus bisa buat task, ubah task sendiri, komentar, catat waktu, lampiran: %+v", c)
	}
	if c.ManageProject || c.ManageRules || c.ManageMembers {
		t.Errorf("Editor tidak boleh kelola project/rule/member: %+v", c)
	}
}

func TestEditorMatrix_TaskGates(t *testing.T) {
	ctx := context.Background()
	if canDeleteTask("editor") { // No 14
		t.Error("No 14: Editor tidak boleh hapus task")
	}
	for _, r := range []string{"admin_workspace", "project_manager", ""} {
		if !canDeleteTask(r) {
			t.Errorf("No 14: %q harus boleh hapus task", r)
		}
	}
	owner := &fakeAttachmentTaskResolver{}
	stranger := &fakeAttachmentTaskResolver{notOwner: true}
	if err := requireOwnTask(ctx, nil, owner, "editor", "t1", "u1"); err != nil { // No 9/10/12/20/26 task sendiri
		t.Errorf("Editor pada task sendiri: %v", err)
	}
	if err := requireOwnTask(ctx, nil, stranger, "editor", "t1", "u1"); !errors.Is(err, domain.ErrNotYourTask) {
		t.Errorf("Editor pada task orang lain: err = %v, want ErrNotYourTask", err)
	}
	if err := requireOwnTask(ctx, nil, stranger, "project_manager", "t1", "u1"); err != nil {
		t.Errorf("PM tidak dibatasi kepemilikan: %v", err)
	}
	if err := checkChecklistWrite(ctx, nil, stranger, "editor", "t1", "u1"); !errors.Is(err, domain.ErrNotYourTask) { // No 8 sub-task
		t.Errorf("sub-task Editor pada task orang lain: err = %v", err)
	}
	if err := checkChecklistWrite(ctx, nil, owner, "editor", "t1", "u1"); err != nil {
		t.Errorf("sub-task Editor pada task sendiri: %v", err)
	}
	if err := checkChecklistWrite(ctx, nil, owner, "", "t1", "u1"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("non-anggota harus ditolak: %v", err)
	}
}

func TestEditorMatrix_Attachments(t *testing.T) { // No 20, 21, 22
	if !canEditAttachment("editor") {
		t.Error("No 20: Editor boleh upload (pada task miliknya)")
	}
	if !canManageAttachment("editor", true) {
		t.Error("No 21: Editor boleh rename/hapus lampiran SENDIRI")
	}
	if canManageAttachment("editor", false) {
		t.Error("No 21: Editor tidak boleh rename/hapus lampiran orang lain")
	}
	if canEditAttachment("viewer") {
		t.Error("No 20: Viewer tidak boleh upload")
	}
}
