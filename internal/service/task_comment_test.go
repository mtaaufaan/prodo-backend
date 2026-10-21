package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// fakeCommentStore -- pengganti commentStore di memori; mencatat notifikasi, mention, follower, audit.
type fakeCommentStore struct {
	tc         repository.TaskCommentContext
	roles      repository.ActorProjectRoles
	rec        repository.CommentRecord
	cands      []repository.MentionCandidate
	recipients []string
	existing   map[string]bool
	blocked    map[string]bool // userID -> cooldown berlaku

	notifs    []string // "user:type"
	mentions  map[string]string
	followers map[string]string
	audits    []string
	updated   string
	deleted   bool
}

func newFake() *fakeCommentStore {
	return &fakeCommentStore{
		tc:         repository.TaskCommentContext{TaskID: "t1", TaskCode: "QST-001", TaskTitle: "Judul", ProjectID: "p1", WorkspaceID: "w1", CooldownMin: 10, DigestEnabled: true},
		roles:      repository.ActorProjectRoles{WorkspaceRole: "editor"},
		rec:        repository.CommentRecord{ID: "c1", TaskID: "t1", UserID: "me", AuthorName: "Eka", Text: "isi lama"},
		cands:      []repository.MentionCandidate{{UserID: "u2", DisplayName: "Budi", Email: "budi@x.id"}, {UserID: "u3", DisplayName: "Citra", Email: "citra@x.id"}},
		existing:   map[string]bool{},
		blocked:    map[string]bool{},
		mentions:   map[string]string{},
		followers:  map[string]string{},
		recipients: []string{"me", "u2", "u4", "u4"},
	}
}

func (f *fakeCommentStore) GetTaskContext(context.Context, db.Executor, string) (*repository.TaskCommentContext, error) {
	return &f.tc, nil
}
func (f *fakeCommentStore) ActorRoles(context.Context, db.Executor, string, string) (repository.ActorProjectRoles, error) {
	return f.roles, nil
}
func (f *fakeCommentStore) Create(_ context.Context, _ db.Executor, _, userID, text string) (*repository.CommentRecord, error) {
	r := f.rec
	r.UserID, r.Text = userID, text
	return &r, nil
}
func (f *fakeCommentStore) Get(context.Context, db.Executor, string) (*repository.CommentRecord, error) {
	r := f.rec
	return &r, nil
}
func (f *fakeCommentStore) List(context.Context, db.Executor, string) ([]repository.CommentRecord, error) {
	return []repository.CommentRecord{f.rec}, nil
}
func (f *fakeCommentStore) UpdateText(_ context.Context, _ db.Executor, _, text string) error {
	f.updated = text
	return nil
}
func (f *fakeCommentStore) SoftDelete(context.Context, db.Executor, string) error {
	f.deleted = true
	return nil
}
func (f *fakeCommentStore) CountForTask(context.Context, db.Executor, string) (int, error) {
	return 3, nil
}
func (f *fakeCommentStore) EnsureFollower(_ context.Context, _ db.Executor, _, userID, source string) error {
	if _, ok := f.followers[userID]; !ok {
		f.followers[userID] = source
	}
	return nil
}
func (f *fakeCommentStore) Unfollow(_ context.Context, _ db.Executor, _, userID string) error {
	delete(f.followers, userID)
	return nil
}
func (f *fakeCommentStore) NotifyRecipients(context.Context, db.Executor, string) ([]string, error) {
	return f.recipients, nil
}
func (f *fakeCommentStore) MentionCandidates(context.Context, db.Executor, string) ([]repository.MentionCandidate, error) {
	return f.cands, nil
}
func (f *fakeCommentStore) ExistingMentionUsers(context.Context, db.Executor, string) (map[string]bool, error) {
	return f.existing, nil
}
func (f *fakeCommentStore) MentionBlock(_ context.Context, _ db.Executor, userID, _ string, _ int) (blocked bool, windowEnds time.Time, err error) {
	return f.blocked[userID], time.Now().Add(5 * time.Minute), nil
}
func (f *fakeCommentStore) InsertMention(_ context.Context, _ db.Executor, _, _, userID, _, state string, _ *time.Time) error {
	f.mentions[userID] = state
	return nil
}
func (f *fakeCommentStore) InsertNotification(_ context.Context, _ db.Executor, userID, _, typ, _, _, _, _ string) error {
	f.notifs = append(f.notifs, userID+":"+typ)
	return nil
}
func (f *fakeCommentStore) AuditComment(_ context.Context, _ db.Executor, _, role, action string, _ *repository.TaskCommentContext, _, _ map[string]any) error {
	f.audits = append(f.audits, action+"@"+role)
	return nil
}

