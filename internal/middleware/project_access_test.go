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
}

func (s *stubAccessChecker) FollowedProjectAccess(_ context.Context, _ db.Executor, projectID, _ string) (restricted, followed bool, err error) {
	s.gotProject = projectID
	return s.restricted, s.followed, s.err
}

func (s *stubAccessChecker) FollowedTaskAccess(_ context.Context, _ db.Executor, taskID, _ string) (restricted, followed bool, err error) {
	s.gotTask = taskID
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
