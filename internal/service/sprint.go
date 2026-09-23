// Package service -- SprintService (Task Management Core Phase 1, US-013;
// status 3-state + kapasitas SP + audit trail, Track S5 IG-92).
package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// sprintRepository -- interface didefinisikan di consumer, §3.9.
type sprintRepository interface {
	Create(ctx context.Context, exec db.Executor, projectID, name string, startDate, endDate *time.Time, goal *string, workspaceID, actorID, actorRole string) (*repository.Sprint, error)
	List(ctx context.Context, exec db.Executor, projectID string) ([]repository.Sprint, error)
	NameTaken(ctx context.Context, exec db.Executor, projectID, name string, excludeID *string) (bool, error)
	CountInProject(ctx context.Context, exec db.Executor, projectID string) (int, error)
	Get(ctx context.Context, exec db.Executor, sprintID string) (*repository.Sprint, error)
	GetActiveInProject(ctx context.Context, exec db.Executor, projectID string) (*repository.Sprint, error)
	Update(ctx context.Context, exec db.Executor, sprintID, name string, startDate, endDate *time.Time, goal *string, workspaceID, actorID, actorRole string, before map[string]any) error
	SetStatus(ctx context.Context, exec db.Executor, sprintID, status, action, workspaceID, actorID, actorRole, fromStatus string) error
	Delete(ctx context.Context, exec db.Executor, sprintID, workspaceID, actorID, actorRole, name string) error
	UnassignIncompleteTasks(ctx context.Context, exec db.Executor, sprintID, doneStatusID string) error
	Summary(ctx context.Context, exec db.Executor, sprintID string) (totalSP, doneSP, unestimatedCount, taskCount int, err error)
	AssignTasks(ctx context.Context, exec db.Executor, sprintID, projectID, workspaceID, actorID, actorRole string, taskIDs []string) error
}

// sprintProjectResolver -- reuse ProjectRepository.GetWorkspaceID.
type sprintProjectResolver interface {
	GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error)
}

// sprintCustomStatuses -- reuse CustomStatusRepository, dipakai
// closeSprint untuk resolve status DONE (S4-09: task belum selesai
// dipindah ke backlog).
type sprintCustomStatuses interface {
	ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.CustomStatus, error)
}

// sprintWorkspaceRoleChecker -- reuse RBACService.GetMemberRole.
type sprintWorkspaceRoleChecker interface {
	GetMemberRole(ctx context.Context, exec db.Executor, workspaceID, userID string) (string, error)
}

// sprintProjectRoleChecker -- reuse ProjectMemberRepository.GetRole.
type sprintProjectRoleChecker interface {
	GetRole(ctx context.Context, exec db.Executor, projectID, userID string) (string, bool, error)
}

type SprintService struct {
	repo         sprintRepository
	projects     sprintProjectResolver
	statuses     sprintCustomStatuses
	rbac         sprintWorkspaceRoleChecker
	projectRoles sprintProjectRoleChecker
}

func NewSprintService(repo sprintRepository, projects sprintProjectResolver, statuses sprintCustomStatuses, rbac sprintWorkspaceRoleChecker, projectRoles sprintProjectRoleChecker) *SprintService {
	return &SprintService{repo: repo, projects: projects, statuses: statuses, rbac: rbac, projectRoles: projectRoles}
}

