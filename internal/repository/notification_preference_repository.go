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

// ListForGroupAdmin mengembalikan GroupAdminNotificationEvents lengkap,
// disi dari notification_preferences kalau sudah pernah diatur, atau
// default skema kalau belum -- GET /users/me/notification-preferences
// selalu mengembalikan SEMUA 5 event (bukan cuma yang sudah ada baris-nya)
// supaya FE tidak perlu tahu perbedaan "belum diatur" vs "default".
func (r *NotificationPreferenceRepository) ListForGroupAdmin(ctx context.Context, userID string) ([]NotificationPreference, error) {
	rows, err := r.db.Query(ctx, `
		SELECT event_type, in_app, push, email FROM notification_preferences WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListForGroupAdmin: %w", err)
	}
	defer rows.Close()

	saved := make(map[string]NotificationPreference)
	for rows.Next() {
		var p NotificationPreference
		if err := rows.Scan(&p.EventType, &p.InApp, &p.Push, &p.Email); err != nil {
			return nil, fmt.Errorf("repository.ListForGroupAdmin: scan: %w", err)
		}
		saved[p.EventType] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListForGroupAdmin: %w", err)
	}

	result := make([]NotificationPreference, len(GroupAdminNotificationEvents))
	for i, ev := range GroupAdminNotificationEvents {
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
