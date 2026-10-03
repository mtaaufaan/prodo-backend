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

type picGroupReplaceRequest struct {
	UserIDs []string `json:"user_ids"`
}

// Replace menangani PUT /projects/:id/pic-groups/:statusId -- mengganti
// SELURUH anggota PIC Group status ini (user_ids kosong = Full handoff,
// tombol KOSONGKAN). Satu operasi atomik + satu entri audit.
func (h *PicGroupHandler) Replace(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	var body picGroupReplaceRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	list, err := h.pics.ReplaceGroup(c.Context(), exec, c.Params("id"), c.Params("statusId"), body.UserIDs, actorUserID, actorRole)
	if err != nil {
		return h.mapError(c, err, "Gagal menyimpan PIC Group")
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = picGroupMemberJSON(&list[i])
	}
	return c.JSON(response.Success(data))
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
	case errors.Is(err, domain.ErrPicGroupIneligibleMember):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("PIC_GROUP_MEMBER_INELIGIBLE", "Hanya member project dengan role Editor, Approver, atau Project Manager yang dapat menjadi anggota PIC Group.", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Hanya Project Manager atau Admin Workspace yang dapat mengelola PIC Group.", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
