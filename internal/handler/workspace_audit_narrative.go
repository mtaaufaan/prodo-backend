package handler

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// workspaceAuditNarrativeText -- port Go dari formatWorkspaceAuditNarrative
// (features/workspace-audit/narrative.ts), KHUSUS dipakai
// WorkspaceAuditHandler.writeCSV -- alasan duplikasi sama persis
// groupAuditNarrativeText (lihat komentarnya).
//
// PENTING untuk pengembangan selanjutnya: setiap kali menambah kasus BARU
// di features/workspace-audit/narrative.ts, tambahkan JUGA kasusnya di
// sini -- action yang tidak dikenali jatuh ke fallback generik, TIDAK
// error.
func workspaceAuditNarrativeText(e *repository.WorkspaceAuditLogEntry) string {
	meta := parseWorkspaceAuditMetadata(e)
	target := workspaceAuditTargetOf(e)

	switch e.Action {
	case "workspace.created":
		return fmt.Sprintf(`Workspace %q dibuat`, target)
	case "workspace.updated":
		return fmt.Sprintf(`Workspace %q diperbarui`, target)
	case "workspace.archived":
		return fmt.Sprintf(`Workspace %q diarsipkan`, target)
	case "workspace.unarchived":
		return fmt.Sprintf(`Workspace %q dikeluarkan dari arsip`, target)
	case "workspace.deactivated":
		return fmt.Sprintf(`Workspace %q dinonaktifkan`, target)
	case "workspace.reactivated":
		return fmt.Sprintf(`Workspace %q diaktifkan kembali`, target)
	case "workspace.deleted":
		return fmt.Sprintf(`Workspace %q dihapus`, target)
	case "workspace.restored":
		return fmt.Sprintf(`Workspace %q dipulihkan`, target)
	case "workspace.moved":
		return fmt.Sprintf(`Workspace %q dipindahkan ke organisasi lain`, target)
	case "workspace.mention_settings_updated":
		return "Pengaturan cooldown mention workspace diperbarui"
	case "project.created":
		return fmt.Sprintf(`Project %q dibuat`, target)
	case "project.updated":
		return fmt.Sprintf(`Project %q diperbarui`, target)
	case "project.archived":
		return fmt.Sprintf(`Project %q diarsipkan`, target)
	case "project.unarchived":
		return fmt.Sprintf(`Project %q dikeluarkan dari arsip`, target)
	case "project.deleted":
		return fmt.Sprintf(`Project %q dihapus`, target)
	case "project.restored":
		return fmt.Sprintf(`Project %q dipulihkan`, target)
	case "project.pm_assigned":
		return fmt.Sprintf(`Project Manager ditetapkan untuk project %q`, target)
	case "project.pm_reassigned":
		return fmt.Sprintf(`Project Manager project %q diganti`, target)
	case "project.pm_removed":
		return fmt.Sprintf(`Project Manager project %q dicabut`, target)
	case "custom_status.created":
		return fmt.Sprintf(`Status kustom %q dibuat`, target)
	case "custom_status.updated":
		return fmt.Sprintf(`Status kustom %q diperbarui`, target)
	case "custom_status.reordered":
		return "Urutan status kustom diubah"
	case "custom_status.undefined":
		return fmt.Sprintf(`Status kustom %q dinonaktifkan (undefine)`, target)
	case "custom_status.restored":
		return fmt.Sprintf(`Status kustom %q dipulihkan`, target)
	case "custom_status.pic_requirement_changed":
		return fmt.Sprintf(`Kewajiban PIC status %q diubah`, target)
	case "custom_status.start_confirmation_changed":
		return fmt.Sprintf(`Konfirmasi mulai pengerjaan status %q diubah`, target)
	case "rule.created":
		return fmt.Sprintf(`Rule otomatisasi %q dibuat`, target)
	case "rule.deleted":
		return fmt.Sprintf(`Rule otomatisasi %q dihapus`, target)
	case "rule.activated":
		return fmt.Sprintf(`Rule otomatisasi %q diaktifkan`, target)
	case "rule.deactivated":
		return fmt.Sprintf(`Rule otomatisasi %q dinonaktifkan`, target)
	case "rule.auto_deactivated":
		return fmt.Sprintf(`Rule otomatisasi %q dinonaktifkan otomatis (status dihapus)`, target)
	case "webhook.created":
		return fmt.Sprintf(`Webhook %q didaftarkan`, target)
	case "webhook.updated":
		return fmt.Sprintf(`Webhook %q diperbarui`, target)
	case "webhook.activated":
		return fmt.Sprintf(`Webhook %q diaktifkan`, target)
	case "webhook.deactivated":
		return fmt.Sprintf(`Webhook %q dinonaktifkan`, target)
	case "webhook.secret_regenerated":
		return fmt.Sprintf(`Secret webhook %q dibuat ulang`, target)
	case "webhook.deleted":
		return fmt.Sprintf(`Webhook %q dihapus`, target)
	case "attachment.uploaded":
		return fmt.Sprintf(`Lampiran %q diunggah`, target)
	case "attachment.downloaded":
		return fmt.Sprintf(`Lampiran %q diunduh`, target)
	case "attachment.renamed":
		return fmt.Sprintf(`Lampiran diganti nama menjadi %q`, target)
	case "attachment.deleted":
		return fmt.Sprintf(`Lampiran %q dihapus (masa retensi)`, target)
	case "attachment.deleted_permanent":
		return fmt.Sprintf(`Lampiran %q dihapus permanen`, target)
	case "attachment.restored":
		return fmt.Sprintf(`Lampiran %q dipulihkan`, target)
	case "attachment.quota_requested":
		return "Permintaan tambah kuota storage dikirim ke Group Admin"
	case "member.role_changed":
		return fmt.Sprintf(`Role member %q diubah`, target)
	case "member.removed":
		return fmt.Sprintf(`Member %q dikeluarkan dari workspace`, target)
	case "invitation.created":
		return fmt.Sprintf(`Undangan workspace dibuat untuk %q`, workspaceAuditMetaString(meta, "email", target))
	case "invitation.cancelled":
		return fmt.Sprintf(`Undangan workspace %q dibatalkan`, workspaceAuditMetaString(meta, "email", target))
	case "invitation.accepted":
		return fmt.Sprintf(`Undangan workspace %q diterima -- akun aktif`, workspaceAuditMetaString(meta, "email", target))
	case "project_member.added":
		return fmt.Sprintf(`Member %q ditambahkan ke project`, target)
	case "project_member.role_changed":
		return fmt.Sprintf(`Role member project %q diubah`, target)
	case "project_member.removed":
		return fmt.Sprintf(`Member %q dikeluarkan dari project`, target)
	case "pic_group.updated":
		return fmt.Sprintf(`PIC Group status %q pada project %q diperbarui`,
			workspaceAuditMetaString(meta, "status_name", "tidak diketahui"), workspaceAuditMetaString(meta, "project_name", target))
	case "sprint.created":
		return fmt.Sprintf(`Sprint %q dibuat`, target)
	case "sprint.updated":
		return fmt.Sprintf(`Sprint %q diperbarui`, target)
	case "sprint.started":
		return fmt.Sprintf(`Sprint %q dimulai`, target)
	case "sprint.completed":
		return fmt.Sprintf(`Sprint %q ditutup`, target)
	case "sprint.reopened":
		return fmt.Sprintf(`Sprint %q dibuka kembali`, target)
	case "sprint.tasks_assigned":
		return fmt.Sprintf(`Task ditarik ke sprint %q`, target)
	case "sprint.deleted":
		return fmt.Sprintf(`Sprint %q dihapus`, target)
	case "project_import.created":
		return fmt.Sprintf(`Import %s %q: %s berhasil, %s dilewati`,
			workspaceAuditMetaString(meta, "kind", "data"), target,
			workspaceAuditMetaNumber(meta, "success_count"), workspaceAuditMetaNumber(meta, "failed_count"))
	case "project_import.report_downloaded":
		return fmt.Sprintf(`Laporan import %q diunduh`, target)
	case "project.pm_added":
		return fmt.Sprintf(`Project Manager ditambahkan ke project %q`, target)
	case "task.created":
		text := fmt.Sprintf(`Task %s dibuat`, workspaceAuditTaskLabel(e, meta))
		if imported, _ := workspaceAuditStateAfter(e)["imported"].(bool); imported {
			text += " (hasil import)"
		}
		return text
	case "task.updated":
		return workspaceAuditTaskUpdatedText(e, meta)
	case "task.status_changed":
		return fmt.Sprintf(`Status task %s diubah dari %s menjadi %s`, workspaceAuditTaskLabel(e, meta),
			workspaceAuditMetaString(workspaceAuditStateBefore(e), "status", "?"), workspaceAuditMetaString(workspaceAuditStateAfter(e), "status", "?"))
	case "task.completeness_changed":
		return fmt.Sprintf(`Kelengkapan task %s diubah dari %s menjadi %s`, workspaceAuditTaskLabel(e, meta),
			workspaceAuditCompletenessLabel(workspaceAuditMetaString(workspaceAuditStateBefore(e), "completeness", "")),
			workspaceAuditCompletenessLabel(workspaceAuditMetaString(workspaceAuditStateAfter(e), "completeness", "")))
	case "task.deleted":
		return fmt.Sprintf(`Task %s dihapus (masuk masa retensi)`, workspaceAuditTaskLabel(e, meta))
	case "task.dependency_added":
		return fmt.Sprintf(`Task %s kini menunggu task %s`, workspaceAuditTaskLabel(e, meta), workspaceAuditPredecessorLabel(e))
	case "task.dependency_removed":
		return fmt.Sprintf(`Dependency task %s pada task %s dihapus`, workspaceAuditTaskLabel(e, meta), workspaceAuditPredecessorLabel(e))
	case "task.pic_acknowledged":
		return fmt.Sprintf(`PIC mengonfirmasi serah terima task %s`, workspaceAuditTaskLabel(e, meta))
	case "task.pic_added":
		st := workspaceAuditStateAfter(e)
		return fmt.Sprintf(`PIC paralel %q ditambahkan pada fase %s task %s`, workspaceAuditMetaString(st, "user_name", "tidak diketahui"), workspaceAuditMetaString(st, "status_name", "?"), workspaceAuditTaskLabel(e, meta))
	case "task.pic_removed":
		st := workspaceAuditStateAfter(e)
		return fmt.Sprintf(`PIC %q dihapus dari fase %s task %s`, workspaceAuditMetaString(st, "user_name", "tidak diketahui"), workspaceAuditMetaString(st, "status_name", "?"), workspaceAuditTaskLabel(e, meta))
	case "task.pic_handoff":
		st := workspaceAuditStateAfter(e)
		return fmt.Sprintf(`PIC fase %s task %s diserahkan dari %s kepada %q`, workspaceAuditMetaString(st, "status_name", "?"), workspaceAuditTaskLabel(e, meta),
			workspaceAuditJoinNames(st["from_user_names"]), workspaceAuditMetaString(st, "to_user_name", "tidak diketahui"))
	case "task.time_entry_added":
		if workspaceAuditMetaString(workspaceAuditStateAfter(e), "approval_status", "") == "approved" {
			return fmt.Sprintf(`Waktu dicatat (%s menit) pada task %s, langsung disetujui`, workspaceAuditMetaNumber(workspaceAuditStateAfter(e), "duration_minutes"), workspaceAuditTaskLabel(e, meta))
		}
		return fmt.Sprintf(`Entri waktu manual (%s menit) ditambahkan pada task %s, menunggu persetujuan`, workspaceAuditMetaNumber(workspaceAuditStateAfter(e), "duration_minutes"), workspaceAuditTaskLabel(e, meta))
	case "task.time_entry_updated":
		return fmt.Sprintf(`Entri waktu manual task %s diperbarui dan dikirim ulang untuk persetujuan`, workspaceAuditTaskLabel(e, meta))
	case "task.time_entry_approved":
		return fmt.Sprintf(`Entri waktu task %s disetujui (%s menit)`, workspaceAuditTaskLabel(e, meta), workspaceAuditMetaNumber(workspaceAuditStateAfter(e), "duration_minutes"))
	case "task.time_entry_rejected":
		return fmt.Sprintf(`Entri waktu task %s ditolak: %s`, workspaceAuditTaskLabel(e, meta), workspaceAuditMetaString(workspaceAuditStateAfter(e), "rejection_note", "tanpa catatan"))
	case "comment.created":
		text := fmt.Sprintf(`Komentar ditambahkan pada task %s`, workspaceAuditTaskLabel(e, meta))
		if list, _ := workspaceAuditStateAfter(e)["mentions"].([]any); len(list) > 0 {
			text += fmt.Sprintf(` (mention: %s)`, workspaceAuditJoinNames(list))
		}
		return text
	case "comment.edited":
		return fmt.Sprintf(`Komentar pada task %s diedit dari %q menjadi %q`, workspaceAuditTaskLabel(e, meta),
			workspaceAuditShortText(workspaceAuditMetaString(workspaceAuditStateBefore(e), "text", "")), workspaceAuditShortText(workspaceAuditMetaString(workspaceAuditStateAfter(e), "text", "")))
	case "comment.deleted":
		return fmt.Sprintf(`Komentar pada task %s dihapus`, workspaceAuditTaskLabel(e, meta))
	case "audit.report_downloaded":
		return fmt.Sprintf(`Audit Trail %s %q diekspor (CSV, %s baris)`, workspaceAuditMetaString(workspaceAuditStateAfter(e), "scope", "workspace"), target, workspaceAuditMetaNumber(workspaceAuditStateAfter(e), "rows"))
	case "timesheet.estimates_recalculated":
		return fmt.Sprintf(`Estimasi jam tercatat project %q dihitung ulang dari riwayat tanggal status (%s entri, %s task)`, target, workspaceAuditMetaNumber(workspaceAuditStateAfter(e), "entries"), workspaceAuditMetaNumber(workspaceAuditStateAfter(e), "tasks"))
	case "timesheet.report_downloaded":
		return fmt.Sprintf(`Timesheet project %q diekspor (CSV, %s baris)`, target, workspaceAuditMetaNumber(meta, "rows"))
	case "user.login":
		return "Login berhasil"
	case "user.backup_code_used":
		return "Login menggunakan kode cadangan MFA"
	case "account.profile_updated":
		return "Profil akun sendiri diperbarui"
	case "account.password_changed":
		return "Password akun sendiri diganti"
	case "account.mfa_device_reset":
		return "MFA dipindahkan ke perangkat baru"
	case "account.mfa_backup_codes_regenerated":
		return "Kode pemulihan MFA dibuat ulang"
	case "account.notification_preferences_updated":
		return "Preferensi notifikasi akun sendiri diperbarui"
	default:
		return fmt.Sprintf("%s pada %s", e.Action, e.EntityType)
	}
}

