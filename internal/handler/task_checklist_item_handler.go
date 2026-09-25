// Package handler -- TaskChecklistItemHandler (SUB-TASK, IG-97 susulan).
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

type TaskChecklistItemHandler struct {
	items  *service.TaskChecklistItemService
	logger *zap.Logger
}

func NewTaskChecklistItemHandler(items *service.TaskChecklistItemService, logger *zap.Logger) *TaskChecklistItemHandler {
	return &TaskChecklistItemHandler{items: items, logger: logger}
}

func checklistItemJSON(item *repository.TaskChecklistItem) fiber.Map {
	return fiber.Map{"id": item.ID, "task_id": item.TaskID, "title": item.Title, "is_done": item.IsDone, "position": item.Position}
}

type createChecklistItemRequest struct {
	Title string `json:"title"`
}

// Create menangani POST /tasks/:id/checklist-items.
func (h *TaskChecklistItemHandler) Create(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	var body createChecklistItemRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	item, err := h.items.Create(c.Context(), exec, c.Params("id"), body.Title, actorUserID)
	if err != nil {
		return h.mapError(c, err, "Gagal menambah sub-task")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(checklistItemJSON(item)))
}

// List menangani GET /tasks/:id/checklist-items.
func (h *TaskChecklistItemHandler) List(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	list, err := h.items.ListForTask(c.Context(), exec, c.Params("id"))
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil sub-task")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = checklistItemJSON(&list[i])
	}
	return c.JSON(response.Success(data))
}

type updateChecklistItemRequest struct {
	Title  *string `json:"title"`
	IsDone *bool   `json:"is_done"`
}

// Update menangani PATCH /tasks/checklist-items/:itemId.
func (h *TaskChecklistItemHandler) Update(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	var body updateChecklistItemRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	itemID := c.Params("itemId")
	if err := h.items.Update(c.Context(), exec, itemID, body.Title, body.IsDone); err != nil {
		return h.mapError(c, err, "Gagal mengubah sub-task")
	}
	return c.JSON(response.Success(fiber.Map{"id": itemID}))
}

// Delete menangani DELETE /tasks/checklist-items/:itemId.
func (h *TaskChecklistItemHandler) Delete(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	if err := h.items.Delete(c.Context(), exec, c.Params("itemId")); err != nil {
		return h.mapError(c, err, "Gagal menghapus sub-task")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *TaskChecklistItemHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Judul sub-task wajib diisi.", nil))
	case errors.Is(err, domain.ErrTaskNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Sub-task tidak ditemukan", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
