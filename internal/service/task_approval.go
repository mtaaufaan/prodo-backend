package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// Approval task (S7A-04, Sprint 7 "Approver Console", implementation_gaps.md IG-150; desain "TM Antrean Approval" +
// "TM Konfirmasi Approval"): Approver, Project Manager, dan Admin Workspace menyetujui (UNDER REVIEW -> DONE) atau
// menolak (kembali ke status kerja sebelumnya, cadangan IN PROGRESS) task yang sedang direview. Perpindahan status
// memakai jalur status yang sama dengan SetStatus (sesi status, regresi, dependency, rule) lewat setStatusWith.

const (
	approvalReviewStatus = "UNDER REVIEW"
	approvalFallbackWork = "IN PROGRESS"
	approvalRejectMin    = 5
	approvalNoteMax      = 1000
)

// taskApprovalStore -- interface didefinisikan di consumer, diimplementasikan *repository.TaskApprovalRepository.
type taskApprovalStore interface {
	LockStatus(ctx context.Context, exec db.Executor, taskID string) (statusName, projectID string, err error)
	StatusByName(ctx context.Context, exec db.Executor, projectID, name string) (*repository.CustomStatus, error)
	LastPhasePics(ctx context.Context, exec db.Executor, taskID, statusID string) ([]string, error)
	Recipients(ctx context.Context, exec db.Executor, taskID, exceptUserID string) ([]string, error)
	SetStatusByApproval(ctx context.Context, exec db.Executor, taskID, statusID string, isDone bool, actorID, actorRole, workspaceID, action, statusBefore, statusAfter, note string) error
	Record(ctx context.Context, exec db.Executor, taskID, approverID, decision, note, statusFrom, statusTo string) (string, error)
	NotifyDecision(ctx context.Context, exec db.Executor, taskID, actorID string, recipientIDs []string, approved bool, note string) error
	Follows(ctx context.Context, exec db.Executor, projectID, userID string) (bool, error)
	List(ctx context.Context, exec db.Executor, projectID, userID, view string, limit, offset int) ([]repository.ApprovalRow, int, error)
	Stats(ctx context.Context, exec db.Executor, projectID, userID string) (repository.ApprovalStats, error)
}

// WithApprovals -- memasang penyimpan approval (dipanggil main.go); tanpa ini DecideApproval/ListApprovals ditolak.
func (s *TaskService) WithApprovals(store taskApprovalStore) *TaskService {
	s.approvals = store
	return s
}

// DecisionResult -- hasil POST /tasks/:id/approval.
type DecisionResult struct {
	TaskID     string
	TaskCode   string
	StatusName string
	Decision   string // approved | rejected
}

// ApprovalPage -- satu halaman antrean/riwayat + total + kartu.
type ApprovalPage struct {
	Rows    []repository.ApprovalRow
	Total   int
	Page    int
	PerPage int
	Stats   repository.ApprovalStats
}

// canDecideApproval -- Approver, Project Manager, dan Admin Workspace (Platform/Group Admin lewat actorRole).
// role kosong TANPA actorRole PA/GA = bukan anggota -> ditolak.
func canDecideApproval(role, actorRole string) bool {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return true
	}
	return role == "admin_workspace" || role == "project_manager" || role == "approver"
}

// authorizeApproval -- role efektif + gerbang "project diikuti" untuk Approver.
func (s *TaskService) authorizeApproval(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	role, err := s.resolveRole(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return err
	}
	if !canDecideApproval(role, actorRole) {
		return fmt.Errorf("service.authorizeApproval: %w", domain.ErrForbidden)
	}
	if role == "approver" {
		follows, err := s.approvals.Follows(ctx, exec, projectID, actorID)
		if err != nil {
			return fmt.Errorf("service.authorizeApproval: %w", err)
		}
		if !follows {
			return fmt.Errorf("service.authorizeApproval: %w", domain.ErrForbidden)
		}
	}
	return nil
}

// validateApprovalInput -- aturan murni: keputusan approve|reject; tolak wajib alasan >= 5 karakter; catatan <= 1000.
func validateApprovalInput(decision, note string) (approve bool, trimmed string, err error) {
	trimmed = strings.TrimSpace(note)
	if decision != "approve" && decision != "reject" {
		return false, "", domain.ErrInvalidInput
	}
	n := utf8.RuneCountInString(trimmed)
	if n > approvalNoteMax {
		return false, "", domain.ErrInvalidInput
	}
	if decision == "reject" && n < approvalRejectMin {
		return false, "", domain.ErrApprovalNoteRequired
	}
	return decision == "approve", trimmed, nil
}

// pickReturnStatus -- status KERJA terakhir sebelum review: sesi terbaru yang bukan UNDER REVIEW dan bukan status
// non-kerja (BACKLOG/BLOCKED/CANCELED/DONE). false bila tidak ada (pemanggil memakai cadangan IN PROGRESS).
func pickReturnStatus(sessions []repository.TaskStatusSession) (string, bool) {
	for i := len(sessions) - 1; i >= 0; i-- {
		switch sessions[i].StatusName {
		case approvalReviewStatus, "BACKLOG", "BLOCKED", "CANCELED", "DONE":
			continue
		}
		return sessions[i].StatusID, true
	}
	return "", false
}

