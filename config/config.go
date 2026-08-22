package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Config menyimpan seluruh konfigurasi aplikasi yang dibaca dari environment variables.
// Semua nilai sensitif (password, secret, token) WAJIB disuplai via env — tidak ada default.
// Nilai default hanya diperbolehkan untuk konfigurasi non-sensitif seperti port dan timeout.
//
// Jika VAULT_ADDR dan VAULT_TOKEN di-set, Load() akan mengambil secret dari OpenBao
// (path /secret/prodo/*) dan mengisi field database/redis/minio dari Vault.
// Jika tidak di-set, Load() fallback ke environment variables biasa (.env.local).
type Config struct {
	// Server
	AppEnv          string        `env:"APP_ENV" envDefault:"development"`
	ServerPort      int           `env:"SERVER_PORT" envDefault:"3001"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"30s"`

	// Database (PostgreSQL via pgx)
	DatabaseURL string `env:"DATABASE_URL" envDefault:""`
	// AppDatabaseURL -- koneksi runtime API server (S2-10, RLS_DESIGN.md
	// §2). WAJIB connect sebagai prodo_app_user (non-superuser, kena RLS),
	// BUKAN prodo (superuser dari DATABASE_URL) yang dipakai migrate
	// CLI/seed -- superuser selalu bypass RLS apapun policy-nya.
	AppDatabaseURL string        `env:"APP_DATABASE_URL" envDefault:""`
	DBMaxConns     int32         `env:"DB_MAX_CONNS" envDefault:"25"`
	DBMinConns    int32         `env:"DB_MIN_CONNS" envDefault:"5"`
	DBMaxConnLife time.Duration `env:"DB_MAX_CONN_LIFETIME" envDefault:"1h"`
	DBMaxConnIdle time.Duration `env:"DB_MAX_CONN_IDLE_TIME" envDefault:"30m"`

	// Redis
	RedisURL string `env:"REDIS_URL" envDefault:""`

	// Keycloak (OIDC)
	KeycloakIssuer   string `env:"KEYCLOAK_ISSUER" envDefault:""` // e.g. http://localhost:8080/realms/PRODO
	KeycloakAudience string `env:"KEYCLOAK_AUDIENCE" envDefault:"prodo-backend"`

	// KeycloakWebClientID -- client public (tanpa secret) dipakai backend
	// untuk Direct Access Grant (ROPC) atas nama form login web (S1-14).
	// Bukan secret, aman default hardcode.
	KeycloakWebClientID string `env:"KEYCLOAK_WEB_CLIENT_ID" envDefault:"prodo-web"`

	// Keycloak Admin REST API (service account client_credentials) --
	// dipakai internal/keycloak untuk provisioning user (S1-03). Client
	// "prodo-backend-admin" di infra/keycloak/realm-PRODO.json, lihat
	// docs/s1-h1-infra-runbook.md §Update.
	KeycloakAdminClientID     string `env:"KEYCLOAK_ADMIN_CLIENT_ID" envDefault:""`
	KeycloakAdminClientSecret string `env:"KEYCLOAK_ADMIN_CLIENT_SECRET" envDefault:""`

	// SMTP (Mailpit di dev, S1-04) -- tanpa auth untuk Mailpit; SMTPUser/Pass
	// kosong berarti kirim tanpa autentikasi.
	SMTPHost string `env:"SMTP_HOST" envDefault:"localhost"`
	SMTPPort int    `env:"SMTP_PORT" envDefault:"1025"`
	SMTPFrom string `env:"SMTP_FROM" envDefault:"noreply@prodo.local"`
	SMTPUser string `env:"SMTP_USER" envDefault:""`
	SMTPPass string `env:"SMTP_PASS" envDefault:""`

	// AppBaseURL -- origin frontend, dipakai untuk menyusun activation link
	// di email (S1-04). Beda konsep dari CORSAllowOrigins (yang bisa berisi
	// banyak origin dipisah koma untuk keperluan CORS).
	AppBaseURL string `env:"APP_BASE_URL" envDefault:"http://localhost:5173"`

	// MFAEncryptionKey -- passphrase pgcrypto untuk enkripsi totp_secret
	// (S1-06), lihat docs/DATABASE_SCHEMA.md §5.4.
	MFAEncryptionKey string `env:"MFA_ENCRYPTION_KEY" envDefault:""`

	// MinIO (Object Storage)
	MinIOEndpoint  string `env:"MINIO_ENDPOINT" envDefault:""`
	MinIOAccessKey string `env:"MINIO_ACCESS_KEY" envDefault:""`
	MinIOSecretKey string `env:"MINIO_SECRET_KEY" envDefault:""`
	MinIOBucket    string `env:"MINIO_BUCKET" envDefault:"prodo-attachments"`
	MinIOUseSSL    bool   `env:"MINIO_USE_SSL" envDefault:"false"`

	// OpenBao / Vault — jika di-set, secrets dimuat dari Vault
	VaultAddr  string `env:"VAULT_ADDR" envDefault:""`
	VaultToken string `env:"VAULT_TOKEN" envDefault:""`

	// Asynq (background jobs)
	AsynqConcurrency int `env:"ASYNQ_CONCURRENCY" envDefault:"10"`

	// CORS
	CORSAllowOrigins string `env:"CORS_ALLOW_ORIGINS" envDefault:"http://localhost:5173"`

	// Observability
	OTELEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT" envDefault:""`
	SentryDSN    string `env:"SENTRY_DSN" envDefault:""`
}