func parseWorkspaceAuditMetadata(e *repository.WorkspaceAuditLogEntry) map[string]any {
	if len(e.Metadata) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(e.Metadata, &m); err != nil {
		return nil
	}
	return m
}

func workspaceAuditStateBefore(e *repository.WorkspaceAuditLogEntry) map[string]any {
	if len(e.StateBefore) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(e.StateBefore, &m); err != nil {
		return nil
	}
	return m
}

// workspaceAuditTaskLabel -- "KODE "Judul"" dari snapshot metadata.task_code +
// target (judul); baris lama tanpa snapshot jatuh ke judul saja / "tidak diketahui".
func workspaceAuditTaskLabel(e *repository.WorkspaceAuditLogEntry, meta map[string]any) string {
	title := workspaceAuditTargetOf(e)
	if code := workspaceAuditMetaString(meta, "task_code", ""); code != "" {
		return fmt.Sprintf("%s %q", code, title)
	}
	return fmt.Sprintf("%q", title)
}

func workspaceAuditPredecessorLabel(e *repository.WorkspaceAuditLogEntry) string {
	st := workspaceAuditStateAfter(e)
	title := workspaceAuditMetaString(st, "predecessor_title", "tidak diketahui")
	if code := workspaceAuditMetaString(st, "predecessor_code", ""); code != "" {
		return fmt.Sprintf("%s %q", code, title)
	}
	return fmt.Sprintf("%q", title)
}

