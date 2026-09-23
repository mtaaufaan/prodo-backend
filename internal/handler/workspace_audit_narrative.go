package handler

import (
	"encoding/json"
	"fmt"

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
