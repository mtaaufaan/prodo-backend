package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// S6E-15 (IG-146): kolom Editor `rbac_matrix.xlsx` untuk rute yang digerbangi MIDDLEWARE (`RequireRole`, level workspace).
// Rute project/task digerbangi di layanan (diuji `service/editor_rbac_matrix_test.go`); tes ini mengunci bahwa
// registrasi rute workspace tidak pernah diam-diam membuka Editor.
var roleName = regexp.MustCompile(`"([a-z_]+)"`)

var routeGuard = regexp.MustCompile(`v1\.(Get|Post|Put|Patch|Delete)\("([^"]+)".*RequireRole\(accountSvc, rbacSvc, ((?:"[a-z_]+",?\s*)+)\)`)

// Rute workspace yang BOLEH dibaca Editor (bukan kelola): daftar member (picker assignee/mention) dan daftar status.
var editorReadable = map[string]bool{
	"GET /workspaces/:wsId/members":  true,
	"GET /workspaces/:wsId/statuses": true,
}

// Baris matriks ✗ untuk Editor yang harus murni Admin Workspace di tingkat workspace.
var adminOnly = []string{
	"PUT /workspaces/:wsId/members/:userId/role",     // No 3 kelola member workspace
	"DELETE /workspaces/:wsId/members/:userId",       // No 3
	"POST /workspaces/:wsId/invitations",             // No 3
	"POST /workspaces/:wsId/statuses",                // No 16 status workspace
	"POST /workspaces/:wsId/rules",                   // No 23 rule workspace
	"GET /workspaces/:wsId/rules/executions",         // No 24a log eksekusi
	"PUT /workspaces/:wsId/mention-settings",         // No 19a cooldown mention
	"POST /workspaces/:wsId/webhooks",                // No 29 webhook
	"GET /workspaces/:wsId/audit-logs",               // No 30 audit trail
	"GET /workspaces/:wsId/documents",                // dokumen & lampiran AW
	"DELETE /workspaces/:wsId/documents/:id",         // No 21 hapus permanen AW
	"POST /workspaces/:wsId/documents/quota-request", // No 31 kuota
	"GET /workspaces/:wsId/performance",              // No 27a performance
}

func TestEditorMatrix_WorkspaceRouteGuards(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string][]string{}
	for _, line := range strings.Split(string(src), "\n") {
		m := routeGuard.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := strings.ToUpper(m[1]) + " " + m[2]
		for _, r := range roleName.FindAllStringSubmatch(m[3], -1) {
			roles[key] = append(roles[key], r[1])
		}
	}
	if len(roles) < 30 {
		t.Fatalf("hanya %d rute ber-RequireRole terbaca; regex usang?", len(roles))
	}

	for route, rs := range roles {
		for _, r := range rs {
			if r == "editor" && !editorReadable[route] {
				t.Errorf("%s membuka Editor (%v) padahal bukan rute baca yang diizinkan", route, rs)
			}
		}
	}
	for route := range editorReadable {
		if _, ok := roles[route]; !ok {
			t.Errorf("rute baca Editor %s tidak ditemukan; allowlist usang?", route)
		}
	}
	for _, route := range adminOnly {
		rs, ok := roles[route]
		if !ok {
			t.Errorf("rute %s tidak ber-RequireRole", route)
			continue
		}
		if len(rs) != 1 || rs[0] != "admin_workspace" {
			t.Errorf("%s harus murni admin_workspace, ternyata %v", route, rs)
		}
	}
}
