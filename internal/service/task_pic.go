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
	AddPic(ctx context.Context, exec db.Executor, taskID, statusID, statusName, userID, actorID, actorRole, workspaceID string) error
	RemovePic(ctx context.Context, exec db.Executor, taskID, statusName, userID, actorID, actorRole, workspaceID string) (bool, error)
	HandoffPic(ctx context.Context, exec db.Executor, taskID, statusID, statusName string, fromUserIDs []string, toUserID, actorID, actorRole, workspaceID string) error
	ListGroupForStatus(ctx context.Context, exec db.Executor, projectID, statusID string) ([]repository.PicGroupMember, error)
	ListGroupForProject(ctx context.Context, exec db.Executor, projectID string) ([]repository.PicGroupMember, error)
	AddGroupMember(ctx context.Context, exec db.Executor, projectID, statusID, userID string, addedBy string) error
	RemoveGroupMember(ctx context.Context, exec db.Executor, projectID, statusID, userID string) error
}

// taskPicTaskResolver -- reuse TaskRepository.Get (IG-97 susulan) supaya
// AddPic/RemovePic/HandoffPic tahu projectID+statusID+statusName task
// tanpa client mengirimnya manual (selalu beroperasi pada fase status
// task SAAT INI, sama seperti Acknowledge/ListActive).
type taskPicTaskResolver interface {
	Get(ctx context.Context, exec db.Executor, taskID string) (*repository.Task, error)
}

type TaskPicService struct {
	repo         taskPicHistoryRepository
	tasks        taskPicTaskResolver
	projects     taskProjectResolver
	rbac         sprintWorkspaceRoleChecker
	projectRoles sprintProjectRoleChecker
}

// NewTaskPicService -- ListActive/ListHistory/Acknowledge SENGAJA tidak
// authorize() eksplisit di sini (beda dari ListGroup/AddGroupMember/
// RemoveGroupMember) -- RLS task_pic_phases_all sudah menyaring lewat
// project membership task induk, dan Acknowledge sendiri cuma bisa
// menyentuh baris milik actor sendiri (WHERE user_id = actor di query).
func NewTaskPicService(repo taskPicHistoryRepository, tasks taskPicTaskResolver, projects taskProjectResolver, rbac sprintWorkspaceRoleChecker, projectRoles sprintProjectRoleChecker) *TaskPicService {
	return &TaskPicService{repo: repo, tasks: tasks, projects: projects, rbac: rbac, projectRoles: projectRoles}
}