func TestCreateComment_validasiDanIzin(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	svc := NewTaskCommentService(f)

	for _, text := range []string{"", " a ", strings.Repeat("x", 1001)} {
		if _, err := svc.Create(ctx, nil, "t1", "me", text); !errors.Is(err, domain.ErrCommentInvalid) {
			t.Fatalf("%q: err = %v, want ErrCommentInvalid", text[:min(len(text), 4)], err)
		}
	}
	f.roles = repository.ActorProjectRoles{WorkspaceRole: "viewer"}
	if _, err := svc.Create(ctx, nil, "t1", "me", "halo"); !errors.Is(err, domain.ErrCommentForbidden) {
		t.Fatalf("viewer: err = %v, want ErrCommentForbidden", err)
	}
	for _, r := range []repository.ActorProjectRoles{{WorkspaceRole: "editor"}, {WorkspaceRole: "approver"}, {WorkspaceRole: "division_viewer"}, {IsPM: true}, {ProjectRole: "approver"}} {
		f.roles = r
		if _, err := svc.Create(ctx, nil, "t1", "me", "halo"); err != nil {
			t.Fatalf("%+v: %v", r, err)
		}
	}
}

func TestCreateComment_followNotifikasiDanAudit(t *testing.T) {
	f := newFake()
	res, err := NewTaskCommentService(f).Create(context.Background(), nil, "t1", "me", "cek @budi ya")
	if err != nil {
		t.Fatal(err)
	}
	if f.followers["me"] != "comment" || f.followers["u2"] != "mention" {
		t.Fatalf("auto-follow salah: %v", f.followers)
	}
	// u2 = target mention (notifikasi mention, BUKAN komentar biasa); me = penulis (dikecualikan); u4 sekali.
	want := []string{"u2:task_mention", "u4:task_comment"}
	if strings.Join(f.notifs, ",") != strings.Join(want, ",") {
		t.Fatalf("notifikasi = %v, want %v", f.notifs, want)
	}
	if res.CommentCount != 3 || len(res.Mentions) != 1 || res.Mentions[0] != "Budi" || res.Notified != 1 {
		t.Fatalf("hasil = %+v", res)
	}
	if len(f.audits) != 1 || f.audits[0] != "comment.created@editor" {
		t.Fatalf("audit = %v", f.audits)
	}
}

func TestCreateComment_cooldownMention(t *testing.T) {
	t.Run("dalam cooldown + digest aktif -> diakumulasi, tanpa notifikasi langsung", func(t *testing.T) {
		f := newFake()
		f.blocked["u2"] = true
		res, err := NewTaskCommentService(f).Create(context.Background(), nil, "t1", "me", "@budi lagi")
		if err != nil {
			t.Fatal(err)
		}
		if f.mentions["u2"] != "pending" || res.Notified != 1 {
			t.Fatalf("mentions=%v notified=%d", f.mentions, res.Notified)
		}
		for _, n := range f.notifs {
			if strings.HasPrefix(n, "u2:") {
				t.Fatalf("u2 tidak boleh diberi notifikasi langsung: %v", f.notifs)
			}
		}
	})
	t.Run("dalam cooldown + digest nonaktif -> ditekan", func(t *testing.T) {
		f := newFake()
		f.blocked["u2"] = true
		f.tc.DigestEnabled = false
		res, err := NewTaskCommentService(f).Create(context.Background(), nil, "t1", "me", "@budi lagi")
		if err != nil {
			t.Fatal(err)
		}
		if f.mentions["u2"] != "dropped" || res.Notified != 0 {
			t.Fatalf("mentions=%v notified=%d", f.mentions, res.Notified)
		}
	})
	t.Run("target berbeda tidak terpengaruh cooldown target lain", func(t *testing.T) {
		f := newFake()
		f.blocked["u2"] = true
		if _, err := NewTaskCommentService(f).Create(context.Background(), nil, "t1", "me", "@budi @citra"); err != nil {
			t.Fatal(err)
		}
		if f.mentions["u2"] != "pending" || f.mentions["u3"] != "sent" {
			t.Fatalf("mentions = %v", f.mentions)
		}
	})
	t.Run("mention diri sendiri diabaikan", func(t *testing.T) {
		f := newFake()
		f.cands = append(f.cands, repository.MentionCandidate{UserID: "me", DisplayName: "Eka", Email: "eka@x.id"})
		if _, err := NewTaskCommentService(f).Create(context.Background(), nil, "t1", "me", "@eka catatan"); err != nil {
			t.Fatal(err)
		}
		if len(f.mentions) != 0 {
			t.Fatalf("mentions = %v", f.mentions)
		}
	})
}

