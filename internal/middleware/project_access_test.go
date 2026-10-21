package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

type stubAccessChecker struct {
	restricted, followed bool
	err                  error
	gotProject, gotTask  string
	gotAttachment        string
	gotSprint            string
}

func (s *stubAccessChecker) FollowedProjectAccess(_ context.Context, _ db.Executor, projectID, _ string) (restricted, followed bool, err error) {
	s.gotProject = projectID
	return s.restricted, s.followed, s.err
}

func (s *stubAccessChecker) FollowedTaskAccess(_ context.Context, _ db.Executor, taskID, _ string) (restricted, followed bool, err error) {
	s.gotTask = taskID
	return s.restricted, s.followed, s.err
}

func (s *stubAccessChecker) FollowedAttachmentAccess(_ context.Context, _ db.Executor, attachmentID, _ string) (restricted, followed bool, err error) {
	s.gotAttachment = attachmentID
	return s.restricted, s.followed, s.err
}

func (s *stubAccessChecker) FollowedSprintAccess(_ context.Context, _ db.Executor, sprintID, _ string) (restricted, followed bool, err error) {
	s.gotSprint = sprintID
	return s.restricted, s.followed, s.err
}

func accessApp(checker FollowedProjectAccessChecker, byTask bool) *fiber.App {
	app := fiber.New()
	app.Get("/x/:id", withDBTx(), func(c *fiber.Ctx) error {
		c.Locals(actorUserIDLocalsKey, "u1")
		return c.Next()
	}, RequireFollowedProjectAccess(checker, byTask), func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
	return app
}

func TestRequireFollowedProjectAccess(t *testing.T) {
	cases := []struct {
		name                 string
		restricted, followed bool
		byTask               bool
		want                 int
	}{
		{"editor/approver pengikut lolos", true, true, false, http.StatusOK},
		{"editor/approver non-anggota ditolak", true, false, false, http.StatusForbidden},
		{"role lain tidak digerbang", false, false, false, http.StatusOK},
		{"editor non-anggota ditolak lewat task", true, false, true, http.StatusForbidden},
		{"editor pengikut lolos lewat task", true, true, true, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checker := &stubAccessChecker{restricted: tc.restricted, followed: tc.followed}
			resp, err := accessApp(checker, tc.byTask).Test(httptest.NewRequest(http.MethodGet, "/x/abc", http.NoBody))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if tc.byTask && checker.gotTask != "abc" || !tc.byTask && checker.gotProject != "abc" {
				t.Fatalf("id param tidak diteruskan ke checker yang benar: %+v", checker)
			}
		})
	}
}

func TestRequireFollowedProjectAccess_checkerError(t *testing.T) {
	checker := &stubAccessChecker{err: context.DeadlineExceeded}
	resp, err := accessApp(checker, false).Test(httptest.NewRequest(http.MethodGet, "/x/abc", http.NoBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

func queryApp(checker FollowedProjectAccessChecker) *fiber.App {
	app := fiber.New()
	app.Get("/me/tasks", withDBTx(), func(c *fiber.Ctx) error {
		c.Locals(actorUserIDLocalsKey, "u1")
		return c.Next()
	}, RequireFollowedProjectQuery(checker, "project_id"), func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
	return app
}

// S7A-08 (IG-154): GET /me/tasks?project_id= memakai gerbang project diikuti yang sama dengan endpoint baca lain.
func TestRequireFollowedProjectQuery(t *testing.T) {
	const pid = "11111111-1111-4111-8111-111111111111"
	cases := []struct {
		name                 string
		url                  string
		restricted, followed bool
		want                 int
		wantChecked          bool
	}{
		{"approver/editor pengikut lolos", "/me/tasks?project_id=" + pid, true, true, http.StatusOK, true},
		{"approver/editor non-anggota 403", "/me/tasks?project_id=" + pid, true, false, http.StatusForbidden, true},
		{"role lain tidak digerbang", "/me/tasks?project_id=" + pid, false, false, http.StatusOK, true},
		{"project_id kosong diteruskan ke handler (422 di sana)", "/me/tasks", true, false, http.StatusOK, false},
		{"project_id bukan UUID 422", "/me/tasks?project_id=abc", true, true, http.StatusUnprocessableEntity, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checker := &stubAccessChecker{restricted: tc.restricted, followed: tc.followed}
			resp, err := queryApp(checker).Test(httptest.NewRequest(http.MethodGet, tc.url, http.NoBody))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if got := checker.gotProject == pid; got != tc.wantChecked {
				t.Fatalf("checker dipanggil dengan %q, dipanggil=%v want %v", checker.gotProject, got, tc.wantChecked)
			}
		})
	}
}

// S7A-10 (IG-156): rute berbasis id lampiran memakai gerbang project diikuti yang sama.
func TestRequireFollowedAttachmentAccess(t *testing.T) {
	const aid = "22222222-2222-4222-8222-222222222222"
	cases := []struct {
		name                 string
		id                   string
		restricted, followed bool
		want                 int
		wantChecked          bool
	}{
		{"pengikut lolos", aid, true, true, http.StatusOK, true},
		{"editor/approver non-anggota 403", aid, true, false, http.StatusForbidden, true},
		{"role lain tidak digerbang", aid, false, false, http.StatusOK, true},
		{"id bukan UUID 422", "abc", true, true, http.StatusUnprocessableEntity, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checker := &stubAccessChecker{restricted: tc.restricted, followed: tc.followed}
			app := fiber.New()
			app.Get("/attachments/:id/download", withDBTx(), func(c *fiber.Ctx) error {
				c.Locals(actorUserIDLocalsKey, "u1")
				return c.Next()
			}, RequireFollowedAttachmentAccess(checker), func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/attachments/"+tc.id+"/download", http.NoBody))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if got := checker.gotAttachment == aid; got != tc.wantChecked {
				t.Fatalf("checker dipanggil dengan %q, dipanggil=%v want %v", checker.gotAttachment, got, tc.wantChecked)
			}
		})
	}
}

// S7A-13 (IG-159): GET /sprints/:id/summary memakai gerbang project diikuti yang sama.
func TestRequireFollowedSprintAccess(t *testing.T) {
	const sid = "33333333-3333-4333-8333-333333333333"
	cases := []struct {
		name                 string
		id                   string
		restricted, followed bool
		want                 int
		wantChecked          bool
	}{
		{"pengikut lolos", sid, true, true, http.StatusOK, true},
		{"editor/approver non-anggota 403", sid, true, false, http.StatusForbidden, true},
		{"role lain tidak digerbang", sid, false, false, http.StatusOK, true},
		{"id bukan UUID 422", "abc", true, true, http.StatusUnprocessableEntity, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checker := &stubAccessChecker{restricted: tc.restricted, followed: tc.followed}
			app := fiber.New()
			app.Get("/sprints/:id/summary", withDBTx(), func(c *fiber.Ctx) error {
				c.Locals(actorUserIDLocalsKey, "u1")
				return c.Next()
			}, RequireFollowedSprintAccess(checker), func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/sprints/"+tc.id+"/summary", http.NoBody))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if got := checker.gotSprint == sid; got != tc.wantChecked {
				t.Fatalf("checker dipanggil dengan %q, dipanggil=%v want %v", checker.gotSprint, got, tc.wantChecked)
			}
		})
	}
}
