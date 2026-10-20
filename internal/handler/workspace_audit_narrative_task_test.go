package handler

import (
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

func ptr(s string) *string { return &s }

func entry(action, target, meta, before, after string) *repository.WorkspaceAuditLogEntry {
	e := &repository.WorkspaceAuditLogEntry{Action: action, TargetName: ptr(target)}
	if meta != "" {
		e.Metadata = []byte(meta)
	}
	if before != "" {
		e.StateBefore = []byte(before)
	}
	if after != "" {
		e.StateAfter = []byte(after)
	}
	return e
}

// Narasi aksi task (IG-130) -- harus sama kalimatnya dengan
// features/workspace-audit/narrative.ts (ekspor CSV memakai port Go ini).
func TestWorkspaceAuditNarrativeTaskActions(t *testing.T) {
	meta := `{"task_code":"PRODO-023"}`
	cases := []struct {
		name string
		e    *repository.WorkspaceAuditLogEntry
		want string
	}{
		{"dibuat dari import", entry("task.created", "Login", meta, "", `{"imported":true}`), `Task PRODO-023 "Login" dibuat (hasil import)`},
		{"status", entry("task.status_changed", "Login", meta, `{"status":"BACKLOG"}`, `{"status":"IN PROGRESS"}`), `Status task PRODO-023 "Login" diubah dari BACKLOG menjadi IN PROGRESS`},
		{"disetujui dengan catatan", entry("task.approved", "Login", meta, `{"status":"UNDER REVIEW"}`, `{"status":"DONE","note":"Sesuai kriteria."}`), `Task PRODO-023 "Login" disetujui dan dipindahkan ke DONE (catatan: Sesuai kriteria.)`},
		{"disetujui tanpa catatan", entry("task.approved", "Login", meta, `{"status":"UNDER REVIEW"}`, `{"status":"DONE"}`), `Task PRODO-023 "Login" disetujui dan dipindahkan ke DONE`},
		{"ditolak dengan alasan", entry("task.rejected", "Login", meta, `{"status":"UNDER REVIEW"}`, `{"status":"IN PROGRESS","note":"Uji ulang pembayaran."}`), `Task PRODO-023 "Login" ditolak dan dikembalikan ke IN PROGRESS. Alasan: Uji ulang pembayaran.`},
		{"kelengkapan", entry("task.completeness_changed", "Login", meta, `{"completeness":"incomplete"}`, `{"completeness":"complete"}`), `Kelengkapan task PRODO-023 "Login" diubah dari Belum Lengkap menjadi Lengkap`},
		{"diperbarui dengan nilai sebelum", entry("task.updated", "Baru", meta, `{"title":"Lama","priority":"low"}`, `{"title":"Baru","priority":"high"}`), `Task PRODO-023 "Baru" diperbarui: judul "Lama" menjadi "Baru", prioritas low menjadi high`},
		{"diperbarui baris lama tanpa nilai sebelum", entry("task.updated", "Baru", "", "", `{"title":"Baru","priority":"high"}`), `Task "Baru" diperbarui`},
		{"dependency", entry("task.dependency_added", "B", meta, "", `{"predecessor_code":"PRODO-001","predecessor_title":"A"}`), `Task PRODO-023 "B" kini menunggu task PRODO-001 "A"`},
		{"komentar dibuat dengan mention", entry("comment.created", "B", meta, "", `{"mentions":["Eka","Budi"]}`), `Komentar ditambahkan pada task PRODO-023 "B" (mention: Eka, Budi)`},
		{"komentar dibuat tanpa mention", entry("comment.created", "B", meta, "", `{"mentions":[]}`), `Komentar ditambahkan pada task PRODO-023 "B"`},
		{"komentar diedit", entry("comment.edited", "B", meta, `{"text":"lama"}`, `{"text":"baru"}`), `Komentar pada task PRODO-023 "B" diedit dari "lama" menjadi "baru"`},
		{"komentar dihapus", entry("comment.deleted", "B", meta, `{"comment_id":"c1"}`, ""), `Komentar pada task PRODO-023 "B" dihapus`},
		{"pic ditambah", entry("task.pic_added", "B", meta, "", `{"status_name":"IN PROGRESS","user_name":"Joyse"}`), `PIC paralel "Joyse" ditambahkan pada fase IN PROGRESS task PRODO-023 "B"`},
		{"pic diserahkan", entry("task.pic_handoff", "B", meta, "", `{"status_name":"UNDER REVIEW","from_user_names":["Joyse","Amir"],"to_user_name":"Taufan"}`), `PIC fase UNDER REVIEW task PRODO-023 "B" diserahkan dari Joyse, Amir kepada "Taufan"`},
		{"lampiran diunduh", entry("attachment.downloaded", "wireframe.pdf", "", "", ""), `Lampiran "wireframe.pdf" diunduh`},
		{"waktu dicatat langsung disetujui", entry("task.time_entry_added", "B", meta, "", `{"duration_minutes":150,"approval_status":"approved"}`), `Waktu dicatat (150 menit) pada task PRODO-023 "B", langsung disetujui`},
		{"entri manual menunggu persetujuan", entry("task.time_entry_added", "B", meta, "", `{"duration_minutes":90,"approval_status":"pending"}`), `Entri waktu manual (90 menit) ditambahkan pada task PRODO-023 "B", menunggu persetujuan`},
		{"entri waktu ditolak", entry("task.time_entry_rejected", "B", meta, "", `{"rejection_note":"durasi salah"}`), `Entri waktu task PRODO-023 "B" ditolak: durasi salah`},
		{"ekspor audit project", entry("audit.report_downloaded", "PRODO", "", "", `{"scope":"project","rows":12}`), `Audit Trail project "PRODO" diekspor (CSV, 12 baris)`},
		{"task lama tanpa snapshot", entry("task.deleted", "", "", "", ""), `Task "tidak diketahui" dihapus (masuk masa retensi)`},
	}
	for _, c := range cases {
		if got := workspaceAuditNarrativeText(c.e); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}
}
