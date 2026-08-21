package keycloak

import (
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

// ErrInvalidGrant dikembalikan PasswordGrant saat Keycloak menolak
// email/password (grant_type=password, HTTP 400 error=invalid_grant).
// Keycloak sengaja tidak membedakan "user tidak ada" vs "password salah"
// vs "akun disabled" lewat kode ini -- semuanya invalid_grant, jadi
// caller (service.AuthService) yang menentukan pemetaan error domain,
// bukan client ini.
var ErrInvalidGrant = errors.New("keycloak: invalid_grant")

// TokenResponse adalah subset field respons token endpoint Keycloak yang
// dipakai PRODO (S1-14) -- diteruskan apa adanya ke response API_CONTRACT.md
// POST /auth/login, bukan diterbitkan ulang oleh backend sendiri.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// ROPCClient membungkus Resource Owner Password Credentials grant
// (Direct Access Grant) Keycloak untuk client public "prodo-web" -- model
// Keycloak-delegated yang dikonfirmasi di S1-01/02 (tidak ada password_hash
// lokal, lihat docs/s1-kickoff.html S1-14).
type ROPCClient interface {
	PasswordGrant(ctx context.Context, username, password string) (*TokenResponse, error)
}

type httpROPCClient struct {
	baseURL    string
	realm      string
	clientID   string
	httpClient *http.Client
}

// NewROPCClient membuat ROPCClient dari KEYCLOAK_ISSUER + client public
// (KEYCLOAK_WEB_CLIENT_ID, default "prodo-web" -- lihat
// infra/keycloak/realm-PRODO.json directAccessGrantsEnabled=true).
func NewROPCClient(issuerURL, clientID string) (ROPCClient, error) {
	base, realm, err := splitIssuer(issuerURL)
	if err != nil {
		return nil, fmt.Errorf("keycloak.NewROPCClient: %w", err)
	}
	return &httpROPCClient{
		baseURL:    base,
		realm:      realm,
		clientID:   clientID,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (c *httpROPCClient) PasswordGrant(ctx context.Context, username, password string) (*TokenResponse, error) {
	form := url.Values{
		"client_id":  {c.clientID},
		"grant_type": {"password"},
		"username":   {username},
		"password":   {password},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", c.baseURL, c.realm),
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("keycloak.PasswordGrant: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("keycloak.PasswordGrant: request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // close error on a read-only response body is not actionable

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("keycloak.PasswordGrant: baca response: %w", err)
	}

	if resp.StatusCode == http.StatusBadRequest {
		return nil, fmt.Errorf("keycloak.PasswordGrant: %w", ErrInvalidGrant)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("keycloak.PasswordGrant: HTTP %d: %s", resp.StatusCode, string(body))
	}

	tr := &TokenResponse{}
	if err := json.Unmarshal(body, tr); err != nil {
		return nil, fmt.Errorf("keycloak.PasswordGrant: parse response: %w", err)
	}
	return tr, nil
}
