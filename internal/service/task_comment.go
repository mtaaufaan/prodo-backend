package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

const (
	commentMinRunes = 2
	commentMaxRunes = 1000
)

// commentStore -- interface didefinisikan di consumer, diimplementasikan
// *repository.TaskCommentRepository (S6E-04, IG-135).
type commentStore interface {
	GetTaskContext(ctx context.Context, exec db.Executor, taskID string) (*repository.TaskCommentContext, error)
	ActorRoles(ctx context.Context, exec db.Executor, projectID, userID string) (repository.ActorProjectRoles, error)
	Create(ctx context.Context, exec db.Executor, taskID, userID, text string) (*repository.CommentRecord, error)
	Get(ctx context.Context, exec db.Executor, commentID string) (*repository.CommentRecord, error)
	List(ctx context.Context, exec db.Executor, taskID string) ([]repository.CommentRecord, error)
	UpdateText(ctx context.Context, exec db.Executor, commentID, text string) error
	SoftDelete(ctx context.Context, exec db.Executor, commentID string) error
	CountForTask(ctx context.Context, exec db.Executor, taskID string) (int, error)
	EnsureFollower(ctx context.Context, exec db.Executor, taskID, userID, source string) error
	Unfollow(ctx context.Context, exec db.Executor, taskID, userID string) error
	NotifyRecipients(ctx context.Context, exec db.Executor, taskID string) ([]string, error)
	MentionCandidates(ctx context.Context, exec db.Executor, projectID string) ([]repository.MentionCandidate, error)
	ExistingMentionUsers(ctx context.Context, exec db.Executor, commentID string) (map[string]bool, error)
	MentionBlock(ctx context.Context, exec db.Executor, userID, workspaceID string, cooldownMin int) (bool, time.Time, error)
	InsertMention(ctx context.Context, exec db.Executor, commentID, taskID, userID, actorID, state string, windowEnds *time.Time) error
	InsertNotification(ctx context.Context, exec db.Executor, userID, actorID, typ, entityType, taskID, title, body string) error
	AuditComment(ctx context.Context, exec db.Executor, actorID, actorRole, action string, c *repository.TaskCommentContext, before, after map[string]any) error
}

// TaskCommentService -- komentar task, mention, follower (S6E-04, IG-135; US-030/031/033).
type TaskCommentService struct {
	store commentStore
	now   func() time.Time
}

func NewTaskCommentService(store commentStore) *TaskCommentService {
	return &TaskCommentService{store: store, now: time.Now}
}

// Status pengiriman mention (task_comment_mentions.digest_state).
const (
	mentionSent    = "sent"
	mentionPending = "pending"
	mentionDropped = "dropped"
)

// CommentView -- komentar + izin untuk pemirsa (flag kemampuan, bukan penegakan).
type CommentView struct {
	repository.CommentRecord
	CanEdit   bool
	CanDelete bool
}

// CreateResult -- hasil POST komentar.
type CreateResult struct {
	Comment      CommentView
	CommentCount int
	// Mentions -- nama tampil user yang berhasil di-resolusi; Notified -- berapa di antaranya yang
	// akan diberi notifikasi (langsung atau lewat ringkasan; yang ditekan tidak dihitung).
	Mentions []string
	Notified int
}

func validateCommentText(text string) (string, error) {
	t := strings.TrimSpace(text)
	if n := utf8.RuneCountInString(t); n < commentMinRunes || n > commentMaxRunes {
		return "", fmt.Errorf("service.validateCommentText: %w", domain.ErrCommentInvalid)
	}
	return t, nil
}

// effectiveRole -- label role untuk audit (actor_role).
func effectiveRole(r repository.ActorProjectRoles) string {
	switch {
	case r.IsPM:
		return "project_manager"
	case r.WorkspaceRole != "":
		return r.WorkspaceRole
	default:
		return r.ProjectRole
	}
}