func workspaceAuditCompletenessLabel(v string) string {
	switch v {
	case "complete":
		return "Lengkap"
	case "incomplete":
		return "Belum Lengkap"
	default:
		return "?"
	}
}

// workspaceAuditShortText -- potong teks komentar untuk narasi (80 karakter + "..."), sama dengan
// shortText di features/workspace-audit/narrative.ts.
func workspaceAuditShortText(s string) string {
	r := []rune(s)
	if len(r) <= 80 {
		return s
	}
	return string(r[:80]) + "..."
}

func workspaceAuditJoinNames(v any) string {
	list, _ := v.([]any)
	names := make([]string, 0, len(list))
	for _, n := range list {
		if s, ok := n.(string); ok && s != "" {
			names = append(names, s)
		}
	}
	if len(names) == 0 {
		return "tidak diketahui"
	}
	return strings.Join(names, ", ")
}

// workspaceAuditTaskUpdatedText -- "dari X menjadi Y" untuk judul/prioritas
// bila nilai sebelumnya tercatat (baris lama hanya punya nilai sesudah).
func workspaceAuditTaskUpdatedText(e *repository.WorkspaceAuditLogEntry, meta map[string]any) string {
	before, after := workspaceAuditStateBefore(e), workspaceAuditStateAfter(e)
	parts := []string{}
	if b, a := workspaceAuditMetaString(before, "title", ""), workspaceAuditMetaString(after, "title", ""); b != "" && a != "" && b != a {
		parts = append(parts, fmt.Sprintf("judul %q menjadi %q", b, a))
	}
	if b, a := workspaceAuditMetaString(before, "priority", ""), workspaceAuditMetaString(after, "priority", ""); b != "" && a != "" && b != a {
		parts = append(parts, fmt.Sprintf("prioritas %s menjadi %s", b, a))
	}
	if len(parts) == 0 {
		return fmt.Sprintf(`Task %s diperbarui`, workspaceAuditTaskLabel(e, meta))
	}
	return fmt.Sprintf(`Task %s diperbarui: %s`, workspaceAuditTaskLabel(e, meta), strings.Join(parts, ", "))
}

func workspaceAuditStateAfter(e *repository.WorkspaceAuditLogEntry) map[string]any {
	if len(e.StateAfter) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(e.StateAfter, &m); err != nil {
		return nil
	}
	return m
}

func workspaceAuditMetaString(meta map[string]any, key, fallback string) string {
	if v, ok := meta[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

func workspaceAuditTargetOf(e *repository.WorkspaceAuditLogEntry) string {
	if s := stringOrEmpty(e.TargetName); s != "" {
		return s
	}
	return "tidak diketahui"
}

// workspaceAuditMetaNumber -- angka metadata (JSON float64) sebagai string
// bulat; "0" kalau tidak ada.
func workspaceAuditMetaNumber(meta map[string]any, key string) string {
	if v, ok := meta[key].(float64); ok {
		return fmt.Sprintf("%.0f", v)
	}
	return "0"
}
