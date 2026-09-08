// Package repository -- WebhookRepository (Webhook, Track S4G, desain
// "GA Webhook.dc.html" + "GA Add Webhook.dc.html"). Tabel `webhook_configs`
// + `webhook_deliveries` (migrasi 20260920090000) -- lihat komentar migrasi
// untuk penyimpangan dari DATABASE_SCHEMA.md (org_id nullable, events
// dibatasi 3 nilai). hmac_secret_encrypted dienkripsi via pgcrypto
// (pgp_sym_encrypt), pola PERSIS MFARepository.totp_secret.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type Webhook struct {
	ID          string
	GroupID     string
	OrgID       *string
	OrgName     *string
	Name        string
	TargetURL   string
	Events      []string
	IsActive    bool
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Sent30d     int
	Failed30d   int
	LastEventAt *time.Time
}

type WebhookDelivery struct {
	ID            string
	WebhookID     string
	WebhookName   string
	EventType     string
	Payload       json.RawMessage
	AttemptNumber int
	Status        string
	HTTPStatus    *int
	ResponseBody  *string
	ErrorMessage  *string
	DurationMs    *int
	CreatedAt     time.Time
}

type WebhookRepository struct {
	encryptionKey string
}

// NewWebhookRepository mengembalikan error kalau encryptionKey kosong --
// pola sama NewMFARepository, pgp_sym_encrypt/decrypt DIAM-DIAM berhasil
// dengan passphrase kosong.
func NewWebhookRepository(encryptionKey string) (*WebhookRepository, error) {
	if encryptionKey == "" {
		return nil, fmt.Errorf("repository.NewWebhookRepository: WEBHOOK_ENCRYPTION_KEY wajib diisi")
	}
	return &WebhookRepository{encryptionKey: encryptionKey}, nil
}

// Create menyimpan webhook baru + secret plaintext terenkripsi. secret
// plaintext HANYA dikembalikan sekali oleh service (ditampilkan di modal),
// tidak pernah disimpan di luar bentuk terenkripsi ini.
func (r *WebhookRepository) Create(ctx context.Context, exec db.Executor, groupID string, orgID *string, name, targetURL, secret string, events []string, actorID, actorRole string) (string, error) {
	var id string
	err := exec.QueryRow(ctx, `
		INSERT INTO webhook_configs (group_id, org_id, name, target_url, hmac_secret_encrypted, events, created_by)
		VALUES ($1, $2, $3, $4, encode(pgp_sym_encrypt($5, $6), 'base64'), $7, $8)
		RETURNING id
	`, groupID, orgID, name, targetURL, secret, r.encryptionKey, events, actorID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("repository.Create: %w", err)
	}
	if err := insertWebhookAudit(ctx, exec, actorID, actorRole, "webhook.created", id, orgID, groupID, nil, nil); err != nil {
		return "", fmt.Errorf("repository.Create: %w", err)
	}
	return id, nil
}

// Get mengembalikan satu webhook TANPA statistik 30 hari (dipakai
// Update/Delete/ToggleActive/RegenerateSecret/Test, cuma butuh baris
// intinya) -- RLS yang menyaring kepemilikan grup, "tidak ada baris" berarti
// ErrWebhookNotFound baik karena benar tidak ada atau karena bukan milik
// grup actor (tidak membocorkan mana).
func (r *WebhookRepository) Get(ctx context.Context, exec db.Executor, webhookID string) (*Webhook, error) {
	var w Webhook
	err := exec.QueryRow(ctx, `
		SELECT id, group_id, org_id, name, target_url, events, is_active, created_by, created_at, updated_at
		FROM webhook_configs WHERE id = $1
	`, webhookID).Scan(&w.ID, &w.GroupID, &w.OrgID, &w.Name, &w.TargetURL, &w.Events, &w.IsActive, &w.CreatedBy, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.Get: %w", domain.ErrWebhookNotFound)
		}
		return nil, fmt.Errorf("repository.Get: %w", err)
	}
	return &w, nil
}