// Load membaca konfigurasi dari environment variables.
// Jika VAULT_ADDR dan VAULT_TOKEN di-set, secrets sensitif dimuat dari OpenBao
// dan menimpa nilai env vars yang ada (env vars tetap sebagai fallback).
func Load() (*Config, error) {
	// Muat .env.local jika ada (dev lokal) -- tidak menimpa env var yang sudah
	// di-set (CI, shell export, container) sesuai perilaku default godotenv.
	// File tidak ada = dianggap normal (CI/production selalu set env var
	// langsung, tidak lewat file).
	if err := godotenv.Load(".env.local"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("membaca .env.local: %w", err)
	}

	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("membaca env vars: %w", err)
	}

	// Jika Vault dikonfigurasi, muat secret dari OpenBao
	if cfg.VaultAddr != "" && cfg.VaultToken != "" {
		if err := cfg.loadFromVault(); err != nil {
			return nil, fmt.Errorf("membaca secret dari Vault (%s): %w", cfg.VaultAddr, err)
		}
	}

	// Validasi field wajib setelah (mungkin) diisi dari Vault
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validate memastikan field-field wajib sudah terisi.
func (c *Config) validate() error {
	required := []struct {
		value string
		name  string
	}{
		{c.DatabaseURL, "DATABASE_URL (atau secret Vault /secret/prodo/db.url)"},
		{c.AppDatabaseURL, "APP_DATABASE_URL (koneksi prodo_app_user, S2-10)"},
		{c.RedisURL, "REDIS_URL (atau secret Vault /secret/prodo/redis.url)"},
		{c.KeycloakIssuer, "KEYCLOAK_ISSUER (atau secret Vault /secret/prodo/keycloak.issuer)"},
		{c.MinIOEndpoint, "MINIO_ENDPOINT (atau secret Vault /secret/prodo/minio.endpoint)"},
		{c.MinIOAccessKey, "MINIO_ACCESS_KEY (atau secret Vault /secret/prodo/minio.access_key)"},
		{c.MinIOSecretKey, "MINIO_SECRET_KEY (atau secret Vault /secret/prodo/minio.secret_key)"},
	}

	for _, r := range required {
		if r.value == "" {
			return fmt.Errorf("konfigurasi wajib tidak ditemukan: %s", r.name)
		}
	}
	return nil
}

// vaultKVResponse adalah struktur respons KV v2 dari Vault/OpenBao.
type vaultKVResponse struct {
	Data struct {
		Data map[string]string `json:"data"`
	} `json:"data"`
}

// vaultGet mengambil secret dari path KV v2.
func (c *Config) vaultGet(ctx context.Context, path string) (map[string]string, error) {
	url := fmt.Sprintf("%s/v1/secret/data/%s", c.VaultAddr, path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", c.VaultToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request ke Vault gagal: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // close error on a read-only response body is not actionable

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("membaca respons Vault: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Vault mengembalikan HTTP %d untuk path %s", resp.StatusCode, path)
	}

	var kvResp vaultKVResponse
	if err := json.Unmarshal(body, &kvResp); err != nil {
		return nil, fmt.Errorf("parse respons Vault: %w", err)
	}

	return kvResp.Data.Data, nil
}

// loadFromVault mengambil semua secret PRODO dari OpenBao dan mengisi Config.
// Nilai dari Vault menimpa env vars — ini yang diinginkan saat production.
func (c *Config) loadFromVault() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// /secret/prodo/db
	dbSecrets, err := c.vaultGet(ctx, "prodo/db")
	if err != nil {
		return fmt.Errorf("gagal membaca /secret/prodo/db: %w", err)
	}
	if url, ok := dbSecrets["url"]; ok && url != "" {
		c.DatabaseURL = url
	}

	// /secret/prodo/redis
	redisSecrets, err := c.vaultGet(ctx, "prodo/redis")
	if err != nil {
		return fmt.Errorf("gagal membaca /secret/prodo/redis: %w", err)
	}
	if url, ok := redisSecrets["url"]; ok && url != "" {
		c.RedisURL = url
	}

	// /secret/prodo/minio
	minioSecrets, err := c.vaultGet(ctx, "prodo/minio")
	if err != nil {
		return fmt.Errorf("gagal membaca /secret/prodo/minio: %w", err)
	}
	if v, ok := minioSecrets["endpoint"]; ok && v != "" {
		c.MinIOEndpoint = v
	}
	if v, ok := minioSecrets["access_key"]; ok && v != "" {
		c.MinIOAccessKey = v
	}
	if v, ok := minioSecrets["secret_key"]; ok && v != "" {
		c.MinIOSecretKey = v
	}
	if v, ok := minioSecrets["bucket"]; ok && v != "" {
		c.MinIOBucket = v
	}

	// /secret/prodo/keycloak
	kcSecrets, err := c.vaultGet(ctx, "prodo/keycloak")
	if err != nil {
		return fmt.Errorf("gagal membaca /secret/prodo/keycloak: %w", err)
	}
	if v, ok := kcSecrets["issuer"]; ok && v != "" {
		c.KeycloakIssuer = v
	}
	if v, ok := kcSecrets["audience"]; ok && v != "" {
		c.KeycloakAudience = v
	}

	return nil
}
