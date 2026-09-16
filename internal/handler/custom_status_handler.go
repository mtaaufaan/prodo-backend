package handler

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// CustomStatusHandler -- Task Management Core Phase 1; toggle
// require_start_confirmation Phase 4 (US-018b); CRUD template workspace
// S4W-05 (US-020/021, "AW Custom Status.dc.html"/"AW Add Status.dc.html").
type CustomStatusHandler struct {
	statuses *service.CustomStatusService
	logger   *zap.Logger
}

func NewCustomStatusHandler(statuses *service.CustomStatusService, logger *zap.Logger) *CustomStatusHandler {
	return &CustomStatusHandler{statuses: statuses, logger: logger}
}

// ListForWorkspace menangani GET /workspaces/:wsId/statuses -- kolom papan
// Kanban (status sistem, di-seed otomatis saat workspace dibuat) SEKALIGUS
// data tabel "AW Custom Status" (task_count dipakai panel Kelola, US-021).
func (h *CustomStatusHandler) ListForWorkspace(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("CustomStatusHandler.ListForWorkspace dipanggil tanpa DBContextMiddleware")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	list, err := h.statuses.ListForWorkspace(c.Context(), exec, workspaceID)
	if err != nil {
		h.logger.Error("gagal mengambil daftar status", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil daftar status", nil))
	}
	data := make([]fiber.Map, len(list))
	for i := range list {
		data[i] = customStatusJSON(&list[i])
	}
	return c.JSON(response.Success(data))
}

type createStatusRequest struct {
	Name       string `json:"name"`
	ColorToken string `json:"color_token"`
	Position   int    `json:"position"`
}

// Create menangani POST /workspaces/:wsId/statuses (S4W-05, "AW Add
// Status.dc.html") -- hanya Admin Workspace.
func (h *CustomStatusHandler) Create(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("CustomStatusHandler.Create dipanggil tanpa DBContextMiddleware")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	workspaceID := c.Params("wsId")

	var req createStatusRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	created, err := h.statuses.Create(c.Context(), exec, workspaceID, req.Name, req.ColorToken, req.Position, actorUserID, actorRole)
	if err != nil {
		return h.mapCustomStatusError(c, err, "Gagal menambah status")
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(customStatusJSON(created)))
}

type updateAppearanceRequest struct {
	Name       string `json:"name"`
	ColorToken string `json:"color_token"`
}

// UpdateAppearance menangani PUT /statuses/:id/appearance (S4W-05, panel
// "KELOLA STATUS TEMPLATE" -> SIMPAN PERUBAHAN) -- hanya Admin Workspace.
func (h *CustomStatusHandler) UpdateAppearance(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	statusID := c.Params("id")

	var req updateAppearanceRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	if err := h.statuses.UpdateNameColor(c.Context(), exec, statusID, req.Name, req.ColorToken, actorUserID, actorRole); err != nil {
		return h.mapCustomStatusError(c, err, "Gagal menyimpan perubahan status")
	}
	return c.JSON(response.Success(fiber.Map{"id": statusID}))
}

type moveStatusRequest struct {
	Direction string `json:"direction"`
}

// Move menangani POST /statuses/:id/move (S4W-05, tombol ▲▼ "URUT") --
// hanya Admin Workspace.
func (h *CustomStatusHandler) Move(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	statusID := c.Params("id")

	var req moveStatusRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	var direction int
	switch strings.ToLower(strings.TrimSpace(req.Direction)) {
	case "up":
		direction = -1
	case "down":
		direction = 1
	default:
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "direction wajib \"up\" atau \"down\"", nil))
	}
	if err := h.statuses.Move(c.Context(), exec, statusID, direction, actorUserID, actorRole); err != nil {
		return h.mapCustomStatusError(c, err, "Gagal mengubah urutan status")
	}
	return c.JSON(response.Success(fiber.Map{"id": statusID}))
}

// Undefine menangani POST /statuses/:id/undefine (S4W-05, US-021, "⊘
// JADIKAN UNDEFINED") -- hanya Admin Workspace.
func (h *CustomStatusHandler) Undefine(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	statusID := c.Params("id")

	if err := h.statuses.Undefine(c.Context(), exec, statusID, actorUserID, actorRole); err != nil {
		return h.mapCustomStatusError(c, err, "Gagal menjadikan status UNDEFINED")
	}
	return c.JSON(response.Success(fiber.Map{"id": statusID}))
}

// Restore menangani POST /statuses/:id/restore (S4W-05, US-020, "↺
// PULIHKAN KE TEMPLATE") -- hanya Admin Workspace.
func (h *CustomStatusHandler) Restore(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	statusID := c.Params("id")

	if err := h.statuses.Restore(c.Context(), exec, statusID, actorUserID, actorRole); err != nil {
		return h.mapCustomStatusError(c, err, "Gagal memulihkan status")
	}
	return c.JSON(response.Success(fiber.Map{"id": statusID}))
}

type requireStartConfirmationRequest struct {
	RequireStartConfirmation bool `json:"require_start_confirmation"`
}

// UpdateRequireStartConfirmation menangani PUT /statuses/:id (Phase 4,
// US-018b/S4-64) -- PM dan Admin Workspace (perilaku existing, TIDAK
// dipersempit ke AW-only seperti CRUD template S4W-05 di atas).
func (h *CustomStatusHandler) UpdateRequireStartConfirmation(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	statusID := c.Params("id")

	var body requireStartConfirmationRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	if err := h.statuses.SetRequireStartConfirmation(c.Context(), exec, statusID, body.RequireStartConfirmation, actorUserID, actorRole); err != nil {
		return h.mapCustomStatusError(c, err, "Gagal mengubah setting status")
	}
	return c.JSON(response.Success(fiber.Map{"id": statusID, "require_start_confirmation": body.RequireStartConfirmation}))
}

func (h *CustomStatusHandler) mapCustomStatusError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang mengubah status workspace ini.", nil))
	case errors.Is(err, domain.ErrCustomStatusNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Status tidak ditemukan", nil))
	case errors.Is(err, domain.ErrCustomStatusNameTaken):
		return c.Status(fiber.StatusConflict).JSON(response.Error("STATUS_NAME_TAKEN", "Status ini sudah ada di template workspace", nil))
	case errors.Is(err, domain.ErrCustomStatusLimitReached):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("STATUS_LIMIT_REACHED", "Batas 12 status per workspace tercapai -- hapus status yang tidak dipakai lebih dulu", nil))
	case errors.Is(err, domain.ErrCustomStatusIsSystem):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("STATUS_IS_SYSTEM", "Status sistem wajib ada di setiap project dan tidak dapat dihapus dari template", nil))
	case errors.Is(err, domain.ErrCustomStatusNotUndefined):
		return c.Status(fiber.StatusConflict).JSON(response.Error("STATUS_NOT_UNDEFINED", "Status ini tidak sedang UNDEFINED", nil))
	case errors.Is(err, domain.ErrCustomStatusNotTrackable):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("STATUS_NOT_TRACKABLE", "BACKLOG, DONE, dan BLOCKED tidak dilacak waktunya -- konfirmasi mulai tidak berlaku", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}

func customStatusJSON(s *repository.CustomStatus) fiber.Map {
	return fiber.Map{
		"id": s.ID, "name": s.Name, "color_token": s.ColorToken, "position": s.Position,
		"is_system": s.IsSystem, "is_undefined": s.IsUndefined, "require_start_confirmation": s.RequireStartConfirmation,
		"task_count": s.TaskCount,
	}
}
