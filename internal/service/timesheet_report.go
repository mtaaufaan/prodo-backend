// Package service -- TimesheetReportService (menu "Timesheet & Analitik" PM,
// "PM Timesheet.dc.html", IG-128). Agregat jam tercatat per project di atas
// time_entries (IG-97). Akses: PM project, Admin Workspace, GA/PA (sama
// gerbang Performance Dashboard project). Tab Persetujuan memakai endpoint
// approve/reject per-entri yang sudah ada (TimeEntryService).
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// timesheetReportRepository -- interface didefinisikan di consumer, §3.9.
type timesheetReportRepository interface {
	ProjectInfo(ctx context.Context, exec db.Executor, projectID string) (workspaceID, name string, err error)
	ListStatuses(ctx context.Context, exec db.Executor, projectID string) ([]repository.TimesheetStatus, error)
	ListTasks(ctx context.Context, exec db.Executor, projectID string) ([]repository.TimesheetTask, error)
	ListMembers(ctx context.Context, exec db.Executor, projectID string) ([]repository.TimesheetMember, error)
	PendingCount(ctx context.Context, exec db.Executor, projectID string) (int, error)
	ListManualEntries(ctx context.Context, exec db.Executor, projectID, approvalStatus string) ([]repository.TimesheetPendingEntry, error)
	ListMine(ctx context.Context, exec db.Executor, userID string, from, to *time.Time) ([]repository.MyTimesheetEntry, error)
	LogExport(ctx context.Context, exec db.Executor, actorID, actorRole, projectID, workspaceID, projectName string, rows, loggedMinutes int) error
}

type TimesheetReportService struct {
	repo timesheetReportRepository
	pm   performancePMChecker
	rbac sprintWorkspaceRoleChecker
}

func NewTimesheetReportService(repo timesheetReportRepository, pm performancePMChecker, rbac sprintWorkspaceRoleChecker) *TimesheetReportService {
	return &TimesheetReportService{repo: repo, pm: pm, rbac: rbac}
}

// TimesheetStatusHours -- satu batang "JAM TERCATAT PER STATUS".
type TimesheetStatusHours struct {
	Name        string
	ColorToken  string
	IsUndefined bool
	Hours       float64
	TaskCount   int
}

// TimesheetTaskRow -- satu baris "AKURASI ESTIMASI PER TASK" (hanya task
// berestimasi) -- rasio/label dihitung FE dari dua angka ini.
type TimesheetTaskRow struct {
	Code           string
	Title          string
	StatusName     string
	EstimatedHours float64
	LoggedHours    float64
}

// TimesheetMemberRow -- satu baris Per-Member.
type TimesheetMemberRow struct {
	UserID      string
	Name        string
	Role        string
	TaskCount   int
	Hours       float64
	ActiveCount int
}

type TimesheetSummary struct {
	ProjectName     string
	LoggedHours     float64
	EstimateHours   float64
	OverCount       int
	OverCodes       []string // maks 2 kode pertama (catatan kartu)
	MembersInvolved int
	PendingCount    int
	ByStatus        []TimesheetStatusHours
	Tasks           []TimesheetTaskRow
	Members         []TimesheetMemberRow
}

func minutesToHours(m int) float64 { return float64(m) / 60 }

// authorize -- PM project ini, Admin Workspace, atau GA/PA. Mengembalikan
// workspaceID + nama project (dipakai audit export + nama berkas).
func (s *TimesheetReportService) authorize(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) (workspaceID, name string, err error) {
	if projectID == "" {
		return "", "", fmt.Errorf("service.TimesheetReport: %w", domain.ErrInvalidInput)
	}
	workspaceID, name, err = s.repo.ProjectInfo(ctx, exec, projectID)
	if err != nil {
		return "", "", err
	}
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return workspaceID, name, nil
	}
	isPM, err := s.pm.IsPM(ctx, exec, projectID, actorID)
	if err != nil {
		return "", "", fmt.Errorf("service.TimesheetReport: %w", err)
	}
	if isPM {
		return workspaceID, name, nil
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return "", "", fmt.Errorf("service.TimesheetReport: %w", err)
	}
	if role != "admin_workspace" {
		return "", "", fmt.Errorf("service.TimesheetReport: %w", domain.ErrForbidden)
	}
	return workspaceID, name, nil
}

// Summary -- GET /projects/:id/timesheet.
func (s *TimesheetReportService) Summary(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) (*TimesheetSummary, error) {
	_, name, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return nil, err
	}
	statuses, err := s.repo.ListStatuses(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.TimesheetSummary: %w", err)
	}
	tasks, err := s.repo.ListTasks(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.TimesheetSummary: %w", err)
	}
	members, err := s.repo.ListMembers(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.TimesheetSummary: %w", err)
	}
	pending, err := s.repo.PendingCount(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.TimesheetSummary: %w", err)
	}
	return buildTimesheetSummary(name, statuses, tasks, members, pending), nil
}

