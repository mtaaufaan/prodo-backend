// Package keycloak memanggil Keycloak Admin REST API lewat client
// service-account "prodo-backend-admin" (client_credentials grant). Client
// ini + scope "roles" yang membawa claim resource_access ditambahkan di
// infra/keycloak/realm-PRODO.json -- lihat docs/s1-h1-infra-runbook.md
// §Update untuk root-cause 2 bug yang sempat memblokir ini (client scope
// "roles" tidak pernah didefinisikan, dan owner schema public yang salah
// saat reset DB keycloak_dev).
package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrUserAlreadyExists dikembalikan CreateDisabledUser saat email/username
// sudah terdaftar di Keycloak (HTTP 409 dari Admin REST API).
var ErrUserAlreadyExists = errors.New("keycloak: user already exists")

// AdminClient adalah abstraksi tipis di atas Keycloak Admin REST API --
// cukup untuk kebutuhan S1-03 (provisioning user disabled). Operasi admin
// lain ditambah langsung di sini kalau dibutuhkan, bukan lewat library
// pihak ketiga (gocloak dkk.) yang jauh lebih besar dari yang dipakai.
type AdminClient interface {
	// CreateDisabledUser membuat user Keycloak dalam kondisi disabled,
	// dengan requiredActions UPDATE_PASSWORD + CONFIGURE_TOTP -- user baru
	// bisa login setelah mengaktifkan akun lewat activation link (S1-06/07).
	// Mengembalikan Keycloak user ID (subject/`sub`).
	CreateDisabledUser(ctx context.Context, email, displayName string) (keycloakUserID string, err error)
}

type httpAdminClient struct {
	baseURL      string // contoh: http://localhost:8081
	realm        string // contoh: PRODO
	clientID     string
	clientSecret string
	httpClient   *http.Client
}

// NewAdminClient membuat AdminClient dari KEYCLOAK_ISSUER (dipecah jadi base
// URL + nama realm) dan kredensial client service-account
// (KEYCLOAK_ADMIN_CLIENT_ID/SECRET). clientID/clientSecret wajib diisi --
// tanpa itu setiap panggilan Admin REST API akan gagal, jadi divalidasi di
// sini supaya error muncul saat startup, bukan saat request pertama masuk.
func NewAdminClient(issuerURL, clientID, clientSecret string) (AdminClient, error) {
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("keycloak.NewAdminClient: KEYCLOAK_ADMIN_CLIENT_ID/KEYCLOAK_ADMIN_CLIENT_SECRET wajib diisi")
	}

	base, realm, err := splitIssuer(issuerURL)
	if err != nil {
		return nil, fmt.Errorf("keycloak.NewAdminClient: %w", err)
	}

	return &httpAdminClient{
		baseURL:      base,
		realm:        realm,
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// splitIssuer memecah "http://host:port/realms/PRODO" jadi base URL dan
// nama realm.
func splitIssuer(issuerURL string) (base, realm string, err error) {
	const marker = "/realms/"
	idx := strings.Index(issuerURL, marker)
	if idx == -1 {
		return "", "", fmt.Errorf("KEYCLOAK_ISSUER tidak mengandung %q: %q", marker, issuerURL)
	}
	return issuerURL[:idx], issuerURL[idx+len(marker):], nil
}

func (c *httpAdminClient) token(ctx context.Context) (string, error) {
	form := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"grant_type":    {"client_credentials"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", c.baseURL, c.realm),
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token: request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // close error on a read-only response body is not actionable

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("token: baca response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var tr struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("token: parse response: %w", err)
	}
	return tr.AccessToken, nil
}

func (c *httpAdminClient) CreateDisabledUser(ctx context.Context, email, displayName string) (string, error) {
	tok, err := c.token(ctx)
	if err != nil {
		return "", fmt.Errorf("keycloak.CreateDisabledUser: %w", err)
	}

	firstName, lastName := splitDisplayName(displayName)
	payload, err := json.Marshal(map[string]any{
		"username":        email,
		"email":           email,
		"firstName":       firstName,
		"lastName":        lastName,
		"enabled":         false,
		"emailVerified":   false,
		"requiredActions": []string{"UPDATE_PASSWORD", "CONFIGURE_TOTP"},
	})
	if err != nil {
		return "", fmt.Errorf("keycloak.CreateDisabledUser: encode payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/admin/realms/%s/users", c.baseURL, c.realm),
		bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("keycloak.CreateDisabledUser: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("keycloak.CreateDisabledUser: request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // close error on a read-only response body is not actionable

	if resp.StatusCode == http.StatusConflict {
		return "", fmt.Errorf("keycloak.CreateDisabledUser: %w", ErrUserAlreadyExists)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort untuk pesan error
		return "", fmt.Errorf("keycloak.CreateDisabledUser: HTTP %d: %s", resp.StatusCode, string(body))
	}

	location := resp.Header.Get("Location")
	id := location[strings.LastIndex(location, "/")+1:]
	if id == "" {
		return "", fmt.Errorf("keycloak.CreateDisabledUser: Location header kosong, tidak bisa ambil user ID")
	}
	return id, nil
}

// splitDisplayName memecah "Nama Lengkap" jadi firstName/lastName --
// Keycloak butuh 2 field terpisah, PRODO cuma simpan satu display_name
// (§5.1). Nama satu kata (tanpa spasi) jadi firstName saja.
func splitDisplayName(displayName string) (first, last string) {
	parts := strings.SplitN(strings.TrimSpace(displayName), " ", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}
