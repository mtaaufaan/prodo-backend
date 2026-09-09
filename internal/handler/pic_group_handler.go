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

// PicGroupHandler -- Task Management Core Phase 2, US-017b. PM/AW saja
// yang boleh kelola (lihat TaskPicService.authorizePicGroupManage).
type PicGroupHandler struct {
	pics   *service.TaskPicService
	logger *zap.Logger
}

func NewPicGroupHandler(pics *service.TaskPicService, logger *zap.Logger) *PicGroupHandler {
	return &PicGroupHandler{pics: pics, logger: logger}
}

// List menangani GET /projects/:id/pic-groups.
func (h *PicGroupHandler) List(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	list, err := h.pics.ListGroup(c.Context(), exec, c.Params("id"), actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal mengambil PIC Group")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = picGroupMemberJSON(&list[i])
	}
	return c.JSON(response.Success(data))
}

type picGroupRequest struct {
	StatusID string `json:"status_id"`
	UserID   string `json:"user_id"`
}

// Add menangani POST /projects/:id/pic-groups.
func (h *PicGroupHandler) Add(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	var body picGroupRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	if err := h.pics.AddGroupMember(c.Context(), exec, projectID, body.StatusID, body.UserID, actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal menambah anggota PIC Group")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{"project_id": projectID, "status_id": body.StatusID, "user_id": body.UserID}))
}

// Remove menangani DELETE /projects/:id/pic-groups/:statusId/:userId.
func (h *PicGroupHandler) Remove(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")
	if err := h.pics.RemoveGroupMember(c.Context(), exec, projectID, c.Params("statusId"), c.Params("userId"), actorUserID, actorRole); err != nil {
		return h.mapError(c, err, "Gagal menghapus anggota PIC Group")
	}
	return c.JSON(response.Success(fiber.Map{"project_id": projectID}))
}

func picGroupMemberJSON(m *repository.PicGroupMember) fiber.Map {
	return fiber.Map{
		"project_id": m.ProjectID, "status_id": m.StatusID, "user_id": m.UserID,
		"user_name": m.UserName, "user_email": m.UserEmail, "added_by": m.AddedBy, "created_at": m.CreatedAt,
	}
}

func (h *PicGroupHandler) mapError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Hanya Project Manager atau Admin Workspace yang dapat mengelola PIC Group.", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