// DecideApproval -- POST /tasks/:id/approval {decision: approve|reject, note}. Setuju -> DONE; tolak -> status kerja
// sebelumnya dengan PIC fase kerja diaktifkan kembali (tanpa memilih PIC). Hanya task UNDER REVIEW; baris task dikunci
// sehingga keputusan serentak diserialkan -- yang kedua ErrNotInReview.
func (s *TaskService) DecideApproval(ctx context.Context, exec db.Executor, taskID, decision, note, actorID, actorRole string) (*DecisionResult, error) {
	if s.approvals == nil {
		return nil, errors.New("service.DecideApproval: penyimpan approval belum dipasang")
	}
	approve, note, err := validateApprovalInput(decision, note)
	if err != nil {
		return nil, fmt.Errorf("service.DecideApproval: %w", err)
	}
	projectID, err := s.repo.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeApproval(ctx, exec, projectID, actorID, actorRole); err != nil {
		return nil, err
	}
	statusName, _, err := s.approvals.LockStatus(ctx, exec, taskID)
	if err != nil {
		return nil, err
	}
	if statusName != approvalReviewStatus {
		return nil, fmt.Errorf("service.DecideApproval: %w", domain.ErrNotInReview)
	}
	task, err := s.repo.Get(ctx, exec, taskID)
	if err != nil {
		return nil, err
	}
	recipients, err := s.approvals.Recipients(ctx, exec, taskID, actorID)
	if err != nil {
		return nil, fmt.Errorf("service.DecideApproval: %w", err)
	}

	target, picIDs, err := s.approvalTarget(ctx, exec, projectID, task, approve, actorID)
	if err != nil {
		return nil, err
	}
	action, decided := "task.rejected", "rejected"
	if approve {
		action, decided = "task.approved", "approved"
	}
	if err := s.setStatusWith(ctx, exec, taskID, target.ID, picIDs, actorID, actorRole, statusOpts{
		fireRules: true, skipPicGroup: true, skipDeps: !approve, approval: &approvalAudit{action: action, note: note},
	}); err != nil {
		return nil, err
	}
	if _, err := s.approvals.Record(ctx, exec, taskID, actorID, decided, note, approvalReviewStatus, target.Name); err != nil {
		return nil, fmt.Errorf("service.DecideApproval: %w", err)
	}
	if err := s.approvals.NotifyDecision(ctx, exec, taskID, actorID, recipients, approve, note); err != nil {
		return nil, fmt.Errorf("service.DecideApproval: %w", err)
	}
	code := ""
	if task.TaskCode != nil {
		code = *task.TaskCode
	}
	return &DecisionResult{TaskID: taskID, TaskCode: code, StatusName: target.Name, Decision: decided}, nil
}

// approvalTarget -- status tujuan dan PIC fase barunya. Setuju: DONE (tanpa PIC). Tolak: status kerja terakhir
// sebelum review (cadangan IN PROGRESS bila tidak ada/sudah dihapus), PIC = PIC fase itu pada kesempatan terakhir,
// cadangan assignee task, lalu pengambil keputusan.
func (s *TaskService) approvalTarget(ctx context.Context, exec db.Executor, projectID string, task *repository.Task, approve bool, actorID string) (*repository.CustomStatus, []string, error) {
	if approve {
		done, err := s.approvals.StatusByName(ctx, exec, projectID, "DONE")
		if err != nil {
			return nil, nil, err
		}
		return done, nil, nil
	}
	var target *repository.CustomStatus
	sessions, err := s.sessions.ListForTask(ctx, exec, task.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("service.approvalTarget: %w", err)
	}
	if id, ok := pickReturnStatus(sessions); ok {
		if st, err := s.statuses.Get(ctx, exec, id); err == nil && !st.IsUndefined {
			target = st
		}
	}
	if target == nil {
		st, err := s.approvals.StatusByName(ctx, exec, projectID, approvalFallbackWork)
		if err != nil {
			return nil, nil, err
		}
		target = st
	}
	pics, err := s.approvals.LastPhasePics(ctx, exec, task.ID, target.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("service.approvalTarget: %w", err)
	}
	if len(pics) == 0 {
		for _, a := range task.Assignees {
			pics = append(pics, a.UserID)
		}
	}
	if len(pics) == 0 {
		pics = []string{actorID}
	}
	return target, pics, nil
}

// ListApprovals -- GET /me/approvals?project_id=&view=pending|decided&page=&per_page=.
func (s *TaskService) ListApprovals(ctx context.Context, exec db.Executor, projectID, userID, actorRole, view string, page, perPage int) (*ApprovalPage, error) {
	if s.approvals == nil {
		return nil, errors.New("service.ListApprovals: penyimpan approval belum dipasang")
	}
	if projectID == "" || (view != repository.ApprovalViewPending && view != repository.ApprovalViewDecided) {
		return nil, fmt.Errorf("service.ListApprovals: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeApproval(ctx, exec, projectID, userID, actorRole); err != nil {
		return nil, err
	}
	page, perPage = NormalizePaging(page, perPage)
	rows, total, err := s.approvals.List(ctx, exec, projectID, userID, view, perPage, (page-1)*perPage)
	if err != nil {
		return nil, fmt.Errorf("service.ListApprovals: %w", err)
	}
	stats, err := s.approvals.Stats(ctx, exec, projectID, userID)
	if err != nil {
		return nil, fmt.Errorf("service.ListApprovals: %w", err)
	}
	return &ApprovalPage{Rows: rows, Total: total, Page: page, PerPage: perPage, Stats: stats}, nil
}