// authorizeProject -- identik TaskService.resolveRole (viewer/
// division_viewer ditolak, role yang di-resolve DIKEMBALIKAN supaya
// pemanggil bisa reuse untuk isFullPicMode() tanpa resolve dua kali --
// pola sama TaskService/TaskDependencyService.authorize). Dipakai PIC
// Group CRUD (ListGroup) dan AddPic/HandoffPic (IG-97 susulan) -- Lihat
// komentar sprint.go/task.go kenapa duplikasi kecil ini disengaja.
func (s *TaskPicService) authorizeProject(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) (string, error) {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return "", nil
	}
	if role, found, err := s.projectRoles.GetRole(ctx, exec, projectID, actorID); err == nil && found {
		if role == "viewer" {
			return "", fmt.Errorf("service.authorizeProject: %w", domain.ErrForbidden)
		}
		return role, nil
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return "", fmt.Errorf("service.authorizeProject: %w", err)
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return "", fmt.Errorf("service.authorizeProject: %w", err)
	}
	if role == "viewer" || role == "division_viewer" {
		return "", fmt.Errorf("service.authorizeProject: %w", domain.ErrForbidden)
	}
	return role, nil
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
	if _, err := s.authorizeProject(ctx, exec, projectID, actorID, actorRole); err != nil {
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

// checkPicGroupAllowed -- reuse EXACT sama guard Full vs Terbatas dipakai
// TaskService.SetStatus (§5.34): role Editor/Approver dibatasi ke PIC
// Group status ini KECUALI kosong (fallback Bebas). Dipakai AddPic DAN
// HandoffPic (IG-97 susulan) supaya kandidat "+ Tambah PIC Paralel"/
// "SERAHKAN PIC FASE" digerbangi aturan yang SAMA PERSIS, bukan aturan
// baru.
func (s *TaskPicService) checkPicGroupAllowed(ctx context.Context, exec db.Executor, projectID, statusID, role, candidateUserID string) error {
	if isFullPicMode(role) {
		return nil
	}
	group, err := s.repo.ListGroupForStatus(ctx, exec, projectID, statusID)
	if err != nil {
		return fmt.Errorf("service.checkPicGroupAllowed: %w", err)
	}
	if len(group) == 0 {
		return nil
	}
	for _, m := range group {
		if m.UserID == candidateUserID {
			return nil
		}
	}
	return fmt.Errorf("service.checkPicGroupAllowed: %w", domain.ErrPicNotInGroup)
}

// AddPic (IG-97 susulan, tab PIC FASE "+ Tambah PIC Paralel") -- tambah
// co-PIC pada fase status task SAAT INI, TANPA mengubah status atau
// menonaktifkan PIC lain. Gate "canEdit" (viewer ditolak) -- BUKAN
// PM/AW-only seperti RemovePic, karena selalu MENAMBAH tanggung jawab,
// tidak pernah meninggalkan fase tanpa PIC.
func (s *TaskPicService) AddPic(ctx context.Context, exec db.Executor, taskID, userID, actorID, actorRole string) error {
	if taskID == "" || userID == "" {
		return fmt.Errorf("service.AddPic: %w", domain.ErrInvalidInput)
	}
	task, err := s.tasks.Get(ctx, exec, taskID)
	if err != nil {
		return fmt.Errorf("service.AddPic: %w", err)
	}
	role, err := s.authorizeProject(ctx, exec, task.ProjectID, actorID, actorRole)
	if err != nil {
		return err
	}
	if err := s.checkPicGroupAllowed(ctx, exec, task.ProjectID, task.StatusID, role, userID); err != nil {
		return err
	}
	active, err := s.repo.ListActiveForTask(ctx, exec, taskID)
	if err != nil {
		return fmt.Errorf("service.AddPic: %w", err)
	}
	for i := range active {
		if active[i].UserID == userID {
			return fmt.Errorf("service.AddPic: %w", domain.ErrPicAlreadyActive)
		}
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, task.ProjectID)
	if err != nil {
		return fmt.Errorf("service.AddPic: %w", err)
	}
	if err := s.repo.AddPic(ctx, exec, taskID, task.StatusID, task.StatusName, userID, actorID, actorRole, workspaceID); err != nil {
		return fmt.Errorf("service.AddPic: %w", err)
	}
	return nil
}

// RemovePic (IG-97 susulan, tombol "✕ HAPUS PIC") -- lepas SATU PIC aktif
// TANPA pengganti. HANYA PM/AW (desain "canRemovePic": /PROJECT
// MANAGER|ADMIN WORKSPACE/) -- lebih ketat dari AddPic/HandoffPic karena
// bisa meninggalkan fase tanpa penanggung jawab kalau bukan PIC terakhir
// yang dihapus. Ditolak kalau ini PIC aktif TERAKHIR (pakai
// HandoffPic/"serah terima" sebagai gantinya, yang selalu mengisi
// pengganti dalam satu aksi).
func (s *TaskPicService) RemovePic(ctx context.Context, exec db.Executor, taskID, userID, actorID, actorRole string) error {
	if taskID == "" || userID == "" {
		return fmt.Errorf("service.RemovePic: %w", domain.ErrInvalidInput)
	}
	task, err := s.tasks.Get(ctx, exec, taskID)
	if err != nil {
		return fmt.Errorf("service.RemovePic: %w", err)
	}
	if err := s.authorizePicGroupManage(ctx, exec, task.ProjectID, actorID, actorRole); err != nil {
		return err
	}
	active, err := s.repo.ListActiveForTask(ctx, exec, taskID)
	if err != nil {
		return fmt.Errorf("service.RemovePic: %w", err)
	}
	if len(active) <= 1 {
		return fmt.Errorf("service.RemovePic: %w", domain.ErrLastActivePic)
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, task.ProjectID)
	if err != nil {
		return fmt.Errorf("service.RemovePic: %w", err)
	}
	ok, err := s.repo.RemovePic(ctx, exec, taskID, task.StatusName, userID, actorID, actorRole, workspaceID)
	if err != nil {
		return fmt.Errorf("service.RemovePic: %w", err)
	}
	if !ok {
		return fmt.Errorf("service.RemovePic: %w", domain.ErrNotActivePic)
	}
	return nil
}

// HandoffPic (IG-97 susulan, "SERAHKAN PIC FASE") -- serah terima fase ke
// user baru. fromUserID kosong berarti SEMUA PIC aktif digantikan (desain
// chip "SEMUA PIC"); fromUserID terisi berarti hanya PIC tersebut yang
// dilepas (PIC aktif lain, kalau ada, TIDAK berubah). Gate SAMA seperti
// AddPic (canEdit, bukan PM/AW-only) -- desain menaruh SERAHKAN dan
// TAMBAH PIC PARALEL dalam satu blok canEdit yang sama.
func (s *TaskPicService) HandoffPic(ctx context.Context, exec db.Executor, taskID, fromUserID, toUserID, actorID, actorRole string) error {
	if taskID == "" || toUserID == "" {
		return fmt.Errorf("service.HandoffPic: %w", domain.ErrInvalidInput)
	}
	task, err := s.tasks.Get(ctx, exec, taskID)
	if err != nil {
		return fmt.Errorf("service.HandoffPic: %w", err)
	}
	role, err := s.authorizeProject(ctx, exec, task.ProjectID, actorID, actorRole)
	if err != nil {
		return err
	}
	if err := s.checkPicGroupAllowed(ctx, exec, task.ProjectID, task.StatusID, role, toUserID); err != nil {
		return err
	}
	active, err := s.repo.ListActiveForTask(ctx, exec, taskID)
	if err != nil {
		return fmt.Errorf("service.HandoffPic: %w", err)
	}
	if len(active) == 0 {
		return fmt.Errorf("service.HandoffPic: %w", domain.ErrNotActivePic)
	}
	for i := range active {
		if active[i].UserID == toUserID {
			return fmt.Errorf("service.HandoffPic: %w", domain.ErrPicAlreadyActive)
		}
	}
	var fromUserIDs []string
	if fromUserID == "" {
		for i := range active {
			fromUserIDs = append(fromUserIDs, active[i].UserID)
		}
	} else {
		found := false
		for i := range active {
			if active[i].UserID == fromUserID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("service.HandoffPic: %w", domain.ErrNotActivePic)
		}
		fromUserIDs = []string{fromUserID}
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, task.ProjectID)
	if err != nil {
		return fmt.Errorf("service.HandoffPic: %w", err)
	}
	if err := s.repo.HandoffPic(ctx, exec, taskID, task.StatusID, task.StatusName, fromUserIDs, toUserID, actorID, actorRole, workspaceID); err != nil {
		return fmt.Errorf("service.HandoffPic: %w", err)
	}
	return nil
}