// canComment -- editor, approver, PM, Admin Workspace, Division Viewer (PRD §2.1); viewer tidak.
func canComment(r repository.ActorProjectRoles) bool {
	switch r.WorkspaceRole {
	case "admin_workspace", "project_manager", "editor", "approver", "division_viewer":
		return true
	}
	return r.IsPM || r.ProjectRole == "editor" || r.ProjectRole == "approver"
}

func canDeleteOthers(r repository.ActorProjectRoles) bool {
	return r.IsPM || r.WorkspaceRole == "admin_workspace"
}

func (s *TaskCommentService) view(c *repository.CommentRecord, userID string, roles repository.ActorProjectRoles) CommentView {
	own := c.UserID == userID
	return CommentView{CommentRecord: *c, CanEdit: own && !c.Locked, CanDelete: own || canDeleteOthers(roles)}
}

// Create -- simpan komentar, auto-follow penulis + target mention, kirim notifikasi, catat audit.
func (s *TaskCommentService) Create(ctx context.Context, exec db.Executor, taskID, userID, text string) (*CreateResult, error) {
	clean, err := validateCommentText(text)
	if err != nil {
		return nil, err
	}
	tc, err := s.store.GetTaskContext(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.CreateComment: %w", err)
	}
	roles, err := s.store.ActorRoles(ctx, exec, tc.ProjectID, userID)
	if err != nil {
		return nil, fmt.Errorf("service.CreateComment: %w", err)
	}
	if !canComment(roles) {
		return nil, fmt.Errorf("service.CreateComment: %w", domain.ErrCommentForbidden)
	}

	rec, err := s.store.Create(ctx, exec, taskID, userID, clean)
	if err != nil {
		return nil, fmt.Errorf("service.CreateComment: %w", err)
	}
	if err := s.store.EnsureFollower(ctx, exec, taskID, userID, "comment"); err != nil {
		return nil, fmt.Errorf("service.CreateComment: %w", err)
	}

	mentioned, notified, err := s.processMentions(ctx, exec, tc, rec, userID, clean, map[string]bool{})
	if err != nil {
		return nil, err
	}
	if err := s.notifyCommentRecipients(ctx, exec, tc, userID, mentioned); err != nil {
		return nil, err
	}

	names := make([]string, len(mentioned))
	for i, m := range mentioned {
		names[i] = m.DisplayName
	}
	if err := s.store.AuditComment(ctx, exec, userID, effectiveRole(roles), "comment.created", tc, nil,
		map[string]any{"comment_id": rec.ID, "length": utf8.RuneCountInString(clean), "mentions": names}); err != nil {
		return nil, fmt.Errorf("service.CreateComment: audit: %w", err)
	}
	count, err := s.store.CountForTask(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.CreateComment: %w", err)
	}
	return &CreateResult{Comment: s.view(rec, userID, roles), CommentCount: count, Mentions: names, Notified: notified}, nil
}

