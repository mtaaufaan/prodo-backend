package service

import "testing"

func TestCapabilitiesForRole(t *testing.T) {
	cases := []struct {
		role string
		want Capabilities
	}{
		{"editor", Capabilities{CreateTask: true, EditOwnTask: true, Comment: true, LogTime: true, Attach: true}},
		{"approver", Capabilities{Comment: true}},
		{"viewer", Capabilities{}},
		{"division_viewer", Capabilities{}},
		{"", Capabilities{}},
		{"project_manager", Capabilities{CreateTask: true, EditOwnTask: true, Comment: true, LogTime: true, Attach: true, ManageProject: true, ManageRules: true}},
		{"admin_workspace", Capabilities{true, true, true, true, true, true, true, true}},
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
