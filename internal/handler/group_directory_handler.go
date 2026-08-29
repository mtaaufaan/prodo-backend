package handler

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// GroupDirectoryHandler -- S4P-34, US-083: GET /platform/groups.
type GroupDirectoryHandler struct {
	directory *service.GroupDirectoryService
	logger    *zap.Logger
}

func NewGroupDirectoryHandler(directory *service.GroupDirectoryService, logger *zap.Logger) *GroupDirectoryHandler {
	return &GroupDirectoryHandler{directory: directory, logger: logger}
}

// List menangani GET /platform/groups?q=... -- gerbang HTTP
// RequirePlatformRole(platform_admin, group_admin) di main.go; scoping
// GA-vs-PA sesungguhnya ada di query (lihat GroupDirectoryRepository.List).
func (h *GroupDirectoryHandler) List(c *fiber.Ctx) error {
	actorUserID, actorRole, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("GroupDirectoryHandler.List dipanggil tanpa RequirePlatformRole -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}

	entries, err := h.directory.List(c.Context(), actorUserID, actorRole, c.Query("q"))
	if err != nil {
		h.logger.Error("gagal mengambil direktori grup", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil daftar grup", nil))
	}

	data := make([]fiber.Map, len(entries))
	for i := range entries {
		e := &entries[i]
		data[i] = fiber.Map{
			"id":        e.ID,
			"name":      e.Name,
			"tier":      e.TierName,
			"ga_names":  e.GANames,
			"org_count": e.OrgCount,
		}
	}
	return c.JSON(response.Success(data))
}
