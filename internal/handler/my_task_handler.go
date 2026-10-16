package handler

import (
	"context"
	"errors"
	"math"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

// myTaskService -- interface didefinisikan di consumer, diimplementasikan *service.MyTaskService.
type myTaskService interface {
	List(ctx context.Context, exec db.Executor, projectID, userID, view string, page, perPage int) (*service.MyTaskPage, error)
}

// MyTaskHandler -- GET /me/tasks (Task Saya Editor, S6E-04, IG-135).
type MyTaskHandler struct {
	tasks  myTaskService
	logger *zap.Logger
}

func NewMyTaskHandler(tasks myTaskService, logger *zap.Logger) *MyTaskHandler {
	return &MyTaskHandler{tasks: tasks, logger: logger}
}

func myTaskJSON(m *repository.MyTaskRow) fiber.Map {
	var due any
	if m.DueDate != nil {
		due = m.DueDate.Format("2006-01-02")
	}
	var next any
	if m.Next != nil {
		next = fiber.Map{"id": m.Next.ID, "name": m.Next.Name, "require_pic": m.Next.RequirePic}
	}
	pics := m.PicNames
	if pics == nil {
		pics = []string{}
	}
	return fiber.Map{
		"task_id": m.TaskID, "task_code": m.TaskCode, "title": m.Title, "project_id": m.ProjectID,
		"status_id": m.StatusID, "status_name": m.StatusName, "status_color": m.StatusColor, "priority": m.Priority,
		"sprint_name": m.SprintName, "due_date": due, "pic_names": pics, "comment_count": m.CommentCount,
		"sub_total": m.SubTotal, "sub_done": m.SubDone,
		"logged_hours": hours(m.LoggedMinutes), "estimate_hours": m.EstimateHours,
		"is_mine": m.IsMine, "is_followed": m.IsFollowed, "is_late": m.IsLate, "next_status": next,
	}
}

// hours -- menit -> jam satu desimal (7.5), sama dengan tampilan "6J / 8J".
func hours(minutes int) float64 { return math.Round(float64(minutes)/60*10) / 10 }

// List menangani GET /me/tasks?project_id=&view=assigned|following&page=&per_page=.
// Respons: {items, total, page, per_page, stats{total, running, logged_hours, late}}.
func (h *MyTaskHandler) List(c *fiber.Ctx) error {
	userID, _, uok := middleware.ActorFromContext(c)
	exec, eok := middleware.DBTxFromContext(c)
	if !uok || !eok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	view := c.Query("view", repository.MyTaskViewAssigned)
	res, err := h.tasks.List(c.Context(), exec, c.Query("project_id"), userID, view, c.QueryInt("page", 1), c.QueryInt("per_page", 0))
	if err != nil {
		if errors.Is(err, domain.ErrInvalidInput) {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "project_id wajib diisi dan view harus assigned atau following", nil))
		}
		h.logger.Error("MyTaskHandler.List gagal", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengambil task saya", nil))
	}
	items := make([]fiber.Map, len(res.Rows))
	for i := range res.Rows {
		items[i] = myTaskJSON(&res.Rows[i])
	}
	return c.JSON(response.Success(fiber.Map{
		"items": items, "total": res.Total, "page": res.Page, "per_page": res.PerPage,
		"stats": fiber.Map{
			"total": res.Stats.Total, "running": res.Stats.Running, "logged_hours": hours(res.Stats.LoggedMinutes), "late": res.Stats.Late,
		},
	}))
}