// ListByGroup -- daftar webhook grup + statistik 30 hari (stats bar & kolom
// SUKSES di "GA Webhook.dc.html") lewat agregasi LEFT JOIN webhook_deliveries,
// bukan kolom counter -- menghindari counter drift, cukup sekali query per
// tampilan grid (grid ini tidak dipanggil dengan frekuensi tinggi).
func (r *WebhookRepository) ListByGroup(ctx context.Context, exec db.Executor, groupID string) ([]Webhook, error) {
	rows, err := exec.Query(ctx, `
		SELECT wc.id, wc.group_id, wc.org_id, o.name, wc.name, wc.target_url, wc.events, wc.is_active,
		       wc.created_by, wc.created_at, wc.updated_at,
		       COUNT(*) FILTER (WHERE d.status = 'delivered' AND d.created_at > NOW() - INTERVAL '30 days'),
		       COUNT(*) FILTER (WHERE d.status = 'failed' AND d.created_at > NOW() - INTERVAL '30 days'),
		       MAX(d.created_at)
		FROM webhook_configs wc
		LEFT JOIN organizations o ON o.id = wc.org_id
		LEFT JOIN webhook_deliveries d ON d.webhook_id = wc.id
		WHERE wc.group_id = $1
		GROUP BY wc.id, o.name
		ORDER BY wc.created_at DESC
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListByGroup: %w", err)
	}
	defer rows.Close()

	list := make([]Webhook, 0)
	for rows.Next() {
		var w Webhook
		if err := rows.Scan(&w.ID, &w.GroupID, &w.OrgID, &w.OrgName, &w.Name, &w.TargetURL, &w.Events, &w.IsActive,
			&w.CreatedBy, &w.CreatedAt, &w.UpdatedAt, &w.Sent30d, &w.Failed30d, &w.LastEventAt); err != nil {
			return nil, fmt.Errorf("repository.ListByGroup: scan: %w", err)
		}
		list = append(list, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListByGroup: %w", err)
	}
	return list, nil
}

// ListActiveForEvent -- webhook aktif grup yang berlangganan eventType,
// dicocokkan cakupan (org_id NULL = seluruh grup, atau org_id = orgID
// persis) -- dipanggil WebhookService.Dispatch tiap mutasi project.
func (r *WebhookRepository) ListActiveForEvent(ctx context.Context, exec db.Executor, groupID, orgID, eventType string) ([]Webhook, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, group_id, org_id, name, target_url, events, is_active, created_by, created_at, updated_at
		FROM webhook_configs
		WHERE group_id = $1 AND is_active = TRUE AND (org_id IS NULL OR org_id = $2) AND $3 = ANY(events)
	`, groupID, orgID, eventType)
	if err != nil {
		return nil, fmt.Errorf("repository.ListActiveForEvent: %w", err)
	}
	defer rows.Close()

	list := make([]Webhook, 0)
	for rows.Next() {
		var w Webhook
		if err := rows.Scan(&w.ID, &w.GroupID, &w.OrgID, &w.Name, &w.TargetURL, &w.Events, &w.IsActive, &w.CreatedBy, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("repository.ListActiveForEvent: scan: %w", err)
		}
		list = append(list, w)
	}
	return list, rows.Err()
}

// DecryptSecret -- HANYA dipanggil job pengiriman (worker) dan handler Test,
// keduanya butuh plaintext untuk menandatangani payload HMAC-SHA256.
func (r *WebhookRepository) DecryptSecret(ctx context.Context, exec db.Executor, webhookID string) (string, error) {
	var secret string
	err := exec.QueryRow(ctx, `
		SELECT pgp_sym_decrypt(decode(hmac_secret_encrypted, 'base64'), $2)
		FROM webhook_configs WHERE id = $1
	`, webhookID, r.encryptionKey).Scan(&secret)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("repository.DecryptSecret: %w", domain.ErrWebhookNotFound)
		}
		return "", fmt.Errorf("repository.DecryptSecret: %w", err)
	}
	return secret, nil
}

