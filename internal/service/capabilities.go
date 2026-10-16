package service

// Capabilities -- kemampuan role workspace untuk kerangka UI (S6E-02, Sprint 6
// "Editor Console"): dikirim lewat GET /me/context supaya frontend tidak
// menebak dari nama role. HANYA petunjuk tampilan -- penegakan hak akses
// tetap di service/middleware tiap endpoint. Mengikuti rbac_matrix.xlsx v3.1
// dan aturan yang sudah berlaku di TaskService (mis. story point Editor
// hanya bila project mengizinkan, itu diputuskan per project, bukan di sini).
type Capabilities struct {
	CreateTask    bool `json:"create_task"`
	EditOwnTask   bool `json:"edit_own_task"`
	Comment       bool `json:"comment"`
	LogTime       bool `json:"log_time"`
	Attach        bool `json:"attach"`
	ManageProject bool `json:"manage_project"`
	ManageRules   bool `json:"manage_rules"`
	ManageMembers bool `json:"manage_members"`
}

// CapabilitiesForRole -- role tak dikenal mendapat semua false.
func CapabilitiesForRole(role string) Capabilities {
	switch role {
	case "admin_workspace":
		return Capabilities{true, true, true, true, true, true, true, true}
	case "project_manager":
		return Capabilities{CreateTask: true, EditOwnTask: true, Comment: true, LogTime: true, Attach: true, ManageProject: true, ManageRules: true}
	case "editor":
		return Capabilities{CreateTask: true, EditOwnTask: true, Comment: true, LogTime: true, Attach: true}
	case "approver":
		return Capabilities{Comment: true}
	}
	return Capabilities{}
}