// processMentions -- resolusi mention lalu kirim/akumulasi/tekan notifikasi sesuai cooldown.
// skip berisi user yang sudah punya catatan mention pada komentar ini (edit tidak mengirim ulang).
func (s *TaskCommentService) processMentions(ctx context.Context, exec db.Executor, tc *repository.TaskCommentContext,
	rec *repository.CommentRecord, authorID, text string, skip map[string]bool,
) (resolved []repository.MentionCandidate, notified int, err error) {
	tokens := ParseMentionTokens(text)
	if len(tokens) == 0 {
		return nil, 0, nil
	}
	cands, err := s.store.MentionCandidates(ctx, exec, tc.ProjectID)
	if err != nil {
		return nil, 0, fmt.Errorf("service.processMentions: %w", err)
	}
	for _, m := range ResolveMentions(tokens, cands) {
		if m.UserID == authorID || skip[m.UserID] {
			continue
		}
		resolved = append(resolved, m)
		if err := s.store.EnsureFollower(ctx, exec, tc.TaskID, m.UserID, "mention"); err != nil {
			return nil, 0, fmt.Errorf("service.processMentions: %w", err)
		}
		blocked, windowEnds, err := s.store.MentionBlock(ctx, exec, m.UserID, tc.WorkspaceID, tc.CooldownMin)
		if err != nil {
			return nil, 0, fmt.Errorf("service.processMentions: %w", err)
		}
		switch {
		case !blocked:
			if err := s.store.InsertMention(ctx, exec, rec.ID, tc.TaskID, m.UserID, authorID, mentionSent, nil); err != nil {
				return nil, 0, fmt.Errorf("service.processMentions: %w", err)
			}
			if err := s.store.InsertNotification(ctx, exec, m.UserID, authorID, "task_mention", "task", tc.TaskID,
				"Anda di-mention di "+tc.TaskCode, rec.AuthorName+` menyebut Anda di task "`+tc.TaskTitle+`".`); err != nil {
				return nil, 0, fmt.Errorf("service.processMentions: %w", err)
			}
			notified++
		case tc.DigestEnabled:
			if err := s.store.InsertMention(ctx, exec, rec.ID, tc.TaskID, m.UserID, authorID, mentionPending, &windowEnds); err != nil {
				return nil, 0, fmt.Errorf("service.processMentions: %w", err)
			}
			notified++
		default:
			if err := s.store.InsertMention(ctx, exec, rec.ID, tc.TaskID, m.UserID, authorID, mentionDropped, nil); err != nil {
				return nil, 0, fmt.Errorf("service.processMentions: %w", err)
			}
		}
	}
	return resolved, notified, nil
}

// notifyCommentRecipients -- assignee, PIC aktif, dan follower task (kecuali penulis dan target
// mention, yang sudah ditangani aturan cooldown mention).
func (s *TaskCommentService) notifyCommentRecipients(ctx context.Context, exec db.Executor, tc *repository.TaskCommentContext,
	authorID string, mentioned []repository.MentionCandidate,
) error {
	ids, err := s.store.NotifyRecipients(ctx, exec, tc.TaskID)
	if err != nil {
		return fmt.Errorf("service.notifyCommentRecipients: %w", err)
	}
	skip := map[string]bool{authorID: true}
	for _, m := range mentioned {
		skip[m.UserID] = true
	}
	for _, id := range ids {
		if skip[id] {
			continue
		}
		skip[id] = true
		if err := s.store.InsertNotification(ctx, exec, id, authorID, "task_comment", "task", tc.TaskID,
			"Komentar baru di "+tc.TaskCode, `Ada komentar baru pada task "`+tc.TaskTitle+`".`); err != nil {
			return fmt.Errorf("service.notifyCommentRecipients: %w", err)
		}
	}
	return nil
}

// List -- komentar aktif task dengan flag edit/hapus untuk pemirsa.
func (s *TaskCommentService) List(ctx context.Context, exec db.Executor, taskID, userID string) ([]CommentView, error) {
	tc, err := s.store.GetTaskContext(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.ListComments: %w", err)
	}
	roles, err := s.store.ActorRoles(ctx, exec, tc.ProjectID, userID)
	if err != nil {
		return nil, fmt.Errorf("service.ListComments: %w", err)
	}
	list, err := s.store.List(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.ListComments: %w", err)
	}
	out := make([]CommentView, len(list))
	for i := range list {
		out[i] = s.view(&list[i], userID, roles)
	}
	return out, nil
}