// authorize -- gate tulis: viewer/division_viewer DITOLAK, selebihnya
// (admin_workspace/project_manager/editor/approver, atau project-scoped
// editor/approver) boleh. RLS tetap jadi lapisan pertama (membership),
// ini menambah gate ROLE yang RLS sendiri sengaja tidak tegakkan
// (RLS_DESIGN.md §7.6: "role check di application layer").
//
// Mengembalikan role EFEKTIF yang dipakai untuk audit trail (IG-92,
// ditemukan lewat verifikasi live -- BUKAN review kode: rute sprint/task
// sengaja TIDAK dipasangi middleware `RequireRole`/`RequirePlatformRole`
// (route berbasis :projectId, bukan :wsId -- lihat komentar registrasi
// rute di main.go), jadi `actorRoleLocalsKey` yang jadi sumber parameter
// `actorRole` di seluruh handler TIDAK PERNAH terisi untuk endpoint ini --
// selalu string kosong. `SprintService.authorize` sendiri SUDAH resolve
// role asli lewat `projectRoles`/`rbac` untuk keperluan otorisasi, tapi
// nilai itu sebelumnya dibuang -- audit trail sprint (baru dibangun sesi
// ini) jadi satu-satunya jalur yang KETAHUAN menulis `actor_role` kosong,
// karena tidak ada audit sprint sebelumnya. Dikembalikan di sini supaya
// seluruh pemanggil pakai role EFEKTIF ini utk audit, bukan parameter
// `actorRole` mentah dari handler.
func (s *SprintService) authorize(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) (string, error) {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return actorRole, nil
	}
	if role, found, err := s.projectRoles.GetRole(ctx, exec, projectID, actorID); err == nil && found {
		if role == "viewer" {
			return "", fmt.Errorf("service.authorize: %w", domain.ErrForbidden)
		}
		return role, nil
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return "", fmt.Errorf("service.authorize: %w", err)
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return "", fmt.Errorf("service.authorize: %w", err)
	}
	if role == "viewer" || role == "division_viewer" {
		return "", fmt.Errorf("service.authorize: %w", domain.ErrForbidden)
	}
	return role, nil
}

func (s *SprintService) Create(ctx context.Context, exec db.Executor, projectID, name string, startDate, endDate *time.Time, goal *string, actorID, actorRole string) (*repository.Sprint, error) {
	if projectID == "" {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrInvalidInput)
	}
	auditRole, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		// Auto-generate "Sprint N" kalau nama dikosongkan (PM Add
		// Sprint.dc.html nextName()) -- ditegakkan di backend juga
		// (bukan cuma FE) supaya API tetap benar dipanggil langsung.
		count, err := s.repo.CountInProject(ctx, exec, projectID)
		if err != nil {
			return nil, fmt.Errorf("service.Create: %w", err)
		}
		name = repository.NextAutoSprintName(count)
	}
	taken, err := s.repo.NameTaken(ctx, exec, projectID, name, nil)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	if taken {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrSprintNameTaken)
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	sprint, err := s.repo.Create(ctx, exec, projectID, name, startDate, endDate, goal, workspaceID, actorID, auditRole)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	return sprint, nil
}

func (s *SprintService) List(ctx context.Context, exec db.Executor, projectID string) ([]repository.Sprint, error) {
	if projectID == "" {
		return nil, fmt.Errorf("service.List: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.List(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.List: %w", err)
	}
	return list, nil
}

func (s *SprintService) Update(ctx context.Context, exec db.Executor, sprintID, name string, startDate, endDate *time.Time, goal *string, actorID, actorRole string) error {
	name = strings.TrimSpace(name)
	if sprintID == "" || name == "" {
		return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
	}
	sprint, err := s.repo.Get(ctx, exec, sprintID)
	if err != nil {
		return err
	}
	auditRole, err := s.authorize(ctx, exec, sprint.ProjectID, actorID, actorRole)
	if err != nil {
		return err
	}
	taken, err := s.repo.NameTaken(ctx, exec, sprint.ProjectID, name, &sprintID)
	if err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	if taken {
		return fmt.Errorf("service.Update: %w", domain.ErrSprintNameTaken)
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, sprint.ProjectID)
	if err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	before := map[string]any{"name": sprint.Name, "start_date": sprint.StartDate, "end_date": sprint.EndDate}
	if err := s.repo.Update(ctx, exec, sprintID, name, startDate, endDate, goal, workspaceID, actorID, auditRole, before); err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	return nil
}

