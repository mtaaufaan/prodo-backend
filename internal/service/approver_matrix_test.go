package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// S7A-13 (IG-159): kolom Approver `rbac_matrix.xlsx` v4.2 -- tiap kasus diberi nomor kolom "No" pada matriks. Gerbang tingkat layanan
// (fake); bukti terhadap RLS sungguhan ada di IG-159 (probe live). Baris yang sudah dikunci tes Sprint 7 lain:
// No 8/9/10a/13 (`approver_rbac_matrix_test.go`), No 10/10b/12 (idem), No 20-21 (`approver_task_detail_test.go`),
// No 26 dan sub-task (`task_editor_permissions_test.go`), rute (`cmd/api/approver_routes_test.go`).

func TestApproverMatrix_Capabilities(t *testing.T) { // No 14a, 18, 19, 20, 10c; bukan 8, 9, 26
	c := CapabilitiesForRole("approver")
	if !(c.Comment && c.Attach && c.ApproveTask) {
		t.Errorf("Approver harus bisa komentar, melampirkan, setujui/tolak: %+v", c)
	}
	if c.CreateTask || c.EditOwnTask || c.LogTime || c.ManageProject || c.ManageRules || c.ManageMembers {
		t.Errorf("Approver tidak boleh buat/ubah task, catat waktu, atau kelola project/rule/member: %+v", c)
	}
}

func TestApproverMatrix_DeniedRows(t *testing.T) {
	ctx := context.Background()
	const appr = "appr-1"
	cases := []struct {
		name string // nomor "No" + aksi
		run  func() error
	}{
		{"No 6 ubah project", func() error {
			svc := newTestProjectService(&fakeProjectRepo{workspaceID: map[string]string{"p1": "ws-1"}}, &fakeOrgAuthorizer{err: domain.ErrForbidden}, &fakeProjectRoleChecker{role: "approver"}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})
			return svc.Update(ctx, nil, "p1", "Nama Baru", "in_progress", appr, "", nil)
		}},
		{"No 7 kelola sprint", func() error {
			_, err := newSprintServiceForTest(&fakeSprintRepo{}, "approver").authorize(ctx, nil, "p1", appr, "")
			return err
		}},
		{"No 12a konfigurasi PIC Group", func() error {
			svc, _ := picSvcWithRole("approver", false)
			_, err := svc.ReplaceGroup(ctx, nil, "p1", "st1", []string{"u2"}, appr, "")
			return err
		}},
		{"No 24 lihat rule project", func() error {
			_, err := newTestRuleService(&fakeRuleRepo{}, "approver", nil, "ws-1").ListForProject(ctx, nil, "p1", appr, "")
			return err
		}},
		{"No 24a log eksekusi rule project", func() error {
			_, err := newTestRuleService(&fakeRuleRepo{}, "approver", nil, "ws-1").ListExecutionsForProject(ctx, nil, "p1", "", appr, "")
			return err
		}},
		{"No 27a Performance Dashboard project", func() error {
			_, err := newTestPerformanceService(nil, "pm-1", "approver").DashboardForProject(ctx, nil, "proj-1", 30, appr, "")
			return err
		}},
		{"Timesheet & Analitik project (ringkasan)", func() error {
			_, err := newTestTimesheetService(&fakeTimesheetRepo{}, "pm-1", "approver").Summary(ctx, nil, "p1", appr, "")
			return err
		}},
		{"No 28 ekspor timesheet project", func() error {
			_, _, err := newTestTimesheetService(&fakeTimesheetRepo{}, "pm-1", "approver").Export(ctx, nil, "p1", appr, "")
			return err
		}},
		{"No 30 audit trail project", func() error {
			_, _, err := newTestProjectAuditService(nil, "pm-1", "approver").List(ctx, nil, "p1", appr, "", "", 30, 10, 0, "")
			return err
		}},
		{"Dokumen & Lampiran project", func() error {
			_, err := newTestAttachmentService(&fakeAttachmentRepo{}, "approver", nil, nil, nil).ListForProject(ctx, nil, "p1", &repository.AttachmentFilter{}, appr, "")
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, domain.ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
		})
	}
}

// No 27 "Approve timesheet": hanya AW dan PM yang menyetujui/menolak entri waktu; Approver tidak.
func TestApproverMatrix_TimesheetApproval(t *testing.T) {
	ctx := context.Background()
	for role, want := range map[string]bool{"admin_workspace": true, "project_manager": true, "approver": false, "editor": false, "viewer": false} {
		svc := newTestTimeEntryService(&fakeTimeEntryRepo{}, role)
		got, err := svc.isApprover(ctx, nil, "t1", "u1", "member")
		if err != nil || got != want {
			t.Errorf("%s: isApprover = %v (err %v), want %v", role, got, err, want)
		}
	}
}

// ListForActor (IG-159): Editor dan Approver hanya melihat project yang diikuti; role lain dan Platform/Group Admin penuh.
func TestProjectService_ListForActor_RestrictsEditorAndApprover(t *testing.T) {
	ctx := context.Background()
	all := []repository.Project{{ID: "p1"}, {ID: "p2"}, {ID: "p3"}}
	cases := []struct {
		role, actorRole string
		want            int
	}{
		{"approver", "", 1},
		{"editor", "", 1},
		{"project_manager", "", 3},
		{"admin_workspace", "", 3},
		{"viewer", "", 3},
		{"division_viewer", "", 3},
		{"approver", "platform_admin", 3},
		{"editor", "group_admin", 3},
	}
	for _, tc := range cases {
		t.Run(tc.role+"/"+tc.actorRole, func(t *testing.T) {
			repo := &fakeProjectRepo{listResult: all, followedIDs: []string{"p2"}}
			svc := newTestProjectService(repo, &fakeOrgAuthorizer{}, &fakeProjectRoleChecker{role: tc.role}, &stubExistingUserFinder{}, &fakeProjectPMInviter{})
			list, err := svc.ListForActor(ctx, nil, "ws-1", "u1", tc.actorRole)
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != tc.want {
				t.Fatalf("len = %d, want %d", len(list), tc.want)
			}
			if tc.want == 1 && list[0].ID != "p2" {
				t.Fatalf("project = %s, want p2 (yang diikuti)", list[0].ID)
			}
		})
	}
}