// Edit -- hanya penulis, dalam jendela 3 menit waktu server (US-031). Mention BARU diproses;
// mention yang sudah tercatat tidak mengirim notifikasi ulang (US-030).
func (s *TaskCommentService) Edit(ctx context.Context, exec db.Executor, commentID, userID, text string) (*CommentView, error) {
	clean, err := validateCommentText(text)
	if err != nil {
		return nil, err
	}
	rec, err := s.store.Get(ctx, exec, commentID)
	if err != nil {
		return nil, fmt.Errorf("service.EditComment: %w", err)
	}
	if rec.UserID != userID {
		return nil, fmt.Errorf("service.EditComment: %w", domain.ErrCommentForbidden)
	}
	if rec.Locked {
		return nil, fmt.Errorf("service.EditComment: %w", domain.ErrCommentEditLocked)
	}
	tc, err := s.store.GetTaskContext(ctx, exec, rec.TaskID)
	if err != nil {
		return nil, fmt.Errorf("service.EditComment: %w", err)
	}
	roles, err := s.store.ActorRoles(ctx, exec, tc.ProjectID, userID)
	if err != nil {
		return nil, fmt.Errorf("service.EditComment: %w", err)
	}
	if err := s.store.UpdateText(ctx, exec, commentID, clean); err != nil {
		return nil, fmt.Errorf("service.EditComment: %w", err)
	}
	existing, err := s.store.ExistingMentionUsers(ctx, exec, commentID)
	if err != nil {
		return nil, fmt.Errorf("service.EditComment: %w", err)
	}
	if _, _, err := s.processMentions(ctx, exec, tc, rec, userID, clean, existing); err != nil {
		return nil, err
	}
	if err := s.store.AuditComment(ctx, exec, userID, effectiveRole(roles), "comment.edited", tc,
		map[string]any{"comment_id": commentID, "text": rec.Text}, map[string]any{"comment_id": commentID, "text": clean}); err != nil {
		return nil, fmt.Errorf("service.EditComment: audit: %w", err)
	}
	updated, err := s.store.Get(ctx, exec, commentID)
	if err != nil {
		return nil, fmt.Errorf("service.EditComment: %w", err)
	}
	v := s.view(updated, userID, roles)
	return &v, nil
}

// Delete -- soft delete oleh penulis, PM project, atau Admin Workspace.
func (s *TaskCommentService) Delete(ctx context.Context, exec db.Executor, commentID, userID string) error {
	rec, err := s.store.Get(ctx, exec, commentID)
	if err != nil {
		return fmt.Errorf("service.DeleteComment: %w", err)
	}
	tc, err := s.store.GetTaskContext(ctx, exec, rec.TaskID)
	if err != nil {
		return fmt.Errorf("service.DeleteComment: %w", err)
	}
	roles, err := s.store.ActorRoles(ctx, exec, tc.ProjectID, userID)
	if err != nil {
		return fmt.Errorf("service.DeleteComment: %w", err)
	}
	if rec.UserID != userID && !canDeleteOthers(roles) {
		return fmt.Errorf("service.DeleteComment: %w", domain.ErrCommentForbidden)
	}
	if err := s.store.SoftDelete(ctx, exec, commentID); err != nil {
		return fmt.Errorf("service.DeleteComment: %w", err)
	}
	if err := s.store.AuditComment(ctx, exec, userID, effectiveRole(roles), "comment.deleted", tc,
		map[string]any{"comment_id": commentID, "author_id": rec.UserID, "length": utf8.RuneCountInString(rec.Text)}, nil); err != nil {
		return fmt.Errorf("service.DeleteComment: audit: %w", err)
	}
	return nil
}

// SetFollow -- ikuti / berhenti mengikuti task (manual). Berhenti tidak mencabut penugasan.
func (s *TaskCommentService) SetFollow(ctx context.Context, exec db.Executor, taskID, userID string, follow bool) error {
	if _, err := s.store.GetTaskContext(ctx, exec, taskID); err != nil {
		return fmt.Errorf("service.SetFollow: %w", err)
	}
	if follow {
		return s.store.EnsureFollower(ctx, exec, taskID, userID, "manual")
	}
	return s.store.Unfollow(ctx, exec, taskID, userID)
}
