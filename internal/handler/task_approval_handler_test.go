package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// S7A-04 (IG-150): pemetaan galat approval dan bentuk JSON baris antrean (kontrak API_CONTRACT section 19.9).

func TestTaskHandler_MapError_Approval(t *testing.T) {
	h := &TaskHandler{logger: zap.NewNop()}
	cases := []struct {
		err      error
		status   int
		wantCode string
	}{
		{domain.ErrNotInReview, http.StatusConflict, "NOT_IN_REVIEW"},
		{fmt.Errorf("service.DecideApproval: %w", domain.ErrNotInReview), http.StatusConflict, "NOT_IN_REVIEW"},
		{domain.ErrApprovalNoteRequired, http.StatusUnprocessableEntity, "VALIDATION_ERROR"},
		{domain.ErrForbidden, http.StatusForbidden, "FORBIDDEN"},
		{domain.ErrTaskNotFound, http.StatusNotFound, "NOT_FOUND"},
		{errors.New("boom"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		app := fiber.New()
		app.Get("/x", func(c *fiber.Ctx) error { return h.mapError(c, tc.err, "gagal") })
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", http.NoBody))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != tc.status || env.Error.Code != tc.wantCode {
			t.Errorf("%v -> %d %q, want %d %q", tc.err, resp.StatusCode, env.Error.Code, tc.status, tc.wantCode)
		}
	}
}

func TestApprovalRowJSON(t *testing.T) {
	due := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	decided := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)
	id, decision, note := "ap-1", "rejected", "Perlu perbaikan."
	pending := approvalRowJSON(&repository.ApprovalRow{TaskID: "t1", TaskCode: "QST-101", Title: "Judul", StatusName: "UNDER REVIEW", Priority: "high", DueDate: &due, LoggedMinutes: 450, EstimateHours: 8})
	for _, k := range []string{"approval_id", "task_id", "task_code", "title", "project_id", "status_name", "status_color", "priority", "sprint_name", "due_date", "pic_names", "comment_count", "sub_total", "sub_done", "logged_hours", "estimate_hours", "decision", "decided_at", "decision_note"} {
		if _, ok := pending[k]; !ok {
			t.Errorf("kunci %q hilang dari item antrean", k)
		}
	}
	if pending["due_date"] != "2026-10-12" || pending["logged_hours"] != 7.5 {
		t.Errorf("due_date/logged_hours = %v / %v", pending["due_date"], pending["logged_hours"])
	}
	if pics, _ := pending["pic_names"].([]string); pics == nil || len(pics) != 0 {
		t.Errorf("pic_names harus [] bukan null: %#v", pending["pic_names"])
	}
	decidedRow := approvalRowJSON(&repository.ApprovalRow{TaskID: "t1", ApprovalID: &id, Decision: &decision, DecidedAt: &decided, DecisionNote: &note})
	if *(decidedRow["approval_id"].(*string)) != "ap-1" || *(decidedRow["decision"].(*string)) != "rejected" {
		t.Errorf("item riwayat = %#v", decidedRow)
	}
}
