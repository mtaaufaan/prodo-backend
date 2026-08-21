package keycloak

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPasswordGrant_InvalidGrant_HTTP401 -- regresi bug ditemukan lewat
// verifikasi live S1-18: Keycloak 24 membalas HTTP 401 (bukan 400) untuk
// grant_type=password dengan kredensial salah, padahal body-nya tetap
// {"error":"invalid_grant"}. tokenRequest() harus mendeteksi ini dari body,
// bukan cuma status code.
func TestPasswordGrant_InvalidGrant_HTTP401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid user credentials"}`))
	}))
	defer server.Close()

	c := &httpOIDCClient{baseURL: server.URL, realm: "PRODO", clientID: "prodo-web", httpClient: server.Client()}

	_, err := c.PasswordGrant(context.Background(), "user@example.com", "wrong-password")
	if !errors.Is(err, ErrInvalidGrant) {
		t.Errorf("err = %v, want wrapped ErrInvalidGrant", err)
	}
}

func TestExchangeAuthorizationCode_InvalidGrant_HTTP400(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Code not valid"}`))
	}))
	defer server.Close()

	c := &httpOIDCClient{baseURL: server.URL, realm: "PRODO", clientID: "prodo-web", httpClient: server.Client()}

	_, err := c.ExchangeAuthorizationCode(context.Background(), "expired-code", "http://localhost/callback")
	if !errors.Is(err, ErrInvalidGrant) {
		t.Errorf("err = %v, want wrapped ErrInvalidGrant", err)
	}
}

func TestTokenRequest_OtherError_NotSwallowed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal server error`))
	}))
	defer server.Close()

	c := &httpOIDCClient{baseURL: server.URL, realm: "PRODO", clientID: "prodo-web", httpClient: server.Client()}

	_, err := c.PasswordGrant(context.Background(), "user@example.com", "whatever")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrInvalidGrant) {
		t.Error("HTTP 500 tanpa error=invalid_grant tidak boleh dipetakan ke ErrInvalidGrant")
	}
}
