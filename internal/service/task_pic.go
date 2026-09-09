// Package service -- TaskPicService (Task Management Core Phase 2,
// US-017/017b). Acknowledge + riwayat PIC + CRUD PIC Group. SetStatus
// (pembuatan fase PIC baru) ada di TaskService.SetStatus -- perpindahan
// status dan penetapan PIC adalah SATU operasi (AC), tidak dipisah.
package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// taskPicHistoryRepository -- interface didefinisikan di consumer.
type taskPicHistoryRepository interface {
	ListActiveForTask(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskPicPhase, error)
	ListHistoryForTask(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskPicPhase, error)
	Acknowledge(ctx context.Context, exec db.Executor, taskID, userID string) (bool, error)
	ListGroupForStatus(ctx context.Context, exec db.Executor, projectID, statusID string) ([]repository.PicGroupMember, error)
	ListGroupForProject(ctx context.Context, exec db.Executor, projectID string) ([]repository.PicGroupMember, error)
	AddGroupMember(ctx context.Context, exec db.Executor, projectID, statusID, userID string, addedBy string) error
	RemoveGroupMember(ctx context.Context, exec db.Executor, projectID, statusID, userID string) error
}

type TaskPicService struct {
	repo         taskPicHistoryRepository
	projects     taskProjectResolver
	rbac         sprintWorkspaceRoleChecker
	projectRoles sprintProjectRoleChecker
}

// NewTaskPicService -- ListActive/ListHistory/Acknowledge SENGAJA tidak
// authorize() eksplisit di sini (beda dari ListGroup/AddGroupMember/
// RemoveGroupMember) -- RLS task_pic_phases_all sudah menyaring lewat
// project membership task induk, dan Acknowledge sendiri cuma bisa
// menyentuh baris milik actor sendiri (WHERE user_id = actor di query).
func NewTaskPicService(repo taskPicHistoryRepository, projects taskProjectResolver, rbac sprintWorkspaceRoleChecker, projectRoles sprintProjectRoleChecker) *TaskPicService {
	return &TaskPicService{repo: repo, projects: projects, rbac: rbac, projectRoles: projectRoles}
}

// authorizeProject -- identik TaskService/SprintService.authorize
// (viewer/division_viewer ditolak untuk tulis) -- dipakai PIC Group CRUD.
// Lihat komentar sprint.go/task.go kenapa ini duplikasi kecil disengaja.
func (s *TaskPicService) authorizeProject(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return nil
	}
	if role, found, err := s.projectRoles.GetRole(ctx, exec, projectID, actorID); err == nil && found {
		if role == "viewer" {
			return fmt.Errorf("service.authorizeProject: %w", domain.ErrForbidden)
		}
		return nil
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("service.authorizeProject: %w", err)
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("service.authorizeProject: %w", err)
	}
	if role == "viewer" || role == "division_viewer" {
		return fmt.Errorf("service.authorizeProject: %w", domain.ErrForbidden)
	}
	return nil
}

// authorizePicGroupManage -- HANYA AW/PM (glossary: "PM dan AW dapat
// menambah/menghapus user dari PIC Group") -- lebih ketat dari
// authorizeProject biasa (yang cuma menolak viewer).
func (s *TaskPicService) authorizePicGroupManage(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return nil
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("service.authorizePicGroupManage: %w", err)
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("service.authorizePicGroupManage: %w", err)
	}
	if role != "admin_workspace" && role != "project_manager" {
		return fmt.Errorf("service.authorizePicGroupManage: %w", domain.ErrForbidden)
	}
	return nil
}

// ListActive -- PIC aktif saat ini (task detail response).
func (s *TaskPicService) ListActive(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskPicPhase, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.ListActive: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.ListActiveForTask(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.ListActive: %w", err)
	}
	return list, nil
}

// ListHistory -- FE PICHistoryTab, seluruh fase (terbaru dulu).
func (s *TaskPicService) ListHistory(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskPicPhase, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.ListHistory: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.ListHistoryForTask(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.ListHistory: %w", err)
	}
	return list, nil
}

// Acknowledge -- S4-33, PIC aktif konfirmasi menerima serah terima. TIDAK
// ada gate role tambahan -- siapa pun yang benar-benar PIC aktif task ini
// boleh acknowledge (viewer pun bukan pengecualian di sini karena viewer
// tidak pernah bisa jadi PIC sejak awal, dipilihnya sudah tersaring).
func (s *TaskPicService) Acknowledge(ctx context.Context, exec db.Executor, taskID, actorID string) error {
	if taskID == "" {
		return fmt.Errorf("service.Acknowledge: %w", domain.ErrInvalidInput)
	}
	ok, err := s.repo.Acknowledge(ctx, exec, taskID, actorID)
	if err != nil {
		return fmt.Errorf("service.Acknowledge: %w", err)
	}
	if !ok {
		return fmt.Errorf("service.Acknowledge: %w", domain.ErrNotActivePic)
	}
	return nil
}

// ListGroup -- seluruh konfigurasi PIC Group project (halaman pengaturan).
func (s *TaskPicService) ListGroup(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) ([]repository.PicGroupMember, error) {
	if projectID == "" {
		return nil, fmt.Errorf("service.ListGroup: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeProject(ctx, exec, projectID, actorID, actorRole); err != nil {
		return nil, err
	}
	list, err := s.repo.ListGroupForProject(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.ListGroup: %w", err)
	}
	return list, nil
}

func (s *TaskPicService) AddGroupMember(ctx context.Context, exec db.Executor, projectID, statusID, userID, actorID, actorRole string) error {
	if projectID == "" || statusID == "" || userID == "" {
		return fmt.Errorf("service.AddGroupMember: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizePicGroupManage(ctx, exec, projectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.AddGroupMember(ctx, exec, projectID, statusID, userID, actorID); err != nil {
		return fmt.Errorf("service.AddGroupMember: %w", err)
	}
	return nil
}

func (s *TaskPicService) RemoveGroupMember(ctx context.Context, exec db.Executor, projectID, statusID, userID, actorID, actorRole string) error {
	if projectID == "" || statusID == "" || userID == "" {
		return fmt.Errorf("service.RemoveGroupMember: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizePicGroupManage(ctx, exec, projectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.RemoveGroupMember(ctx, exec, projectID, statusID, userID); err != nil {
		return fmt.Errorf("service.RemoveGroupMember: %w", err)
	}
	return nil
}
