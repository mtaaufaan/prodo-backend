package main

import (
	"os"
	"strings"
	"testing"
)

// S7A-06 (IG-152): Editor/Approver hanya boleh membaca sub-resource task/project yang mereka ikuti. Tes ini mengunci
// bahwa registrasi rute GET tidak diam-diam kehilangan gerbang "project diikuti".
func TestApproverEditorReadGates(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(src), "\n")
	cases := []struct{ route, gate string }{
		{`v1.Get("/projects/:id/tasks"`, "followedProjectGate"},
		{`v1.Get("/projects/:id/statuses"`, "followedProjectGate"},
		{`v1.Get("/projects/:id/sprints"`, "followedProjectGate"},
		{`v1.Get("/projects/:id/members"`, "followedProjectGate"},
		{`v1.Get("/projects/:id/status-sessions"`, "followedProjectGate"},
		{`v1.Get("/projects/:id/dependencies"`, "followedProjectGate"},
		{`v1.Get("/projects/:id/pic-groups"`, "followedProjectGate"},
		{`v1.Get("/tasks/:id"`, "followedTaskGate"},
		{`v1.Get("/tasks/:id/comments"`, "followedTaskGate"},
		{`v1.Get("/tasks/:id/pic-history"`, "followedTaskGate"},
		{`v1.Get("/tasks/:id/dependencies"`, "followedTaskGate"},
		{`v1.Get("/tasks/:id/status-sessions"`, "followedTaskGate"},
		{`v1.Get("/tasks/:id/versions"`, "followedTaskGate"},
		{`v1.Get("/tasks/:id/activity"`, "followedTaskGate"},
		{`v1.Get("/tasks/:id/time-entries"`, "followedTaskGate"},
		{`v1.Get("/tasks/:id/checklist-items"`, "followedTaskGate"},
		{`v1.Get("/tasks/:id/attachments"`, "followedTaskGate"},
		{`v1.Post("/tasks/:id/approval"`, "followedTaskGate"},
		{`v1.Get("/me/tasks"`, "followedProjectQueryGate"},
		{`v1.Post("/tasks/:id/comments"`, "followedTaskGate"},
		{`v1.Put("/tasks/:id/follow"`, "followedTaskGate"},
		{`v1.Post("/tasks/:id/attachments"`, "followedTaskGate"},
		{`v1.Get("/attachments/:id/download"`, "followedAttachmentGate"},
		{`v1.Put("/attachments/:id"`, "followedAttachmentGate"},
		{`v1.Delete("/attachments/:id"`, "followedAttachmentGate"},
		{`v1.Post("/attachments/:id/restore"`, "followedAttachmentGate"},
	}
	for _, tc := range cases {
		found := false
		for _, l := range lines {
			if !strings.Contains(l, tc.route+",") {
				continue
			}
			found = true
			if !strings.Contains(l, tc.gate) {
				t.Errorf("%s tidak memakai %s: %s", tc.route, tc.gate, strings.TrimSpace(l))
			}
		}
		if !found {
			t.Errorf("rute %s tidak ditemukan; daftar usang?", tc.route)
		}
	}
}
