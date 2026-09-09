// Package service -- SSOConfigService (US-074, Track S4G S4G-23). Cakupan
// SENGAJA lebih kecil dari draft S12-27..33 -- lihat komentar migrasi
// 20261002090000_sso_configs untuk detail (test koneksi IdP/enforcement
// auth_mode/reset password massal/registrasi Keycloak dinamis TIDAK ikut
// dipindah ke Track S4G).
package service

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// ssoConfigRepository -- interface didefinisikan di consumer, §3.9.
type ssoConfigRepository interface {
	Get(ctx context.Context, exec db.Executor, orgID string) (*repository.SSOConfig, error)
	Upsert(ctx context.Context, exec db.Executor, orgID, protocol string, idpEntityID, idpMetadataURL, idpMetadataXML, clientID, clientSecret, discoveryURL *string, ssoEnabled bool) error
}

type SSOConfigService struct {
	repo ssoConfigRepository
	orgs orgAuthorizer
}

func NewSSOConfigService(repo ssoConfigRepository, orgs orgAuthorizer) *SSOConfigService {
	return &SSOConfigService{repo: repo, orgs: orgs}
}

func (s *SSOConfigService) Get(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) (*repository.SSOConfig, error) {
	if orgID == "" {
		return nil, fmt.Errorf("service.Get: %w", domain.ErrInvalidInput)
	}
	if err := s.orgs.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return nil, err
	}
	cfg, err := s.repo.Get(ctx, exec, orgID)
	if err != nil {
		return nil, fmt.Errorf("service.Get: %w", err)
	}
	return cfg, nil
}

// isHTTPSURL -- dipakai validasi discovery_url (OIDC) dan idp_metadata_url
// (SAML, alternatif dari XML) -- IdP sungguhan selalu di-serve lewat HTTPS.
func isHTTPSURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// isWellFormedXML -- validasi STRUKTUR saja (well-formed), BUKAN skema
// penuh SAML Metadata (butuh library SAML khusus, di luar cakupan S4G-23 --
// AC Bruno cuma minta "XML invalid -> 422", bukan validasi skema).
func isWellFormedXML(raw string) bool {
	dec := xml.NewDecoder(strings.NewReader(raw))
	for {
		if _, err := dec.Token(); err != nil {
			return errors.Is(err, io.EOF)
		}
	}
}

// validateMetadata -- S4G-23 AC: "PUT metadata OIDC valid -> 200; XML SAML
// invalid -> 422". OIDC butuh discovery_url (https); SAML butuh
// idp_metadata_xml (well-formed) ATAU idp_metadata_url (https) sebagai
// alternatif.
func validateMetadata(protocol string, idpMetadataURL, idpMetadataXML, discoveryURL *string) error {
	switch protocol {
	case "oidc":
		if discoveryURL == nil || !isHTTPSURL(*discoveryURL) {
			return fmt.Errorf("service.validateMetadata: %w", domain.ErrInvalidSsoMetadata)
		}
	case "saml2":
		hasXML := idpMetadataXML != nil && strings.TrimSpace(*idpMetadataXML) != "" && isWellFormedXML(*idpMetadataXML)
		hasURL := idpMetadataURL != nil && isHTTPSURL(*idpMetadataURL)
		if !hasXML && !hasURL {
			return fmt.Errorf("service.validateMetadata: %w", domain.ErrInvalidSsoMetadata)
		}
	default:
		return fmt.Errorf("service.validateMetadata: %w", domain.ErrInvalidSsoProtocol)
	}
	return nil
}

// Update menangani PUT /organizations/:id/sso-config. clientSecret nil
// berarti tidak diubah (lihat komentar SSOConfigRepository.Upsert).
func (s *SSOConfigService) Update(ctx context.Context, exec db.Executor, orgID, protocol string, idpEntityID, idpMetadataURL, idpMetadataXML, clientID, clientSecret, discoveryURL *string, ssoEnabled bool, actorID, actorRole string) error {
	if orgID == "" {
		return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol != "saml2" && protocol != "oidc" {
		return fmt.Errorf("service.Update: %w", domain.ErrInvalidSsoProtocol)
	}
	if err := validateMetadata(protocol, idpMetadataURL, idpMetadataXML, discoveryURL); err != nil {
		return err
	}
	if err := s.orgs.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.Upsert(ctx, exec, orgID, protocol, idpEntityID, idpMetadataURL, idpMetadataXML, clientID, clientSecret, discoveryURL, ssoEnabled); err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	return nil
}
