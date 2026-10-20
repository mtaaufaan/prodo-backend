package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// S7A-04 (IG-150): keputusan approval -- aturan murni, alur setuju/tolak, gerbang role, dan konflik.

func TestValidateApprovalInput(t *testing.T) {
	long := make([]rune, approvalNoteMax+1)
	for i := range long {
		long[i] = 'x'
	}
	cases := []struct {
		name, decision, note string
		wantApprove          bool
		wantNote             string
		wantErr              error
	}{
		{"setuju tanpa catatan", "approve", "", true, "", nil},
		{"setuju dengan catatan ter-trim", "approve", "  oke  ", true, "oke", nil},
		{"tolak dengan alasan", "reject", "perbaiki bagian itu", false, "perbaiki bagian itu", nil},
		{"tolak alasan 5 karakter lolos", "reject", "abcde", false, "abcde", nil},
		{"tolak alasan 4 karakter", "reject", "abcd", false, "", domain.ErrApprovalNoteRequired},
		{"tolak tanpa alasan", "reject", "   ", false, "", domain.ErrApprovalNoteRequired},
		{"keputusan tak dikenal", "maybe", "x", false, "", domain.ErrInvalidInput},
		{"keputusan kosong", "", "", false, "", domain.ErrInvalidInput},
		{"catatan > 1000 karakter", "approve", string(long), false, "", domain.ErrInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			approve, note, err := validateApprovalInput(tc.decision, tc.note)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && (approve != tc.wantApprove || note != tc.wantNote) {
				t.Fatalf("approve=%v note=%q, want %v %q", approve, note, tc.wantApprove, tc.wantNote)
			}
		})
	}
}

