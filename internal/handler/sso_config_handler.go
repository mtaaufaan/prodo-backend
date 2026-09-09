// Package handler -- SSOConfigHandler (US-074, Track S4G S4G-23).
package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

type SSOConfigHandler struct {
	sso    *service.SSOConfigService
	logger *zap.Logger
}

func NewSSOConfigHandler(sso *service.SSOConfigService, logger *zap.Logger) *SSOConfigHandler {
	return &SSOConfigHandler{sso: sso, logger: logger}
}

// Get menangani GET /organizations/:id/sso-config.
func (h *SSOConfigHandler) Get(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	cfg, err := h.sso.Get(c.Context(), exec, c.Params("id"), actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil konfigurasi SSO")
	}
	return c.JSON(response.Success(ssoConfigToMap(cfg)))
}

type ssoConfigRequest struct {
	Protocol       string  `json:"protocol"`
	IdpEntityID    *string `json:"idp_entity_id"`
	IdpMetadataURL *string `json:"idp_metadata_url"`
	IdpMetadataXML *string `json:"idp_metadata_xml"`
	ClientID       *string `json:"client_id"`
	ClientSecret   *string `json:"client_secret"`
	DiscoveryURL   *string `json:"discovery_url"`
	SSOEnabled     bool    `json:"sso_enabled"`
}

// Update menangani PUT /organizations/:id/sso-config -- client_secret
// kosong/tidak dikirim berarti tidak diubah (lihat komentar
// SSOConfigRepository.Upsert).
func (h *SSOConfigHandler) Update(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	orgID := c.Params("id")

	var body ssoConfigRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	if body.ClientSecret != nil && *body.ClientSecret == "" {
		body.ClientSecret = nil
	}
	if err := h.sso.Update(c.Context(), exec, orgID, body.Protocol, body.IdpEntityID, body.IdpMetadataURL, body.IdpMetadataXML, body.ClientID, body.ClientSecret, body.DiscoveryURL, body.SSOEnabled, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal menyimpan konfigurasi SSO")
	}
	return c.JSON(response.Success(fiber.Map{"id": orgID}))
}

func ssoConfigToMap(c *repository.SSOConfig) fiber.Map {
	return fiber.Map{
		"organization_id":   c.OrgID,
		"configured":        c.Configured,
		"sso_enabled":       c.SSOEnabled,
		"protocol":          c.Protocol,
		"idp_entity_id":     c.IdpEntityID,
		"idp_metadata_url":  c.IdpMetadataURL,
		"idp_metadata_xml":  c.IdpMetadataXML,
		"client_id":         c.ClientID,
		"has_client_secret": c.HasClientSecret,
		"discovery_url":     c.DiscoveryURL,
		"is_tested":         c.IsTested,
		"last_test_at":      c.LastTestAt,
		"updated_at":        c.UpdatedAt,
	}
}

func (h *SSOConfigHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrInvalidSsoProtocol):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("INVALID_PROTOCOL", "Protokol harus 'saml2' atau 'oidc'.", nil))
	case errors.Is(err, domain.ErrInvalidSsoMetadata):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("INVALID_METADATA", "Metadata IdP tidak valid -- OIDC butuh Discovery URL (https), SAML butuh metadata XML yang well-formed atau Metadata URL (https).", nil))
	case errors.Is(err, domain.ErrOrganizationNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Organisasi tidak ditemukan", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas organisasi ini.", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
