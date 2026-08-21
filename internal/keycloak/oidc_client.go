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

// ErrInvalidGrant dikembalikan saat Keycloak menolak grant (password salah,
// authorization code kedaluwarsa/sudah dipakai, dsb -- HTTP 400
// error=invalid_grant). Keycloak sengaja tidak membedakan detail alasan
// lewat kode ini, jadi caller (service.AuthService) yang menentukan
// pemetaan error domain, bukan client ini.
var ErrInvalidGrant = errors.New("keycloak: invalid_grant")

// TokenResponse adalah subset field respons token endpoint Keycloak yang
// dipakai PRODO -- diteruskan apa adanya ke response API_CONTRACT.md
// POST /auth/login, bukan diterbitkan ulang oleh backend sendiri.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"` // hanya ada untuk grant authorization_code (S1-15)
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// OIDCClient membungkus dua grant token endpoint Keycloak yang dipakai
// PRODO untuk model Keycloak-delegated (docs/s1-kickoff.html S1-14/15):
// Resource Owner Password Credentials (login credential lokal) dan
// Authorization Code (login SSO, brokered lewat Keycloak).
type OIDCClient interface {
	// PasswordGrant (S1-14) -- client public "prodo-web".
	PasswordGrant(ctx context.Context, username, password string) (*TokenResponse, error)

	// ExchangeAuthorizationCode (S1-15) -- menukar authorization code hasil
	// redirect Keycloak (setelah user login, termasuk lewat IdP eksternal
	// yang di-broker Keycloak) menjadi token.
	ExchangeAuthorizationCode(ctx context.Context, code, redirectURI string) (*TokenResponse, error)
}

type httpOIDCClient struct {
	baseURL    string
	realm      string
	clientID   string
	httpClient *http.Client
}

// NewOIDCClient membuat OIDCClient dari KEYCLOAK_ISSUER + client public
// (KEYCLOAK_WEB_CLIENT_ID, default "prodo-web" -- lihat
// infra/keycloak/realm-PRODO.json directAccessGrantsEnabled=true).
func NewOIDCClient(issuerURL, clientID string) (OIDCClient, error) {
	base, realm, err := splitIssuer(issuerURL)
	if err != nil {
		return nil, fmt.Errorf("keycloak.NewOIDCClient: %w", err)
	}
	return &httpOIDCClient{
		baseURL:    base,
		realm:      realm,
		clientID:   clientID,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (c *httpOIDCClient) PasswordGrant(ctx context.Context, username, password string) (*TokenResponse, error) {
	return c.tokenRequest(ctx, url.Values{
		"client_id":  {c.clientID},
		"grant_type": {"password"},
		"username":   {username},
		"password":   {password},
	})
}

func (c *httpOIDCClient) ExchangeAuthorizationCode(ctx context.Context, code, redirectURI string) (*TokenResponse, error) {
	return c.tokenRequest(ctx, url.Values{
		"client_id":    {c.clientID},
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
	})
}

func (c *httpOIDCClient) tokenRequest(ctx context.Context, form url.Values) (*TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", c.baseURL, c.realm),
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("keycloak.tokenRequest: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("keycloak.tokenRequest: request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // close error on a read-only response body is not actionable

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("keycloak.tokenRequest: baca response: %w", err)
	}

	if resp.StatusCode == http.StatusBadRequest {
		return nil, fmt.Errorf("keycloak.tokenRequest: %w", ErrInvalidGrant)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("keycloak.tokenRequest: HTTP %d: %s", resp.StatusCode, string(body))
	}

	tr := &TokenResponse{}
	if err := json.Unmarshal(body, tr); err != nil {
		return nil, fmt.Errorf("keycloak.tokenRequest: parse response: %w", err)
	}
	return tr, nil
}