func TestPickReturnStatus(t *testing.T) {
	s := func(names ...string) []repository.TaskStatusSession {
		out := make([]repository.TaskStatusSession, len(names))
		for i, n := range names {
			out[i] = repository.TaskStatusSession{StatusID: "id-" + n, StatusName: n}
		}
		return out
	}
	cases := []struct {
		name     string
		sessions []repository.TaskStatusSession
		want     string
		ok       bool
	}{
		{"kerja terakhir sebelum review", s("BACKLOG", "IN PROGRESS", "UNDER REVIEW"), "id-IN PROGRESS", true},
		{"status kerja kustom", s("BACKLOG", "QA TESTING", "UNDER REVIEW"), "id-QA TESTING", true},
		{"BLOCKED dilewati, kembali ke kerja sebelumnya", s("BACKLOG", "IN PROGRESS", "BLOCKED", "UNDER REVIEW"), "id-IN PROGRESS", true},
		{"review berulang (ditolak lalu direview lagi)", s("IN PROGRESS", "UNDER REVIEW", "IN PROGRESS", "UNDER REVIEW"), "id-IN PROGRESS", true},
		{"hanya BACKLOG -> tidak ada", s("BACKLOG", "UNDER REVIEW"), "", false},
		{"tanpa riwayat", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pickReturnStatus(tc.sessions)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("pickReturnStatus = %q,%v want %q,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestCanDecideApproval(t *testing.T) {
	cases := []struct {
		role, actorRole string
		want            bool
	}{
		{"approver", "", true}, {"project_manager", "", true}, {"admin_workspace", "", true},
		{"", "platform_admin", true}, {"", "group_admin", true},
		{"editor", "", false}, {"viewer", "", false}, {"division_viewer", "", false}, {"", "", false},
	}
	for _, tc := range cases {
		if got := canDecideApproval(tc.role, tc.actorRole); got != tc.want {
			t.Errorf("canDecideApproval(%q,%q) = %v, want %v", tc.role, tc.actorRole, got, tc.want)
		}
	}
}

// --- fakes ---

type approvalCall struct {
	statusID, action, from, to, note string
	isDone                           bool
}

type fakeApprovalStore struct {
	lockStatus   string
	lastPics     []string
	recipients   []string
	follows      bool
	statusByName map[string]*repository.CustomStatus
	setCalls     []approvalCall
	recorded     []string // "decision|note|from|to"
	notified     []string // "approved|recipients..."
	listView     string
}

func (f *fakeApprovalStore) LockStatus(_ context.Context, _ db.Executor, _ string) (statusName, projectID string, err error) {
	return f.lockStatus, "p1", nil
}
func (f *fakeApprovalStore) StatusByName(_ context.Context, _ db.Executor, _, name string) (*repository.CustomStatus, error) {
	if s, ok := f.statusByName[name]; ok {
		return s, nil
	}
	return nil, domain.ErrCustomStatusNotFound
}
func (f *fakeApprovalStore) LastPhasePics(_ context.Context, _ db.Executor, _, _ string) ([]string, error) {
	return f.lastPics, nil
}
func (f *fakeApprovalStore) Recipients(_ context.Context, _ db.Executor, _, _ string) ([]string, error) {
	return f.recipients, nil
}
func (f *fakeApprovalStore) SetStatusByApproval(_ context.Context, _ db.Executor, _, statusID string, isDone bool, _, _, _, action, from, to, note string) error {
	f.setCalls = append(f.setCalls, approvalCall{statusID: statusID, action: action, from: from, to: to, note: note, isDone: isDone})
	return nil
}
func (f *fakeApprovalStore) Record(_ context.Context, _ db.Executor, _, _, decision, note, from, to string) (string, error) {
	f.recorded = append(f.recorded, decision+"|"+note+"|"+from+"|"+to)
	return "ap1", nil
}
func (f *fakeApprovalStore) NotifyDecision(_ context.Context, _ db.Executor, _, _ string, recipientIDs []string, approved bool, note string) error {
	tag := "rejected"
	if approved {
		tag = "approved"
	}
	f.notified = append(f.notified, tag+"|"+note)
	_ = recipientIDs
	return nil
}
func (f *fakeApprovalStore) Follows(_ context.Context, _ db.Executor, _, _ string) (bool, error) {
	return f.follows, nil
}
func (f *fakeApprovalStore) List(_ context.Context, _ db.Executor, _, _, view string, _, _ int) ([]repository.ApprovalRow, int, error) {
	f.listView = view
	return nil, 0, nil
}
func (f *fakeApprovalStore) Stats(_ context.Context, _ db.Executor, _, _ string) (repository.ApprovalStats, error) {
	return repository.ApprovalStats{}, nil
}

type historySessions struct {
	*fakeTaskSessions
	history []repository.TaskStatusSession
}

func (h *historySessions) ListForTask(_ context.Context, _ db.Executor, _ string) ([]repository.TaskStatusSession, error) {
	return h.history, nil
}

type blockedDeps struct{ *fakeTaskDeps }

func (blockedDeps) ListIncompletePredecessors(_ context.Context, _ db.Executor, _ string) ([]repository.TaskDependency, error) {
	code := "QST-1"
	return []repository.TaskDependency{{PredecessorCode: &code, PredecessorTitle: "Task prasyarat"}}, nil
}

type countingRules struct{ events []string }

func (c *countingRules) Evaluate(_ context.Context, _ db.Executor, _, _, event string, _ *repository.Task, _, _ string) {
	c.events = append(c.events, event)
}

type approvalFixture struct {
	svc    *TaskService
	repo   *fakeTaskRepo
	store  *fakeApprovalStore
	pics   *fakeTaskPics
	rules  *countingRules
	status map[string]*repository.CustomStatus
}

func newApprovalFixture(role string, sessions []repository.TaskStatusSession, deps taskDependencyChecker) *approvalFixture {
	code := "QST-101"
	repo := &fakeTaskRepo{byID: map[string]*repository.Task{
		"t1": {ID: "t1", ProjectID: "p1", StatusID: "st-review", StatusName: "UNDER REVIEW", TaskCode: &code,
			Assignees: []repository.TaskAssignee{{UserID: "u-assignee"}}},
	}}
	status := map[string]*repository.CustomStatus{
		"st-backlog":  {ID: "st-backlog", Name: "BACKLOG", Position: 0},
		"st-progress": {ID: "st-progress", Name: "IN PROGRESS", Position: 1, RequirePic: true},
		"st-qa":       {ID: "st-qa", Name: "QA TESTING", Position: 2, RequirePic: true},
		"st-review":   {ID: "st-review", Name: "UNDER REVIEW", Position: 3, RequirePic: true},
		"st-done":     {ID: "st-done", Name: "DONE", Position: 4},
	}
	byName := map[string]*repository.CustomStatus{}
	for _, s := range status {
		byName[s.Name] = s
	}
	store := &fakeApprovalStore{lockStatus: "UNDER REVIEW", follows: true, statusByName: byName, recipients: []string{"u-assignee", "u-pic"}}
	pics := &fakeTaskPics{}
	rules := &countingRules{}
	if deps == nil {
		deps = &fakeTaskDeps{}
	}
	svc := NewTaskService(repo, pics, deps, &historySessions{fakeTaskSessions: &fakeTaskSessions{}, history: sessions},
		&fakeTaskProjects{workspaceID: "ws1"}, &fakeTaskStatuses{byID: status},
		&fakeSprintRBAC{role: role}, &fakeSprintProjectRoles{found: false}, rules, &fakeTaskSprints{}).WithApprovals(store)
	return &approvalFixture{svc: svc, repo: repo, store: store, pics: pics, rules: rules, status: status}
}

func sess(names ...string) []repository.TaskStatusSession {
	ids := map[string]string{"BACKLOG": "st-backlog", "IN PROGRESS": "st-progress", "QA TESTING": "st-qa", "UNDER REVIEW": "st-review", "DONE": "st-done"}
	out := make([]repository.TaskStatusSession, len(names))
	for i, n := range names {
		out[i] = repository.TaskStatusSession{StatusID: ids[n], StatusName: n}
	}
	return out
}

func TestDecideApproval_ApproveMovesToDone(t *testing.T) {
	f := newApprovalFixture("approver", sess("BACKLOG", "IN PROGRESS", "UNDER REVIEW"), nil)
	res, err := f.svc.DecideApproval(context.Background(), nil, "t1", "approve", "  Sesuai acceptance criteria. ", "approver1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusName != "DONE" || res.Decision != "approved" || res.TaskCode != "QST-101" {
		t.Fatalf("hasil = %+v", res)
	}
	if len(f.store.setCalls) != 1 || f.store.setCalls[0] != (approvalCall{statusID: "st-done", action: "task.approved", from: "UNDER REVIEW", to: "DONE", note: "Sesuai acceptance criteria.", isDone: true}) {
		t.Fatalf("setCalls = %+v", f.store.setCalls)
	}
	if len(f.repo.setStatusCall) != 0 {
		t.Fatal("audit status_changed generik tidak boleh dipakai pada keputusan approval")
	}
	if got := f.store.recorded; len(got) != 1 || got[0] != "approved|Sesuai acceptance criteria.|UNDER REVIEW|DONE" {
		t.Fatalf("recorded = %v", got)
	}
	if got := f.store.notified; len(got) != 1 || got[0] != "approved|Sesuai acceptance criteria." {
		t.Fatalf("notified = %v", got)
	}
	// rule dievaluasi dua kali: scope workspace dan scope project (aditif).
	if len(f.rules.events) != 2 || f.rules.events[0] != "status_changed" || f.rules.events[1] != "status_changed" {
		t.Fatalf("rule status_changed harus tetap terpicu: %v", f.rules.events)
	}
	if len(f.pics.phases) != 0 {
		t.Fatalf("DONE tanpa PIC fase baru: %v", f.pics.phases)
	}
}

func TestDecideApproval_RejectReturnsToPreviousWorkStatusWithItsPics(t *testing.T) {
	f := newApprovalFixture("approver", sess("BACKLOG", "IN PROGRESS", "QA TESTING", "UNDER REVIEW"), nil)
	f.store.lastPics = []string{"u-qa1", "u-qa2"}
	res, err := f.svc.DecideApproval(context.Background(), nil, "t1", "reject", "Uji ulang bagian pembayaran.", "approver1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusName != "QA TESTING" || res.Decision != "rejected" {
		t.Fatalf("hasil = %+v", res)
	}
	if c := f.store.setCalls[0]; c.statusID != "st-qa" || c.action != "task.rejected" || c.isDone || c.note != "Uji ulang bagian pembayaran." {
		t.Fatalf("setCall = %+v", c)
	}
	if len(f.pics.phases) != 2 || f.pics.phases[0] != "u-qa1" || f.pics.phases[1] != "u-qa2" {
		t.Fatalf("PIC fase kerja harus diaktifkan kembali: %v", f.pics.phases)
	}
	if f.pics.deactivated != 1 {
		t.Fatalf("PIC fase review harus dinonaktifkan: %d", f.pics.deactivated)
	}
	if got := f.store.recorded[0]; got != "rejected|Uji ulang bagian pembayaran.|UNDER REVIEW|QA TESTING" {
		t.Fatalf("recorded = %s", got)
	}
}

func TestDecideApproval_RejectFallbacks(t *testing.T) {
	t.Run("tanpa riwayat kerja -> IN PROGRESS dengan assignee sebagai PIC", func(t *testing.T) {
		f := newApprovalFixture("project_manager", sess("BACKLOG", "UNDER REVIEW"), nil)
		res, err := f.svc.DecideApproval(context.Background(), nil, "t1", "reject", "Belum memenuhi kriteria.", "pm1", "")
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusName != "IN PROGRESS" {
			t.Fatalf("status = %s", res.StatusName)
		}
		if len(f.pics.phases) != 1 || f.pics.phases[0] != "u-assignee" {
			t.Fatalf("PIC cadangan = assignee: %v", f.pics.phases)
		}
	})
	t.Run("tanpa assignee -> pengambil keputusan menjadi PIC", func(t *testing.T) {
		f := newApprovalFixture("admin_workspace", nil, nil)
		f.repo.byID["t1"].Assignees = nil
		if _, err := f.svc.DecideApproval(context.Background(), nil, "t1", "reject", "Belum memenuhi kriteria.", "aw1", ""); err != nil {
			t.Fatal(err)
		}
		if len(f.pics.phases) != 1 || f.pics.phases[0] != "aw1" {
			t.Fatalf("PIC cadangan terakhir = aktor: %v", f.pics.phases)
		}
	})
	t.Run("status kerja terakhir sudah dihapus -> IN PROGRESS", func(t *testing.T) {
		f := newApprovalFixture("approver", sess("QA TESTING", "UNDER REVIEW"), nil)
		f.status["st-qa"].IsUndefined = true
		res, err := f.svc.DecideApproval(context.Background(), nil, "t1", "reject", "Belum memenuhi kriteria.", "a1", "")
		if err != nil || res.StatusName != "IN PROGRESS" {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	})
}

func TestDecideApproval_Conflicts(t *testing.T) {
	ctx := context.Background()
	t.Run("task bukan UNDER REVIEW -> ErrNotInReview, tidak ada yang ditulis", func(t *testing.T) {
		f := newApprovalFixture("approver", nil, nil)
		f.store.lockStatus = "DONE" // mis. approver lain sudah memutuskan
		_, err := f.svc.DecideApproval(ctx, nil, "t1", "approve", "", "a1", "")
		if !errors.Is(err, domain.ErrNotInReview) {
			t.Fatalf("err = %v", err)
		}
		if len(f.store.setCalls)+len(f.store.recorded)+len(f.store.notified) != 0 {
			t.Fatal("tidak boleh menulis apa pun")
		}
	})
	t.Run("tolak tanpa alasan -> ErrApprovalNoteRequired", func(t *testing.T) {
		f := newApprovalFixture("approver", nil, nil)
		if _, err := f.svc.DecideApproval(ctx, nil, "t1", "reject", "abc", "a1", ""); !errors.Is(err, domain.ErrApprovalNoteRequired) {
			t.Fatalf("err = %v", err)
		}
		if len(f.store.setCalls) != 0 {
			t.Fatal("status tidak boleh berubah")
		}
	})
	t.Run("task tidak ada", func(t *testing.T) {
		f := newApprovalFixture("approver", nil, nil)
		if _, err := f.svc.DecideApproval(ctx, nil, "nope", "approve", "", "a1", ""); !errors.Is(err, domain.ErrTaskNotFound) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("setuju diblokir predecessor belum selesai", func(t *testing.T) {
		f := newApprovalFixture("approver", sess("IN PROGRESS", "UNDER REVIEW"), blockedDeps{&fakeTaskDeps{}})
		_, err := f.svc.DecideApproval(ctx, nil, "t1", "approve", "", "a1", "")
		var block *domain.PredecessorBlockingError
		if !errors.As(err, &block) {
			t.Fatalf("err = %v, want PredecessorBlockingError", err)
		}
		if len(f.store.recorded) != 0 {
			t.Fatal("keputusan tidak boleh tercatat")
		}
	})
	t.Run("tolak tidak dihalangi dependency", func(t *testing.T) {
		f := newApprovalFixture("approver", sess("IN PROGRESS", "UNDER REVIEW"), blockedDeps{&fakeTaskDeps{}})
		if _, err := f.svc.DecideApproval(ctx, nil, "t1", "reject", "Perlu perbaikan lagi.", "a1", ""); err != nil {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestDecideApproval_RoleGate(t *testing.T) {
	cases := []struct {
		name, role, actorRole string
		follows               bool
		wantErr               error
	}{
		{"approver anggota", "approver", "", true, nil},
		{"approver bukan pengikut project", "approver", "", false, domain.ErrForbidden},
		{"project manager", "project_manager", "", false, nil},
		{"admin workspace", "admin_workspace", "", false, nil},
		{"platform admin", "", "platform_admin", false, nil},
		{"editor", "editor", "", true, domain.ErrForbidden},
		{"viewer", "viewer", "", true, domain.ErrForbidden},
		{"division viewer", "division_viewer", "", true, domain.ErrForbidden},
		{"bukan anggota", "", "", true, domain.ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newApprovalFixture(tc.role, nil, nil)
			f.store.follows = tc.follows
			_, err := f.svc.DecideApproval(context.Background(), nil, "t1", "approve", "", "u1", tc.actorRole)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil && len(f.store.setCalls) != 0 {
				t.Fatal("status tidak boleh berubah saat ditolak")
			}
		})
	}
}

func TestDecideApproval_StoreNotWired(t *testing.T) {
	svc := NewTaskService(&fakeTaskRepo{}, &fakeTaskPics{}, &fakeTaskDeps{}, &fakeTaskSessions{}, &fakeTaskProjects{}, &fakeTaskStatuses{},
		&fakeSprintRBAC{role: "approver"}, &fakeSprintProjectRoles{}, &fakeTaskRules{}, &fakeTaskSprints{})
	if _, err := svc.DecideApproval(context.Background(), nil, "t1", "approve", "", "u1", ""); err == nil {
		t.Fatal("tanpa WithApprovals harus gagal, bukan panik")
	}
}

func TestListApprovals(t *testing.T) {
	ctx := context.Background()
	f := newApprovalFixture("approver", nil, nil)
	if _, err := f.svc.ListApprovals(ctx, nil, "p1", "u1", "", "decided", 1, 10); err != nil || f.store.listView != "decided" {
		t.Fatalf("err=%v view=%q", err, f.store.listView)
	}
	for _, bad := range []struct{ project, view string }{{"", "pending"}, {"p1", "all"}, {"p1", ""}} {
		if _, err := f.svc.ListApprovals(ctx, nil, bad.project, "u1", "", bad.view, 1, 10); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("(%q,%q) err = %v, want ErrInvalidInput", bad.project, bad.view, err)
		}
	}
	for _, role := range []string{"editor", "viewer", ""} {
		e := newApprovalFixture(role, nil, nil)
		if _, err := e.svc.ListApprovals(ctx, nil, "p1", "u1", "", "pending", 1, 10); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("role %q err = %v, want ErrForbidden", role, err)
		}
	}
	nf := newApprovalFixture("approver", nil, nil)
	nf.store.follows = false
	if _, err := nf.svc.ListApprovals(ctx, nil, "p1", "u1", "", "pending", 1, 10); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("approver non-pengikut err = %v", err)
	}
}