// Update menyimpan before/after (url + jumlah event) ke audit trail, sama
// pola before/after di store.updateWebhook desain.
func (r *WebhookRepository) Update(ctx context.Context, exec db.Executor, webhookID, name, targetURL string, orgID *string, events []string, actorID, actorRole string, before *Webhook) error {
	tag, err := exec.Exec(ctx, `
		UPDATE webhook_configs SET name = $2, target_url = $3, org_id = $4, events = $5, updated_at = NOW()
		WHERE id = $1
	`, webhookID, name, targetURL, orgID, events)
	if err != nil {
		return fmt.Errorf("repository.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Update: %w", domain.ErrWebhookNotFound)
	}
	stateBefore := map[string]any{"target_url": before.TargetURL, "event_count": len(before.Events)}
	stateAfter := map[string]any{"target_url": targetURL, "event_count": len(events)}
	groupID := before.GroupID
	if err := insertWebhookAudit(ctx, exec, actorID, actorRole, "webhook.updated", webhookID, orgID, groupID, stateBefore, stateAfter); err != nil {
		return fmt.Errorf("repository.Update: %w", err)
	}
	return nil
}

func (r *WebhookRepository) SetActive(ctx context.Context, exec db.Executor, webhookID string, active bool, actorID, actorRole string, before *Webhook) error {
	tag, err := exec.Exec(ctx, `UPDATE webhook_configs SET is_active = $2, updated_at = NOW() WHERE id = $1`, webhookID, active)
	if err != nil {
		return fmt.Errorf("repository.SetActive: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetActive: %w", domain.ErrWebhookNotFound)
	}
	action := "webhook.activated"
	if !active {
		action = "webhook.deactivated"
	}
	if err := insertWebhookAudit(ctx, exec, actorID, actorRole, action, webhookID, before.OrgID, before.GroupID, nil, nil); err != nil {
		return fmt.Errorf("repository.SetActive: %w", err)
	}
	return nil
}

func (r *WebhookRepository) RegenerateSecret(ctx context.Context, exec db.Executor, webhookID, newSecret, actorID, actorRole string, before *Webhook) error {
	tag, err := exec.Exec(ctx, `
		UPDATE webhook_configs SET hmac_secret_encrypted = encode(pgp_sym_encrypt($2, $3), 'base64'), updated_at = NOW()
		WHERE id = $1
	`, webhookID, newSecret, r.encryptionKey)
	if err != nil {
		return fmt.Errorf("repository.RegenerateSecret: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.RegenerateSecret: %w", domain.ErrWebhookNotFound)
	}
	if err := insertWebhookAudit(ctx, exec, actorID, actorRole, "webhook.secret_regenerated", webhookID, before.OrgID, before.GroupID, nil, nil); err != nil {
		return fmt.Errorf("repository.RegenerateSecret: %w", err)
	}
	return nil
}

func (r *WebhookRepository) Delete(ctx context.Context, exec db.Executor, webhookID, actorID, actorRole string, before *Webhook) error {
	tag, err := exec.Exec(ctx, `DELETE FROM webhook_configs WHERE id = $1`, webhookID)
	if err != nil {
		return fmt.Errorf("repository.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Delete: %w", domain.ErrWebhookNotFound)
	}
	if err := insertWebhookAudit(ctx, exec, actorID, actorRole, "webhook.deleted", webhookID, before.OrgID, before.GroupID, nil, nil); err != nil {
		return fmt.Errorf("repository.Delete: %w", err)
	}
	return nil
}