// buildTimesheetSummary -- fungsi murni (diuji unit): kartu stat, batang per
// status, akurasi estimasi, dan baris Per-Member dari data mentah repository.
func buildTimesheetSummary(projectName string, statuses []repository.TimesheetStatus, tasks []repository.TimesheetTask, members []repository.TimesheetMember, pending int) *TimesheetSummary {
	out := &TimesheetSummary{
		ProjectName: projectName, MembersInvolved: len(members), PendingCount: pending,
		ByStatus:  make([]TimesheetStatusHours, 0, len(statuses)),
		Tasks:     make([]TimesheetTaskRow, 0),
		Members:   make([]TimesheetMemberRow, 0, len(members)),
		OverCodes: []string{},
	}
	statusIdx := make(map[string]int, len(statuses))
	for i, st := range statuses {
		out.ByStatus = append(out.ByStatus, TimesheetStatusHours{Name: st.Name, ColorToken: st.ColorToken, IsUndefined: st.IsUndefined})
		statusIdx[st.Name] = i
	}
	totalMinutes := 0
	for i := range tasks {
		t := &tasks[i]
		totalMinutes += t.LoggedMinutes
		out.EstimateHours += t.EstimatedHours
		if i, ok := statusIdx[t.StatusName]; ok {
			out.ByStatus[i].Hours += minutesToHours(t.LoggedMinutes)
			out.ByStatus[i].TaskCount++
		}
		if t.EstimatedHours > 0 {
			logged := minutesToHours(t.LoggedMinutes)
			out.Tasks = append(out.Tasks, TimesheetTaskRow{Code: t.Code, Title: t.Title, StatusName: t.StatusName, EstimatedHours: t.EstimatedHours, LoggedHours: logged})
			if logged > t.EstimatedHours {
				out.OverCount++
				if len(out.OverCodes) < 2 {
					out.OverCodes = append(out.OverCodes, t.Code)
				}
			}
		}
	}
	out.LoggedHours = minutesToHours(totalMinutes)
	for i := range members {
		m := &members[i]
		out.Members = append(out.Members, TimesheetMemberRow{
			UserID: m.UserID, Name: m.Name, Role: m.Role, TaskCount: m.TaskCount,
			Hours: minutesToHours(m.LoggedMinutes), ActiveCount: m.ActiveCount,
		})
	}
	return out
}

// Entries -- GET /projects/:id/timesheet/entries: antrian persetujuan
// (entri manual) dengan filter approval_status.
func (s *TimesheetReportService) Entries(ctx context.Context, exec db.Executor, projectID, approvalStatus, actorID, actorRole string) ([]repository.TimesheetPendingEntry, error) {
	if _, _, err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return nil, err
	}
	switch approvalStatus {
	case "", "pending", "approved", "rejected":
	default:
		return nil, fmt.Errorf("service.TimesheetEntries: %w", domain.ErrInvalidInput)
	}
	return s.repo.ListManualEntries(ctx, exec, projectID, approvalStatus)
}

// ExportRow -- satu baris CSV (kode, judul, status, PIC aktif, estimasi, jam
// tercatat, selisih).
type ExportRow struct {
	Code           string
	Title          string
	StatusName     string
	ActivePICs     []string
	EstimatedHours float64
	LoggedHours    float64
}

// Export -- GET /projects/:id/timesheet/export: seluruh task + jam, dan
// mencatat aksi ekspor di audit trail (desain: aksi ACCESS "Ekspor timesheet").
func (s *TimesheetReportService) Export(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) (projectName string, rows []ExportRow, err error) {
	workspaceID, name, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return "", nil, err
	}
	tasks, err := s.repo.ListTasks(ctx, exec, projectID)
	if err != nil {
		return "", nil, fmt.Errorf("service.TimesheetExport: %w", err)
	}
	rows = make([]ExportRow, 0, len(tasks))
	totalMinutes := 0
	for i := range tasks {
		t := &tasks[i]
		totalMinutes += t.LoggedMinutes
		rows = append(rows, ExportRow{Code: t.Code, Title: t.Title, StatusName: t.StatusName, ActivePICs: t.ActivePICs, EstimatedHours: t.EstimatedHours, LoggedHours: minutesToHours(t.LoggedMinutes)})
	}
	if err := s.repo.LogExport(ctx, exec, actorID, actorRole, projectID, workspaceID, name, len(rows), totalMinutes); err != nil {
		return "", nil, fmt.Errorf("service.TimesheetExport: audit: %w", err)
	}
	return name, rows, nil
}

// Mine -- GET /me/timesheets: timesheet pribadi, semua project. from/to
// berformat YYYY-MM-DD (to inklusif); kosong = tanpa batas.
func (s *TimesheetReportService) Mine(ctx context.Context, exec db.Executor, actorID, from, to string) ([]repository.MyTimesheetEntry, error) {
	var fromT, toT *time.Time
	if from != "" {
		t, err := time.Parse("2006-01-02", from)
		if err != nil {
			return nil, fmt.Errorf("service.TimesheetMine: %w", domain.ErrInvalidInput)
		}
		fromT = &t
	}
	if to != "" {
		t, err := time.Parse("2006-01-02", to)
		if err != nil {
			return nil, fmt.Errorf("service.TimesheetMine: %w", domain.ErrInvalidInput)
		}
		t = t.AddDate(0, 0, 1) // inklusif: sampai akhir hari `to`
		toT = &t
	}
	if fromT != nil && toT != nil && !toT.After(*fromT) {
		return nil, fmt.Errorf("service.TimesheetMine: %w", domain.ErrInvalidInput)
	}
	return s.repo.ListMine(ctx, exec, actorID, fromT, toT)
}
