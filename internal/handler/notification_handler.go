package handler

import (
	"context"
	"errors"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// notificationService -- interface didefinisikan di consumer, diimplementasikan *service.NotificationService.
type notificationService interface {
	List(ctx context.Context, exec db.Executor, userID, workspaceID string, page, perPage int) (*service.NotificationPage, error)
	UnreadCount(ctx context.Context, exec db.Executor, userID, workspaceID string) (int, error)
	MarkRead(ctx context.Context, exec db.Executor, userID, id string) error
	MarkAllRead(ctx context.Context, exec db.Executor, userID, workspaceID string) (int, error)
}

// NotificationHandler -- lonceng notifikasi in-app (US-034, S6E-14, IG-145): /me/notifications*.
type NotificationHandler struct {
	svc    notificationService
	logger *zap.Logger
}

func NewNotificationHandler(svc notificationService, logger *zap.Logger) *NotificationHandler {
	return &NotificationHandler{svc: svc, logger: logger}
}

func notificationJSON(n *repository.NotificationRow) fiber.Map {
	return fiber.Map{
		"id": n.ID, "type": n.Type, "actor_name": n.ActorName, "title": n.Title, "body": n.Body,
		"entity_type": n.EntityType, "entity_id": n.EntityID, "project_id": n.ProjectID, "project_name": n.ProjectName,
		"task_code": n.TaskCode, "is_read": n.IsRead, "created_at": n.CreatedAt,
	}
}

// actorTx -- pemanggil + transaksi RLS; rute /me/* tanpa RequireRole.
func (h *NotificationHandler) actorTx(c *fiber.Ctx) (string, db.Executor, bool) {
	userID, _, uok := middleware.ActorFromContext(c)
	exec, eok := middleware.DBTxFromContext(c)
	if !uok || !eok {
		_ = c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
		return "", nil, false
	}
	return userID, exec, true
}

func (h *NotificationHandler) fail(c *fiber.Ctx, err error, msg string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "workspace_id wajib diisi", nil))
	case errors.Is(err, domain.ErrNotificationNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Notifikasi tidak ditemukan", nil))
	}
	h.logger.Error("NotificationHandler gagal", zap.Error(err))
	return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", msg, nil))
}

// List menangani GET /me/notifications?workspace_id=&page=&per_page= -> {items, total, unread_count, page, per_page}.
func (h *NotificationHandler) List(c *fiber.Ctx) error {
	userID, exec, ok := h.actorTx(c)
	if !ok {
		return nil
	}
	res, err := h.svc.List(c.Context(), exec, userID, c.Query("workspace_id"), c.QueryInt("page", 1), c.QueryInt("per_page", 0))
	if err != nil {
		return h.fail(c, err, "Gagal mengambil notifikasi")
	}
	items := make([]fiber.Map, len(res.Items))
	for i := range res.Items {
		items[i] = notificationJSON(&res.Items[i])
	}
	return c.JSON(response.Success(fiber.Map{
		"items": items, "total": res.Total, "unread_count": res.UnreadCount, "page": res.Page, "per_page": res.PerPage,
	}))
}

// UnreadCount menangani GET /me/notifications/unread-count?workspace_id= -> {unread_count}.
func (h *NotificationHandler) UnreadCount(c *fiber.Ctx) error {
	userID, exec, ok := h.actorTx(c)
	if !ok {
		return nil
	}
	n, err := h.svc.UnreadCount(c.Context(), exec, userID, c.Query("workspace_id"))
	if err != nil {
		return h.fail(c, err, "Gagal menghitung notifikasi belum dibaca")
	}
	return c.JSON(response.Success(fiber.Map{"unread_count": n}))
}

// MarkRead menangani PATCH /me/notifications/:id/read.
func (h *NotificationHandler) MarkRead(c *fiber.Ctx) error {
	userID, exec, ok := h.actorTx(c)
	if !ok {
		return nil
	}
	id := c.Params("id")
	if err := h.svc.MarkRead(c.Context(), exec, userID, id); err != nil {
		return h.fail(c, err, "Gagal menandai notifikasi dibaca")
	}
	return c.JSON(response.Success(fiber.Map{"id": id}))
}

type readAllRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

// MarkAllRead menangani POST /me/notifications/read-all (body {workspace_id}) -> {updated}.
func (h *NotificationHandler) MarkAllRead(c *fiber.Ctx) error {
	userID, exec, ok := h.actorTx(c)
	if !ok {
		return nil
	}
	var body readAllRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	n, err := h.svc.MarkAllRead(c.Context(), exec, userID, body.WorkspaceID)
	if err != nil {
		return h.fail(c, err, "Gagal menandai semua notifikasi dibaca")
	}
	return c.JSON(response.Success(fiber.Map{"updated": n}))
}
