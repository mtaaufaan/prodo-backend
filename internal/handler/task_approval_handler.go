package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// Approval task (S7A-04, Sprint 7 "Approver Console", IG-150; API_CONTRACT section 19.9).

func approvalRowJSON(a *repository.ApprovalRow) fiber.Map {
	var due any
	if a.DueDate != nil {
		due = a.DueDate.Format("2006-01-02")
	}
	pics := a.PicNames
	if pics == nil {
		pics = []string{}
	}
	return fiber.Map{
		"approval_id": a.ApprovalID, "task_id": a.TaskID, "task_code": a.TaskCode, "title": a.Title, "project_id": a.ProjectID,
		"status_name": a.StatusName, "status_color": a.StatusColor, "priority": a.Priority, "sprint_name": a.SprintName,
		"due_date": due, "pic_names": pics, "comment_count": a.CommentCount, "sub_total": a.SubTotal, "sub_done": a.SubDone,
		"logged_hours": hours(a.LoggedMinutes), "estimate_hours": a.EstimateHours,
		"decision": a.Decision, "decided_at": a.DecidedAt, "decision_note": a.DecisionNote,
	}
}

// ListApprovals menangani GET /me/approvals?project_id=&view=pending|decided&page=&per_page=.
// Respons: {items, total, page, per_page, stats{pending, decided, total_tasks, late}}.
func (h *TaskHandler) ListApprovals(c *fiber.Ctx) error {
	userID, actorRole, uok := middleware.ActorFromContext(c)
	exec, eok := middleware.DBTxFromContext(c)
	if !uok || !eok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	res, err := h.tasks.ListApprovals(c.Context(), exec, c.Query("project_id"), userID, actorRole, c.Query("view", repository.ApprovalViewPending), c.QueryInt("page", 1), c.QueryInt("per_page", 0))
	if err != nil {
		if errors.Is(err, domain.ErrInvalidInput) {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "project_id wajib diisi dan view harus pending atau decided", nil))
		}
		return h.mapError(c, err, "Gagal mengambil antrean approval")
	}
	items := make([]fiber.Map, len(res.Rows))
	for i := range res.Rows {
		items[i] = approvalRowJSON(&res.Rows[i])
	}
	return c.JSON(response.Success(fiber.Map{
		"items": items, "total": res.Total, "page": res.Page, "per_page": res.PerPage,
		"stats": fiber.Map{"pending": res.Stats.Pending, "decided": res.Stats.Decided, "total_tasks": res.Stats.TotalTasks, "late": res.Stats.Late},
	}))
}

type approvalDecisionRequest struct {
	Decision string `json:"decision"`
	Note     string `json:"note"`
}

// DecideApproval menangani POST /tasks/:id/approval {decision: approve|reject, note}.
func (h *TaskHandler) DecideApproval(c *fiber.Ctx) error {
	actorID, actorRole, uok := middleware.ActorFromContext(c)
	exec, eok := middleware.DBTxFromContext(c)
	if !uok || !eok {
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	var body approvalDecisionRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Body request tidak valid", nil))
	}
	res, err := h.tasks.DecideApproval(c.Context(), exec, c.Params("id"), body.Decision, body.Note, actorID, actorRole)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidInput) {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "decision harus approve atau reject dan catatan maksimal 1000 karakter", nil))
		}
		return h.mapError(c, err, "Gagal menyimpan keputusan approval")
	}
	return c.JSON(response.Success(fiber.Map{"task_id": res.TaskID, "task_code": res.TaskCode, "status_name": res.StatusName, "decision": res.Decision}))
}
