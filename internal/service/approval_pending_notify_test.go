package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// S7A-12 (IG-158): task MASUK UNDER REVIEW -> notifikasi `approval_pending` ke Approver (repositori menentukan
// penerima, tidak ke pelaku, sekali per fase). Di sini diuji KAPAN layanan memicunya: hanya saat memasuki review.
func approvalPendingSvc(from, to string, undefinedTarget bool) (*TaskService, *fakeTaskSessions) {
	statuses := map[string]*repository.CustomStatus{
		"from": {ID: "from", Name: from, RequirePic: from != "DONE"},
		"to":   {ID: "to", Name: to, RequirePic: to != "DONE", IsUndefined: undefinedTarget},
	}
	repo := &fakeTaskRepo{byID: map[string]*repository.Task{
		"t1": {ID: "t1", ProjectID: "p1", StatusID: "from", StatusName: from, Assignees: []repository.TaskAssignee{{UserID: "pic1"}}},
	}}
	sessions := &fakeTaskSessions{}
	svc := NewTaskService(repo, &fakeTaskPics{}, &fakeTaskDeps{}, sessions,
		&fakeTaskProjects{workspaceID: "ws1"}, &fakeTaskStatuses{byID: statuses},
		&fakeSprintRBAC{role: "project_manager"}, &fakeSprintProjectRoles{found: false}, &fakeTaskRules{}, &fakeTaskSprints{})
	return svc, sessions
}

func TestSetStatus_NotifiesApproversOnlyWhenEnteringUnderReview(t *testing.T) {
	cases := []struct {
		name, from, to string
		want           int
	}{
		{"masuk review dari IN PROGRESS", "IN PROGRESS", "UNDER REVIEW", 1},
		{"dibuka kembali dari DONE ke review = fase baru", "DONE", "UNDER REVIEW", 1},
		{"tetap di review (status sama) tidak memicu", "UNDER REVIEW", "UNDER REVIEW", 0},
		{"keluar dari review (ditolak/dikembalikan) tidak memicu", "UNDER REVIEW", "IN PROGRESS", 0},
		{"menuju DONE tidak memicu", "IN PROGRESS", "DONE", 0},
		{"status lain tidak memicu", "IN PROGRESS", "BLOCKED", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, sessions := approvalPendingSvc(c.from, c.to, false)
			if err := svc.SetStatus(context.Background(), nil, "t1", "to", []string{"pic1"}, "user1", "member"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(sessions.approvalPending) != c.want {
				t.Fatalf("pemicu = %v, want %d", sessions.approvalPending, c.want)
			}
		})
	}
}

// Jalur rule ("Ubah status") dan bulk memakai setStatusWith yang sama, jadi ikut memicu.
func TestSetStatusForRule_EnteringUnderReviewNotifies(t *testing.T) {
	svc, sessions := approvalPendingSvc("IN PROGRESS", "UNDER REVIEW", false)
	if err := svc.SetStatusForRule(context.Background(), nil, "t1", "to", "user1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions.approvalPending) != 1 {
		t.Fatalf("pemicu = %v, want 1", sessions.approvalPending)
	}
}

// Perpindahan yang ditolak (status UNDEFINED) tidak boleh mengirim notifikasi.
func TestSetStatus_RejectedMoveDoesNotNotify(t *testing.T) {
	svc, sessions := approvalPendingSvc("IN PROGRESS", "UNDER REVIEW", true)
	err := svc.SetStatus(context.Background(), nil, "t1", "to", []string{"pic1"}, "user1", "member")
	if !errors.Is(err, domain.ErrTaskStatusUndefined) {
		t.Fatalf("err = %v, want ErrTaskStatusUndefined", err)
	}
	if len(sessions.approvalPending) != 0 {
		t.Fatalf("pemicu = %v, want 0", sessions.approvalPending)
	}
}
