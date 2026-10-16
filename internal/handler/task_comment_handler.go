package handler

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// commentService -- interface didefinisikan di consumer, diimplementasikan *service.TaskCommentService.
type commentService interface {
	Create(ctx context.Context, exec db.Executor, taskID, userID, text string) (*service.CreateResult, error)
	List(ctx context.Context, exec db.Executor, taskID, userID string) ([]service.CommentView, error)
	Edit(ctx context.Context, exec db.Executor, commentID, userID, text string) (*service.CommentView, error)
	Delete(ctx context.Context, exec db.Executor, commentID, userID string) error
	SetFollow(ctx context.Context, exec db.Executor, taskID, userID string, follow bool) error
}

// TaskCommentHandler -- komentar task + ikuti/berhenti (S6E-04, IG-135; US-030/031).
type TaskCommentHandler struct {
	comments commentService
	logger   *zap.Logger
}

func NewTaskCommentHandler(comments commentService, logger *zap.Logger) *TaskCommentHandler {
	return &TaskCommentHandler{comments: comments, logger: logger}
}

type commentRequest struct {
	Body string `json:"body"`
}

func commentJSON(v *service.CommentView) fiber.Map {
	var editedAt any
	if v.EditedAt != nil {
		editedAt = v.EditedAt.UTC().Format(time.RFC3339)
	}
	return fiber.Map{
		"id": v.ID, "task_id": v.TaskID, "user_id": v.UserID, "author_name": v.AuthorName, "body": v.Text,
		"is_edited": v.IsEdited, "edited_at": editedAt, "created_at": v.CreatedAt.UTC().Format(time.RFC3339),
		"can_edit": v.CanEdit, "can_delete": v.CanDelete,
	}
}

func (h *TaskCommentHandler) actor(c *fiber.Ctx) (userID string, exec db.Executor, ok bool) {
	userID, _, uok := middleware.ActorFromContext(c)
	exec, eok := middleware.DBTxFromContext(c)
	return userID, exec, uok && eok
}

func (h *TaskCommentHandler) internal(c *fiber.Ctx) error {
	return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
}

func (h *TaskCommentHandler) mapError(c *fiber.Ctx, err error, fallback string) error {
	switch {
	case errors.Is(err, domain.ErrCommentInvalid):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Komentar harus 2-1000 karakter.", nil))
	case errors.Is(err, domain.ErrCommentEditLocked):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("COMMENT_EDIT_LOCKED", "Komentar hanya dapat diedit dalam 3 menit setelah dikirim.", nil))
	case errors.Is(err, domain.ErrCommentForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak memiliki izin untuk aksi komentar ini.", nil))
	case errors.Is(err, domain.ErrCommentNotFound), errors.Is(err, domain.ErrTaskNotFound), errors.Is(err, domain.ErrProjectNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Task atau komentar tidak ditemukan", nil))
	}
	h.logger.Error("TaskCommentHandler: "+fallback, zap.Error(err))
	return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallback, nil))
}

// Create menangani POST /tasks/:id/comments.
func (h *TaskCommentHandler) Create(c *fiber.Ctx) error {
	userID, exec, ok := h.actor(c)
	if !ok {
		return h.internal(c)
	}
	var body commentRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	res, err := h.comments.Create(c.Context(), exec, c.Params("id"), userID, body.Body)
	if err != nil {
		return h.mapError(c, err, "Gagal mengirim komentar")
	}
	mentions := res.Mentions
	if mentions == nil {
		mentions = []string{}
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{
		"comment": commentJSON(&res.Comment), "comment_count": res.CommentCount, "mentions": mentions, "notified": res.Notified,
	}))
}

// List menangani GET /tasks/:id/comments.
func (h *TaskCommentHandler) List(c *fiber.Ctx) error {
	userID, exec, ok := h.actor(c)
	if !ok {
		return h.internal(c)
	}
	list, err := h.comments.List(c.Context(), exec, c.Params("id"), userID)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil komentar")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = commentJSON(&list[i])
	}
	return c.JSON(response.Success(data))
}

// Edit menangani PATCH /comments/:id.
func (h *TaskCommentHandler) Edit(c *fiber.Ctx) error {
	userID, exec, ok := h.actor(c)
	if !ok {
		return h.internal(c)
	}
	var body commentRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	v, err := h.comments.Edit(c.Context(), exec, c.Params("id"), userID, body.Body)
	if err != nil {
		return h.mapError(c, err, "Gagal mengedit komentar")
	}
	return c.JSON(response.Success(commentJSON(v)))
}

// Delete menangani DELETE /comments/:id (soft delete).
func (h *TaskCommentHandler) Delete(c *fiber.Ctx) error {
	userID, exec, ok := h.actor(c)
	if !ok {
		return h.internal(c)
	}
	if err := h.comments.Delete(c.Context(), exec, c.Params("id"), userID); err != nil {
		return h.mapError(c, err, "Gagal menghapus komentar")
	}
	return c.JSON(response.Success(fiber.Map{"id": c.Params("id")}))
}

func (h *TaskCommentHandler) follow(c *fiber.Ctx, follow bool) error {
	userID, exec, ok := h.actor(c)
	if !ok {
		return h.internal(c)
	}
	if err := h.comments.SetFollow(c.Context(), exec, c.Params("id"), userID, follow); err != nil {
		return h.mapError(c, err, "Gagal mengubah status ikuti task")
	}
	return c.JSON(response.Success(fiber.Map{"task_id": c.Params("id"), "following": follow}))
}

// Follow menangani PUT /tasks/:id/follow.
func (h *TaskCommentHandler) Follow(c *fiber.Ctx) error { return h.follow(c, true) }

// Unfollow menangani DELETE /tasks/:id/follow.
func (h *TaskCommentHandler) Unfollow(c *fiber.Ctx) error { return h.follow(c, false) }
