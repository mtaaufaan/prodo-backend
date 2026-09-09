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

// WebhookHandler -- Webhook (Track S4G, desain "GA Webhook.dc.html" +
// "GA Add Webhook.dc.html"). Lihat komentar package service untuk 3 event
// yang didukung.
type WebhookHandler struct {
	webhooks *service.WebhookService
	logger   *zap.Logger
}

func NewWebhookHandler(webhooks *service.WebhookService, logger *zap.Logger) *WebhookHandler {
	return &WebhookHandler{webhooks: webhooks, logger: logger}
}

type webhookRequest struct {
	OrgID  string   `json:"org_id"`
	Name   string   `json:"name"`
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// List menangani GET /groups/:groupId/webhooks.
func (h *WebhookHandler) List(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.List dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.List dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")

	list, err := h.webhooks.List(c.Context(), exec, groupID, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil daftar webhook")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = webhookJSON(&list[i])
	}
	return c.JSON(response.Success(data))
}

// Create menangani POST /groups/:groupId/webhooks -- mengembalikan secret
// plaintext SATU KALI (desain: "DITAMPILKAN SATU KALI").
func (h *WebhookHandler) Create(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.Create dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.Create dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")

	var body webhookRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}

	id, secret, err := h.webhooks.Create(c.Context(), exec, groupID, body.OrgID, body.Name, body.URL, body.Events, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal membuat webhook")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{"id": id, "secret": secret}))
}

// Update menangani PUT /groups/:groupId/webhooks/:webhookId.
func (h *WebhookHandler) Update(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.Update dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.Update dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	webhookID := c.Params("webhookId")

	var body webhookRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}

	if err := h.webhooks.Update(c.Context(), exec, webhookID, body.OrgID, body.Name, body.URL, body.Events, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal memperbarui webhook")
	}
	return c.JSON(response.Success(fiber.Map{"id": webhookID}))
}

type toggleActiveRequest struct {
	Active bool `json:"active"`
}

// ToggleActive menangani PATCH /groups/:groupId/webhooks/:webhookId/toggle-active.
func (h *WebhookHandler) ToggleActive(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.ToggleActive dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.ToggleActive dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	webhookID := c.Params("webhookId")

	var body toggleActiveRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}

	if err := h.webhooks.SetActive(c.Context(), exec, webhookID, body.Active, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal mengubah status webhook")
	}
	return c.JSON(response.Success(fiber.Map{"id": webhookID, "active": body.Active}))
}

// RegenerateSecret menangani POST /groups/:groupId/webhooks/:webhookId/regenerate-secret.
func (h *WebhookHandler) RegenerateSecret(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.RegenerateSecret dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.RegenerateSecret dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	webhookID := c.Params("webhookId")

	secret, err := h.webhooks.RegenerateSecret(c.Context(), exec, webhookID, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal membuat ulang secret")
	}
	return c.JSON(response.Success(fiber.Map{"secret": secret}))
}

// Delete menangani DELETE /groups/:groupId/webhooks/:webhookId.
func (h *WebhookHandler) Delete(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.Delete dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.Delete dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	webhookID := c.Params("webhookId")

	if err := h.webhooks.Delete(c.Context(), exec, webhookID, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal menghapus webhook")
	}
	return c.JSON(response.Success(fiber.Map{"id": webhookID}))
}

// Test menangani POST /groups/:groupId/webhooks/:webhookId/test -- kirim
// SINKRON, rate-limited di router (5x/menit, lihat cmd/api/main.go).
func (h *WebhookHandler) Test(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.Test dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.Test dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	webhookID := c.Params("webhookId")

	durationMs, delivered, err := h.webhooks.Test(c.Context(), exec, webhookID, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengirim tes webhook")
	}
	return c.JSON(response.Success(fiber.Map{"delivered": delivered, "duration_ms": durationMs}))
}

// Deliveries menangani GET /groups/:groupId/webhooks/deliveries?status=&webhook_id=
// -- tab Log Pengiriman.
func (h *WebhookHandler) Deliveries(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.Deliveries dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("WebhookHandler.Deliveries dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")

	list, err := h.webhooks.ListDeliveries(c.Context(), exec, groupID, c.Query("status"), c.Query("webhook_id"), actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil log pengiriman")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = deliveryJSON(&list[i])
	}
	return c.JSON(response.Success(data))
}

func webhookJSON(w *repository.Webhook) fiber.Map {
	return fiber.Map{
		"id": w.ID, "org_id": w.OrgID, "org_name": w.OrgName, "name": w.Name, "url": w.TargetURL,
		"events": w.Events, "is_active": w.IsActive, "created_at": w.CreatedAt,
		"sent_30d": w.Sent30d, "failed_30d": w.Failed30d, "last_event_at": w.LastEventAt,
	}
}

func deliveryJSON(d *repository.WebhookDelivery) fiber.Map {
	return fiber.Map{
		"id": d.ID, "webhook_id": d.WebhookID, "webhook_name": d.WebhookName, "event_type": d.EventType,
		"payload": d.Payload, "attempt_number": d.AttemptNumber, "status": d.Status,
		"http_status": d.HTTPStatus, "response_body": d.ResponseBody, "error_message": d.ErrorMessage,
		"duration_ms": d.DurationMs, "created_at": d.CreatedAt,
	}
}

func (h *WebhookHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR",
			"Input tidak valid -- nama, URL, dan cakupan organisasi (kalau diisi) wajib benar", nil))
	case errors.Is(err, domain.ErrWebhookURLNotHTTPS):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("URL_NOT_HTTPS", "Endpoint harus memakai HTTPS", nil))
	case errors.Is(err, domain.ErrWebhookEventRequired):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("EVENT_REQUIRED", "Pilih minimal satu event trigger yang didukung", nil))
	case errors.Is(err, domain.ErrWebhookNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Webhook tidak ditemukan", nil))
	case errors.Is(err, domain.ErrOrganizationNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Organisasi tidak ditemukan dalam grup ini", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas grup ini.", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