func TestEditComment(t *testing.T) {
	ctx := context.Background()
	t.Run("bukan penulis ditolak", func(t *testing.T) {
		f := newFake()
		if _, err := NewTaskCommentService(f).Edit(ctx, nil, "c1", "orang-lain", "baru"); !errors.Is(err, domain.ErrCommentForbidden) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("lewat 3 menit ditolak (COMMENT_EDIT_LOCKED)", func(t *testing.T) {
		f := newFake()
		f.rec.Locked = true
		if _, err := NewTaskCommentService(f).Edit(ctx, nil, "c1", "me", "baru"); !errors.Is(err, domain.ErrCommentEditLocked) {
			t.Fatalf("err = %v", err)
		}
		if f.updated != "" {
			t.Fatal("teks tidak boleh berubah saat terkunci")
		}
	})
	t.Run("dalam jendela: tersimpan, diaudit dengan sebelum/sesudah", func(t *testing.T) {
		f := newFake()
		if _, err := NewTaskCommentService(f).Edit(ctx, nil, "c1", "me", "  baru  "); err != nil {
			t.Fatal(err)
		}
		if f.updated != "baru" || len(f.audits) != 1 || f.audits[0] != "comment.edited@editor" {
			t.Fatalf("updated=%q audits=%v", f.updated, f.audits)
		}
	})
	t.Run("mention yang sudah tercatat tidak dikirim ulang, mention baru diproses", func(t *testing.T) {
		f := newFake()
		f.existing["u2"] = true
		if _, err := NewTaskCommentService(f).Edit(ctx, nil, "c1", "me", "@budi dan @citra"); err != nil {
			t.Fatal(err)
		}
		if _, ok := f.mentions["u2"]; ok {
			t.Fatal("u2 sudah di-mention sebelumnya, tidak boleh diproses ulang")
		}
		if f.mentions["u3"] != "sent" {
			t.Fatalf("mentions = %v", f.mentions)
		}
	})
	t.Run("teks tidak valid", func(t *testing.T) {
		if _, err := NewTaskCommentService(newFake()).Edit(ctx, nil, "c1", "me", " "); !errors.Is(err, domain.ErrCommentInvalid) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestDeleteComment(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		user  string
		roles repository.ActorProjectRoles
		ok    bool
	}{
		{"penulis", "me", repository.ActorProjectRoles{WorkspaceRole: "editor"}, true},
		{"editor lain", "x", repository.ActorProjectRoles{WorkspaceRole: "editor"}, false},
		{"PM project", "x", repository.ActorProjectRoles{WorkspaceRole: "project_manager", IsPM: true}, true},
		{"PM workspace bukan PM project ini", "x", repository.ActorProjectRoles{WorkspaceRole: "project_manager"}, false},
		{"Admin Workspace", "x", repository.ActorProjectRoles{WorkspaceRole: "admin_workspace"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			f.roles = tc.roles
			err := NewTaskCommentService(f).Delete(ctx, nil, "c1", tc.user)
			if tc.ok && (err != nil || !f.deleted || len(f.audits) != 1) {
				t.Fatalf("err=%v deleted=%v audits=%v", err, f.deleted, f.audits)
			}
			if !tc.ok && !errors.Is(err, domain.ErrCommentForbidden) {
				t.Fatalf("err = %v, want ErrCommentForbidden", err)
			}
		})
	}
}

func TestListCommentFlags(t *testing.T) {
	f := newFake()
	list, err := NewTaskCommentService(f).List(context.Background(), nil, "t1", "me")
	if err != nil || len(list) != 1 || !list[0].CanEdit || !list[0].CanDelete {
		t.Fatalf("penulis dalam jendela: %+v err=%v", list, err)
	}
	f.rec.Locked = true
	list, _ = NewTaskCommentService(f).List(context.Background(), nil, "t1", "me")
	if list[0].CanEdit || !list[0].CanDelete {
		t.Fatalf("terkunci: tidak bisa edit tapi bisa hapus, got %+v", list[0])
	}
	list, _ = NewTaskCommentService(f).List(context.Background(), nil, "t1", "orang-lain")
	if list[0].CanEdit || list[0].CanDelete {
		t.Fatalf("orang lain (editor): tanpa izin, got %+v", list[0])
	}
}

func TestSetFollow(t *testing.T) {
	f := newFake()
	svc := NewTaskCommentService(f)
	if err := svc.SetFollow(context.Background(), nil, "t1", "me", true); err != nil || f.followers["me"] != "manual" {
		t.Fatalf("follow: err=%v followers=%v", err, f.followers)
	}
	if err := svc.SetFollow(context.Background(), nil, "t1", "me", false); err != nil || len(f.followers) != 0 {
		t.Fatalf("unfollow: err=%v followers=%v", err, f.followers)
	}
}

func TestNormalizePaging(t *testing.T) {
	for _, c := range [][4]int{{0, 0, 1, 10}, {-3, 5, 1, 5}, {2, 500, 2, 50}, {4, 10, 4, 10}} {
		if p, pp := NormalizePaging(c[0], c[1]); p != c[2] || pp != c[3] {
			t.Errorf("NormalizePaging(%d,%d) = %d,%d want %d,%d", c[0], c[1], p, pp, c[2], c[3])
		}
	}
}
