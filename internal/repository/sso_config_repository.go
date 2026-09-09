// Package repository -- SSOConfigRepository (US-074, Track S4G S4G-23,
// desain freehand -- lihat sprint_backlog.md). Skema `sso_configs` PERSIS
// DATABASE_SCHEMA.md §5.8. `client_secret` dienkripsi via pgcrypto
// (pgp_sym_encrypt), pola PERSIS WebhookRepository.hmac_secret_encrypted --
// beda passphrase (SSO_ENCRYPTION_KEY, bukan dipakai bersama key lain).
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type SSOConfig struct {
	OrgID           string
	Configured      bool // TRUE kalau baris sso_configs sudah ada (bukan sekadar org kosong)
	Protocol        *string
	IdpEntityID     *string
	IdpMetadataURL  *string
	IdpMetadataXML  *string
	ClientID        *string
	HasClientSecret bool
	DiscoveryURL    *string
	IsTested        bool
	LastTestAt      *time.Time
	SSOEnabled      bool
	UpdatedAt       *time.Time
}

type SSOConfigRepository struct {
	encryptionKey string
}

// NewSSOConfigRepository mengembalikan error kalau encryptionKey kosong --
// pola sama NewWebhookRepository/NewMFARepository, pgp_sym_encrypt/decrypt
// DIAM-DIAM berhasil dengan passphrase kosong.
func NewSSOConfigRepository(encryptionKey string) (*SSOConfigRepository, error) {
	if encryptionKey == "" {
		return nil, fmt.Errorf("repository.NewSSOConfigRepository: SSO_ENCRYPTION_KEY wajib diisi")
	}
	return &SSOConfigRepository{encryptionKey: encryptionKey}, nil
}

// Get -- SELALU mengembalikan baris (LEFT JOIN), org tanpa konfigurasi SSO
// sama sekali dikembalikan dengan Configured=false + field lain nil/default,
// bukan 404 -- FE menampilkan form kosong, bukan halaman error.
func (r *SSOConfigRepository) Get(ctx context.Context, exec db.Executor, orgID string) (*SSOConfig, error) {
	var c SSOConfig
	c.OrgID = orgID
	err := exec.QueryRow(ctx, `
		SELECT o.sso_enabled, sc.protocol, sc.idp_entity_id, sc.idp_metadata_url, sc.idp_metadata_xml,
		       sc.client_id, (sc.client_secret IS NOT NULL), sc.discovery_url, COALESCE(sc.is_tested, FALSE),
		       sc.last_test_at, sc.updated_at
		FROM organizations o
		LEFT JOIN sso_configs sc ON sc.org_id = o.id
		WHERE o.id = $1
	`, orgID).Scan(&c.SSOEnabled, &c.Protocol, &c.IdpEntityID, &c.IdpMetadataURL, &c.IdpMetadataXML,
		&c.ClientID, &c.HasClientSecret, &c.DiscoveryURL, &c.IsTested, &c.LastTestAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.Get: %w", domain.ErrOrganizationNotFound)
		}
		return nil, fmt.Errorf("repository.Get: %w", err)
	}
	c.Configured = c.Protocol != nil
	return &c, nil
}

// Upsert -- clientSecret nil berarti "tidak diubah" (form dikosongkan
// sengaja untuk mempertahankan secret lama, pola sama field password
// opsional di form edit lain) -- COALESCE mempertahankan nilai terenkripsi
// lama pada UPDATE, dan tetap NULL pada INSERT pertama kali kalau memang
// belum pernah diisi. sso_enabled ditulis LANGSUNG ke organizations (bukan
// kolom baru di sso_configs) dalam transaksi yang sama.
func (r *SSOConfigRepository) Upsert(ctx context.Context, exec db.Executor, orgID, protocol string, idpEntityID, idpMetadataURL, idpMetadataXML, clientID, clientSecret, discoveryURL *string, ssoEnabled bool) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO sso_configs (org_id, protocol, idp_entity_id, idp_metadata_url, idp_metadata_xml, client_id, client_secret, discovery_url)
		VALUES ($1, $2, $3, $4, $5, $6,
		        CASE WHEN $8::text IS NULL THEN NULL ELSE encode(pgp_sym_encrypt($8, $9), 'base64') END,
		        $7)
		ON CONFLICT (org_id) DO UPDATE SET
			protocol = EXCLUDED.protocol,
			idp_entity_id = EXCLUDED.idp_entity_id,
			idp_metadata_url = EXCLUDED.idp_metadata_url,
			idp_metadata_xml = EXCLUDED.idp_metadata_xml,
			client_id = EXCLUDED.client_id,
			client_secret = COALESCE(EXCLUDED.client_secret, sso_configs.client_secret),
			discovery_url = EXCLUDED.discovery_url,
			updated_at = NOW()
	`, orgID, protocol, idpEntityID, idpMetadataURL, idpMetadataXML, clientID, discoveryURL, clientSecret, r.encryptionKey)
	if err != nil {
		return fmt.Errorf("repository.Upsert: %w", err)
	}
	if _, err := exec.Exec(ctx, `UPDATE organizations SET sso_enabled = $2, updated_at = NOW() WHERE id = $1`, orgID, ssoEnabled); err != nil {
		return fmt.Errorf("repository.Upsert: sso_enabled: %w", err)
	}
	return nil
}
