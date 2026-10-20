package service

import "testing"

func TestCapabilitiesForRole(t *testing.T) {
	cases := []struct {
		role string
		want Capabilities
	}{
		{"editor", Capabilities{CreateTask: true, EditOwnTask: true, Comment: true, LogTime: true, Attach: true}},
		{"approver", Capabilities{Comment: true, Attach: true, ApproveTask: true}},
		{"viewer", Capabilities{}},
		{"division_viewer", Capabilities{Comment: true}},
		{"", Capabilities{}},
		{"project_manager", Capabilities{CreateTask: true, EditOwnTask: true, Comment: true, LogTime: true, Attach: true, ManageProject: true, ManageRules: true, ApproveTask: true}},
		{"admin_workspace", Capabilities{true, true, true, true, true, true, true, true, true}},
	}
	for _, tc := range cases {
		if got := CapabilitiesForRole(tc.role); got != tc.want {
			t.Errorf("CapabilitiesForRole(%q) = %+v, want %+v", tc.role, got, tc.want)
		}
	}
}

// Editor tidak boleh mengelola rule/member/project (desain: KELOLA RULE ✕).
func TestCapabilitiesEditorCannotManage(t *testing.T) {
	c := CapabilitiesForRole("editor")
	if c.ManageRules || c.ManageMembers || c.ManageProject {
		t.Fatalf("editor tidak boleh punya manage_*: %+v", c)
	}
}

// Approver: tidak membuat/mengubah task, tidak mencatat waktu, tidak mengelola apa pun (S7A-02).
func TestCapabilitiesApprover(t *testing.T) {
	c := CapabilitiesForRole("approver")
	if c.CreateTask || c.EditOwnTask || c.LogTime || c.ManageProject || c.ManageRules || c.ManageMembers {
		t.Fatalf("approver tidak boleh punya kemampuan tulis/kelola: %+v", c)
	}
	if !c.ApproveTask || !c.Comment || !c.Attach {
		t.Fatalf("approver wajib approve_task + comment + attach: %+v", c)
	}
	for _, role := range []string{"editor", "viewer", "division_viewer", ""} {
		if CapabilitiesForRole(role).ApproveTask {
			t.Fatalf("%q tidak boleh approve_task", role)
		}
	}
}