// closeSprint -- helper bersama StartSprint (menutup sprint aktif lama
// sebelum mengaktifkan yang baru) dan CompleteSprint (menutup manual via
// tombol "TUTUP SPRINT"). Perilaku PERSIS sama di kedua jalur (pola desain
// "PM Sprint.dc.html" setSprintStatus(): task belum DONE dipindah ke
// backlog, status -> 'done') -- satu chokepoint, bukan logic terduplikasi.
func (s *SprintService) closeSprint(ctx context.Context, exec db.Executor, sprint *repository.Sprint, workspaceID, actorID, actorRole string) error {
	statuses, err := s.statuses.ListForWorkspace(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("service.closeSprint: %w", err)
	}
	var doneStatusID string
	for _, st := range statuses {
		if st.Name == "DONE" {
			doneStatusID = st.ID
			break
		}
	}
	if err := s.repo.SetStatus(ctx, exec, sprint.ID, "done", "sprint.completed", workspaceID, actorID, actorRole, sprint.Status); err != nil {
		return fmt.Errorf("service.closeSprint: %w", err)
	}
	if doneStatusID != "" {
		if err := s.repo.UnassignIncompleteTasks(ctx, exec, sprint.ID, doneStatusID); err != nil {
			return fmt.Errorf("service.closeSprint: %w", err)
		}
	}
	return nil
}

// StartSprint -- "hanya satu sprint aktif per project" (S4-08, IG-92
// diperluas): kalau ada sprint lain yang masih 'active' di project ini,
// TUTUP dulu (bukan cuma dinonaktifkan diam-diam -- pola desain "menutup
// sprint yang sedang berjalan", task belum DONE-nya ikut dipindah ke
// backlog persis seperti tombol TUTUP SPRINT manual), baru aktifkan
// target. Behavior BERUBAH dari S4-08 asli (dulu 409 kalau ada yang
// aktif) -- ini SENGAJA mengikuti "PM Sprint.dc.html" setSprintStatus()
// yang jadi sumber otoritatif desain, bukan regresi.
func (s *SprintService) StartSprint(ctx context.Context, exec db.Executor, sprintID, actorID, actorRole string) error {
	sprint, err := s.repo.Get(ctx, exec, sprintID)
	if err != nil {
		return err
	}
	auditRole, err := s.authorize(ctx, exec, sprint.ProjectID, actorID, actorRole)
	if err != nil {
		return err
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, sprint.ProjectID)
	if err != nil {
		return fmt.Errorf("service.StartSprint: %w", err)
	}
	running, err := s.repo.GetActiveInProject(ctx, exec, sprint.ProjectID)
	if err != nil {
		return fmt.Errorf("service.StartSprint: %w", err)
	}
	if running != nil && running.ID != sprintID {
		if err := s.closeSprint(ctx, exec, running, workspaceID, actorID, auditRole); err != nil {
			return fmt.Errorf("service.StartSprint: %w", err)
		}
	}
	if err := s.repo.SetStatus(ctx, exec, sprintID, "active", "sprint.started", workspaceID, actorID, auditRole, sprint.Status); err != nil {
		return fmt.Errorf("service.StartSprint: %w", err)
	}
	return nil
}

// CompleteSprint -- S4-09/IG-92: sprint ditutup manual (tombol "TUTUP
// SPRINT"), task yang belum berstatus DONE dipindah ke backlog (sprint_id
// NULL) -- lihat komentar SprintRepository.UnassignIncompleteTasks kenapa
// bukan otomatis ke sprint berikutnya. Reuse closeSprint (chokepoint sama
// dengan auto-close di StartSprint).
func (s *SprintService) CompleteSprint(ctx context.Context, exec db.Executor, sprintID, actorID, actorRole string) error {
	sprint, err := s.repo.Get(ctx, exec, sprintID)
	if err != nil {
		return err
	}
	auditRole, err := s.authorize(ctx, exec, sprint.ProjectID, actorID, actorRole)
	if err != nil {
		return err
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, sprint.ProjectID)
	if err != nil {
		return fmt.Errorf("service.CompleteSprint: %w", err)
	}
	if err := s.closeSprint(ctx, exec, sprint, workspaceID, actorID, auditRole); err != nil {
		return fmt.Errorf("service.CompleteSprint: %w", err)
	}
	return nil
}

