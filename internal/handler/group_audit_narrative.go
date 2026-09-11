package handler

import (
	"encoding/json"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// groupAuditNarrativeText -- port Go dari formatGroupAuditNarrative
// (features/group-audit/narrative.ts), KHUSUS dipakai
// GroupAuditHandler.writeCSV (2026-09-11, dikonfirmasi user). Grid utama GA
// Audit Trail (FE) tetap pakai versi TS -- ekspor CSV TIDAK BISA reuse data
// yang sudah dimuat browser karena grid dibatasi 200 baris per fetch
// (GroupAuditService.List) sedangkan ekspor sengaja TANPA batas
// (GroupAuditService.ExportCSV/CSVExportLimit, retensi 3 tahun bisa jauh
// lebih dari 200 baris) -- CSV writer di sini WAJIB backend, jadi
// narasinya juga harus ada di backend.
//
// PENTING untuk pengembangan selanjutnya: setiap kali menambah kasus BARU
// di features/group-audit/narrative.ts, tambahkan JUGA kasusnya di sini --
// action yang tidak dikenali jatuh ke fallback generik (lihat default
// case, sama persis fallback versi TS), TIDAK error, supaya lupa
// memperbarui salah satu tidak pernah membuat ekspor CSV rusak, cuma
// kurang komunikatif untuk action itu.
//
// Cuma mengembalikan kalimat ("text" versi TS) -- "scope" (kategori ·
// organisasi) sengaja TIDAK diikutkan, CSV sudah py kolom "organization"
// sendiri jadi info itu tidak hilang, cuma tidak diulang di kolom action.
func groupAuditNarrativeText(e *repository.GroupAuditLogEntry) string {
	meta := parseGroupAuditMetadata(e)
	target := groupAuditTargetOf(e)

	switch e.Action {
	case "organization.created":
		return fmt.Sprintf(`Organisasi %q dibuat`, target)
	case "organization.updated":
		return fmt.Sprintf(`Organisasi %q diperbarui`, target)
	case "organization.settings_updated":
		return fmt.Sprintf(`Pengaturan organisasi %q diperbarui`, target)
	case "organization.storage_quota_updated":
		return fmt.Sprintf(`Kuota atau retensi organisasi %q diperbarui`, target)
	case "organization.deactivated":
		return fmt.Sprintf(`Organisasi %q dinonaktifkan`, target)
	case "organization.reactivated":
		return fmt.Sprintf(`Organisasi %q diaktifkan kembali`, target)
	case "organization.deleted":
		return fmt.Sprintf(`Organisasi %q dihapus`, target)
	case "workspace.created":
		return fmt.Sprintf(`Workspace %q dibuat`, target)
	case "workspace.updated":
		return fmt.Sprintf(`Workspace %q diperbarui`, target)
	case "workspace.moved":
		return fmt.Sprintf(`Workspace %q dipindahkan ke organisasi lain`, target)
	case "workspace.deleted":
		return fmt.Sprintf(`Workspace %q dihapus`, target)
	case "workspace.restored":
		return fmt.Sprintf(`Workspace %q dipulihkan`, target)
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
	case "group.locale_updated":
		return "Format regional grup (tanggal/waktu/zona waktu/angka) diperbarui"
	case "organization.domain_added":
		return fmt.Sprintf(`Domain email %q ditambahkan ke organisasi %q`, groupAuditMetaString(meta, "domain"), target)
	case "organization.domain_removed":
		return fmt.Sprintf(`Domain email %q dihapus dari organisasi %q`, groupAuditMetaString(meta, "domain"), target)
	case "invitation.created":
		if groupAuditIsExecutiveInvite(meta) {
			return fmt.Sprintf(`Undangan Eksekutif dibuat untuk %q`, groupAuditMetaString(meta, "email"))
		}
		return fmt.Sprintf(`Undangan workspace dibuat untuk %q`, groupAuditMetaString(meta, "email"))
	case "invitation.cancelled":
		if groupAuditIsExecutiveInvite(meta) {
			return fmt.Sprintf(`Undangan Eksekutif %q dibatalkan`, groupAuditMetaString(meta, "email"))
		}
		return fmt.Sprintf(`Undangan workspace %q dibatalkan`, groupAuditMetaString(meta, "email"))
	case "invitation.accepted":
		if groupAuditIsExecutiveInvite(meta) {
			return fmt.Sprintf(`Undangan Eksekutif %q diterima -- akun aktif`, groupAuditMetaString(meta, "email"))
		}
		return fmt.Sprintf(`Undangan workspace %q diterima -- akun aktif`, groupAuditMetaString(meta, "email"))
	case "invitation.identity_updated":
		return fmt.Sprintf(`Nama/Jabatan Eksekutif %q diperbarui sebelum aktivasi`, groupAuditMetaString(meta, "email"))
	case "user.login":
		return fmt.Sprintf("Login %s berhasil", groupAuditRoleLabel(e))
	case "user.backup_code_used":
		return fmt.Sprintf("Login %s menggunakan kode cadangan MFA", groupAuditRoleLabel(e))
	case "account.profile_updated":
		return fmt.Sprintf("%s memperbarui profil akun sendiri", groupAuditRoleLabel(e))
	case "account.password_changed":
		return fmt.Sprintf("%s mengganti password akun sendiri", groupAuditRoleLabel(e))
	case "account.mfa_device_reset":
		return fmt.Sprintf("%s memindahkan MFA ke perangkat baru", groupAuditRoleLabel(e))
	case "account.mfa_backup_codes_regenerated":
		return fmt.Sprintf("%s membuat ulang kode pemulihan MFA", groupAuditRoleLabel(e))
	case "account.notification_preferences_updated":
		return fmt.Sprintf("%s memperbarui preferensi notifikasi akun sendiri", groupAuditRoleLabel(e))
	default:
		return fmt.Sprintf("%s pada %s", e.Action, e.EntityType)
	}
}

func parseGroupAuditMetadata(e *repository.GroupAuditLogEntry) map[string]any {
	if len(e.Metadata) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(e.Metadata, &m); err != nil {
		return nil
	}
	return m
}

func groupAuditMetaString(meta map[string]any, key string) string {
	if v, ok := meta[key].(string); ok && v != "" {
		return v
	}
	return "tidak diketahui"
}

func groupAuditIsExecutiveInvite(meta map[string]any) bool {
	v, _ := meta["is_executive_invite"].(bool)
	return v
}

func groupAuditTargetOf(e *repository.GroupAuditLogEntry) string {
	if s := stringOrEmpty(e.TargetName); s != "" {
		return s
	}
	if s := stringOrEmpty(e.OrgName); s != "" {
		return s
	}
	return "tidak diketahui"
}

func groupAuditRoleLabel(e *repository.GroupAuditLogEntry) string {
	switch stringOrEmpty(e.ActorRole) {
	case "group_admin":
		return "Group Admin"
	case "executive":
		return "Eksekutif"
	case "member":
		return "Member"
	default:
		return "Pengguna"
	}
}
