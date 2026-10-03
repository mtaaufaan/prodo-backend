// Package repository -- NotificationPreferenceRepository (GA Pengaturan
// Akun, tab Notifikasi, desain "GA Pengaturan Akun.dc.html"). Tabel
// users-scoped (docs/DATABASE_SCHEMA.md §5.35), tidak di-RLS, sama kelas
// dengan user_sessions/user_mfa_configs.
package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NotificationEventType -- daftar event yang relevan untuk Group Admin
// (desain NOTIF_DEFS "GA Pengaturan Akun.dc.html": kuota/webhook/retensi/
// import/keamanan). BUKAN daftar lengkap seluruh event_type yang mungkin
// dipakai sistem notifikasi in-app (skema §5.35 memang generik per-event,
// event lain seperti 'task.mentioned' relevan untuk role workspace, bukan
// GA) -- cukup 5 ini untuk tab Notifikasi GA.
type NotificationEventType struct {
	Key   string
	Label string
	Note  string
}

var GroupAdminNotificationEvents = []NotificationEventType{
	{Key: "org.storage_quota_threshold", Label: "Ambang kuota storage organisasi", Note: "Peringatan 80% dan kritis 95% per organisasi"},
	{Key: "webhook.delivery_failed", Label: "Kegagalan webhook", Note: "Seluruh 3 retry gagal dalam 30 menit"},
	{Key: "retention.deletion_scheduled", Label: "Jadwal penghapusan data", Note: "Peringatan H-60 dan pengingat final H-80"},
	{Key: "csv_import.completed", Label: "Hasil import CSV", Note: "Ringkasan baris berhasil dan dilewati"},
	{Key: "account.security_activity", Label: "Aktivitas keamanan akun", Note: "Login perangkat baru, ganti password, reset MFA"},
}

// WorkspaceNotificationEvents -- daftar jenis notifikasi untuk role workspace
// (Admin Workspace/PM/Editor/Approver/Viewer), desain "User Pengaturan
// Akun.dc.html" (NOTIF_DEFS). `account.security_activity` SENGAJA sama dengan
// milik Group Admin -- satu preferensi tersimpan per (user, event_type).
var WorkspaceNotificationEvents = []NotificationEventType{
	{Key: "comment.mention", Label: "Mention pada komentar", Note: "Saat nama Anda di-tag @; tunduk pada cooldown mention workspace"},
	{Key: "task.pic_assigned", Label: "Penunjukan PIC & permintaan acknowledge", Note: "Saat Anda dipilih sebagai PIC fase berikutnya"},
	{Key: "task.assigned", Label: "Task ditugaskan ke saya", Note: "Assignee baru atau perubahan assignee pada task Anda"},
	{Key: "task.due_date", Label: "Due date & keterlambatan", Note: "Pengingat H-1 dan saat task melewati due date"},
	{Key: "approval.pending", Label: "Antrean approval", Note: "Task masuk ke tahap yang menunggu keputusan Anda"},
	{Key: "account.security_activity", Label: "Aktivitas keamanan akun", Note: "Login perangkat baru, ganti password, perubahan MFA"},
}

const (
	NotificationScopeGroup     = "group"
	NotificationScopeWorkspace = "workspace"
)

// NotificationEventsForScope -- ok=false untuk scope yang tidak dikenal.
func NotificationEventsForScope(scope string) (events []NotificationEventType, ok bool) {
	switch scope {
	case NotificationScopeGroup:
		return GroupAdminNotificationEvents, true
	case NotificationScopeWorkspace:
		return WorkspaceNotificationEvents, true
	}
	return nil, false
}

// IsKnownNotificationEvent -- event_type valid kalau ada di salah satu daftar.
func IsKnownNotificationEvent(key string) bool {
	for _, list := range [][]NotificationEventType{GroupAdminNotificationEvents, WorkspaceNotificationEvents} {
		for _, ev := range list {
			if ev.Key == key {
				return true
			}
		}
	}
	return false
}

// NotificationPreference adalah satu baris hasil gabungan preferensi
// tersimpan + default (§5.35: "Jika tidak ada record ... in_app=TRUE,
// push=TRUE, email=FALSE").
type NotificationPreference struct {
	EventType string
	InApp     bool
	Push      bool
	Email     bool
}

type NotificationPreferenceRepository struct {
	db *pgxpool.Pool
}

func NewNotificationPreferenceRepository(db *pgxpool.Pool) *NotificationPreferenceRepository {
	return &NotificationPreferenceRepository{db: db}
}

// List mengembalikan SEMUA `events` (daftar milik scope pemanggil), diisi dari
// notification_preferences kalau sudah pernah diatur, atau default skema kalau
// belum -- GET /users/me/notification-preferences selalu mengembalikan semua
// event (bukan cuma yang sudah ada baris-nya) supaya FE tidak perlu tahu
// perbedaan "belum diatur" vs "default".
func (r *NotificationPreferenceRepository) List(ctx context.Context, userID string, events []NotificationEventType) ([]NotificationPreference, error) {
	rows, err := r.db.Query(ctx, `
		SELECT event_type, in_app, push, email FROM notification_preferences WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("repository.List: %w", err)
	}
	defer rows.Close()

	saved := make(map[string]NotificationPreference)
	for rows.Next() {
		var p NotificationPreference
		if err := rows.Scan(&p.EventType, &p.InApp, &p.Push, &p.Email); err != nil {
			return nil, fmt.Errorf("repository.List: scan: %w", err)
		}
		saved[p.EventType] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.List: %w", err)
	}

	result := make([]NotificationPreference, len(events))
	for i, ev := range events {
		if p, ok := saved[ev.Key]; ok {
			result[i] = p
			continue
		}
		result[i] = NotificationPreference{EventType: ev.Key, InApp: true, Push: true, Email: false}
	}
	return result, nil
}

// Upsert menyimpan satu channel untuk satu event_type -- PATCH
// /users/me/notification-preferences mengganti SATU channel per klik (pola
// desain, bukan submit seluruh form sekaligus), jadi baris lain (kalau
// sudah ada) tidak boleh ikut ter-reset ke default. INSERT ... ON CONFLICT
// SET hanya kolom yang relevan tidak cukup (nilai channel lain yang belum
// pernah tersimpan perlu default eksplisit) -- caller (ProfileService)
// selalu mengirim ketiga channel (in_app/push/email) hasil merge dengan
// state saat ini, bukan partial update di level SQL.
func (r *NotificationPreferenceRepository) Upsert(ctx context.Context, userID, eventType string, inApp, push, email bool) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO notification_preferences (user_id, event_type, in_app, push, email)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, event_type) DO UPDATE
		SET in_app = EXCLUDED.in_app, push = EXCLUDED.push, email = EXCLUDED.email, updated_at = NOW()
	`, userID, eventType, inApp, push, email)
	if err != nil {
		return fmt.Errorf("repository.Upsert: %w", err)
	}
	return nil
}