// ReopenSprint -- "↺ BUKA KEMBALI" (IG-92, baru -- tidak ada di S4
// original): sprint 'done' dikembalikan ke 'backlog'. Tidak
// mengembalikan task yang sudah dipindah ke backlog saat ditutup (sudah
// hilang keterkaitannya, sama seperti desain -- PM assign manual lagi
// kalau perlu) dan tidak menghapus histori SP (Summary tetap dihitung
// live dari task yang masih ter-assign ke sprint ini).
func (s *SprintService) ReopenSprint(ctx context.Context, exec db.Executor, sprintID, actorID, actorRole string) error {
	sprint, err := s.repo.Get(ctx, exec, sprintID)
	if err != nil {
		return err
	}
	auditRole, err := s.authorize(ctx, exec, sprint.ProjectID, actorID, actorRole)
	if err != nil {
		return err
	}
	if sprint.Status != "done" {
		return fmt.Errorf("service.ReopenSprint: %w", domain.ErrSprintNotDone)
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, sprint.ProjectID)
	if err != nil {
		return fmt.Errorf("service.ReopenSprint: %w", err)
	}
	if err := s.repo.SetStatus(ctx, exec, sprintID, "backlog", "sprint.reopened", workspaceID, actorID, auditRole, sprint.Status); err != nil {
		return fmt.Errorf("service.ReopenSprint: %w", err)
	}
	return nil
}

// Summary -- GET /sprints/:id/summary (Phase 4, US-018a/S4-59; diperluas
// IG-92 kapasitas SP US-081: total/selesai/tersisa, tanpa tabel velocity
// terpisah -- "selesai" dihitung live dari task yang masih ter-assign ke
// sprint, yang otomatis JADI angka velocity begitu sprint ditutup karena
// task belum-done sudah dipindah keluar (UnassignIncompleteTasks)).
func (s *SprintService) Summary(ctx context.Context, exec db.Executor, sprintID string) (totalSP, doneSP, unestimatedCount, taskCount int, err error) {
	if sprintID == "" {
		return 0, 0, 0, 0, fmt.Errorf("service.Summary: %w", domain.ErrInvalidInput)
	}
	totalSP, doneSP, unestimatedCount, taskCount, err = s.repo.Summary(ctx, exec, sprintID)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("service.Summary: %w", err)
	}
	return totalSP, doneSP, unestimatedCount, taskCount, nil
}

// AssignTasks -- "Tarik Task dari Backlog" (checklist di modal buat
// sprint baru, IG-92). Dipanggil terpisah dari Create supaya endpoint
// Create tetap simpel (satu tanggung jawab) -- FE memanggil dua request
// berurutan persis seperti mock (`addSprint` lalu `bulkUpdateTasks`).
func (s *SprintService) AssignTasks(ctx context.Context, exec db.Executor, sprintID string, taskIDs []string, actorID, actorRole string) error {
	if sprintID == "" || len(taskIDs) == 0 {
		return nil
	}
	sprint, err := s.repo.Get(ctx, exec, sprintID)
	if err != nil {
		return err
	}
	auditRole, err := s.authorize(ctx, exec, sprint.ProjectID, actorID, actorRole)
	if err != nil {
		return err
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, sprint.ProjectID)
	if err != nil {
		return fmt.Errorf("service.AssignTasks: %w", err)
	}
	if err := s.repo.AssignTasks(ctx, exec, sprintID, sprint.ProjectID, workspaceID, actorID, auditRole, taskIDs); err != nil {
		return fmt.Errorf("service.AssignTasks: %w", err)
	}
	return nil
}

func (s *SprintService) Delete(ctx context.Context, exec db.Executor, sprintID, actorID, actorRole string) error {
	sprint, err := s.repo.Get(ctx, exec, sprintID)
	if err != nil {
		return err
	}
	auditRole, err := s.authorize(ctx, exec, sprint.ProjectID, actorID, actorRole)
	if err != nil {
		return err
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, sprint.ProjectID)
	if err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	if err := s.repo.Delete(ctx, exec, sprintID, workspaceID, actorID, auditRole, sprint.Name); err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	return nil
}