// insertWebhookAudit -- reuse writeAuditLog (chokepoint audit_logs/
// platform_audit_logs, account_repository.go) supaya actor_ip+request_path
// ikut tercatat otomatis dari context. Selalu ke audit_logs (BUKAN
// platform_audit_logs) walau actor platform_admin -- pola sama
// insertOrgAudit/insertWorkspaceAudit, entitas ini milik grup/organisasi,
// bukan domain khusus konsol Platform Admin. group_id disertakan di metadata
// (bukan kolom audit_logs -- tabel itu tidak punya kolom group_id) supaya
// webhook cakupan "seluruh grup" (org_id NULL) tetap bisa ditemukan nanti.
func insertWebhookAudit(ctx context.Context, exec execer, actorID, actorRole, action, webhookID string, orgID *string, groupID string, stateBefore, stateAfter map[string]any) error {
	return writeAuditLog(ctx, exec, "audit_logs", actorID, actorRole, action, "webhook", &webhookID,
		map[string]any{"group_id": groupID, "org_id": orgID}, stateBefore, stateAfter)
}

// CreateDelivery -- satu baris PER PERCOBAAN, lihat komentar migrasi.
func (r *WebhookRepository) CreateDelivery(ctx context.Context, exec db.Executor, webhookID, eventType string, payload []byte, attemptNumber int, status string, httpStatus *int, responseBody, errMessage *string, durationMs int) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO webhook_deliveries (webhook_id, event_type, payload, attempt_number, status, http_status, response_body, error_message, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, webhookID, eventType, payload, attemptNumber, status, httpStatus, responseBody, errMessage, durationMs)
	if err != nil {
		return fmt.Errorf("repository.CreateDelivery: %w", err)
	}
	return nil
}

// ListDeliveries -- tab "Log Pengiriman", filter opsional status/webhookID
// ("" berarti tidak difilter), retensi 30 hari sesuai desain.
func (r *WebhookRepository) ListDeliveries(ctx context.Context, exec db.Executor, groupID, statusFilter, webhookIDFilter string) ([]WebhookDelivery, error) {
	rows, err := exec.Query(ctx, `
		SELECT d.id, d.webhook_id, wc.name, d.event_type, d.payload, d.attempt_number, d.status,
		       d.http_status, d.response_body, d.error_message, d.duration_ms, d.created_at
		FROM webhook_deliveries d
		JOIN webhook_configs wc ON wc.id = d.webhook_id
		WHERE wc.group_id = $1
		  AND d.created_at > NOW() - INTERVAL '30 days'
		  AND ($2 = '' OR d.status = $2)
		  AND ($3 = '' OR d.webhook_id::text = $3)
		ORDER BY d.created_at DESC
		LIMIT 500
	`, groupID, statusFilter, webhookIDFilter)
	if err != nil {
		return nil, fmt.Errorf("repository.ListDeliveries: %w", err)
	}
	defer rows.Close()

	list := make([]WebhookDelivery, 0)
	for rows.Next() {
		var d WebhookDelivery
		if err := rows.Scan(&d.ID, &d.WebhookID, &d.WebhookName, &d.EventType, &d.Payload, &d.AttemptNumber, &d.Status,
			&d.HTTPStatus, &d.ResponseBody, &d.ErrorMessage, &d.DurationMs, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("repository.ListDeliveries: scan: %w", err)
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

// GroupAdminContact -- satu Group Admin pengelola grup (email+nama), dipakai
// notifikasi kegagalan retry habis.
type GroupAdminContact struct {
	ID, Email, Name string
}

// GroupAdminContacts -- seluruh Group Admin pengelola grup -- pola sama
// RetentionNotifyHandler.
func (r *WebhookRepository) GroupAdminContacts(ctx context.Context, exec db.Executor, groupID string) ([]GroupAdminContact, error) {
	rows, err := exec.Query(ctx, `
		SELECT u.id, u.email, u.display_name
		FROM group_admin_assignments gaa
		JOIN users u ON u.id = gaa.user_id
		WHERE gaa.group_id = $1
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("repository.GroupAdminContacts: %w", err)
	}
	defer rows.Close()

	var list []GroupAdminContact
	for rows.Next() {
		var a GroupAdminContact
		if err := rows.Scan(&a.ID, &a.Email, &a.Name); err != nil {
			return nil, fmt.Errorf("repository.GroupAdminContacts: scan: %w", err)
		}
		list = append(list, a)
	}
	return list, rows.Err()
}
