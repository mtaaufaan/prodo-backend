package service

import (
	"context"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// timesheetEstimateRepository -- interface didefinisikan di consumer, §3.9.
type timesheetEstimateRepository interface {
	ListSources(ctx context.Context, exec db.Executor, projectID string) ([]repository.EstimateSourceRow, error)
	ReplaceEstimates(ctx context.Context, exec db.Executor, projectID string, entries []repository.EstimateInsert) error
	LogRecalculation(ctx context.Context, exec db.Executor, actorID, actorRole, projectID, workspaceID, projectName string, entries, tasks, minutes int) error
}

// EstimateResult -- ringkasan hitung ulang estimasi satu project.
type EstimateResult struct {
	Entries int
	Tasks   int
	Minutes int
}

type TimesheetEstimateService struct {
	repo timesheetEstimateRepository
	now  func() time.Time
}

func NewTimesheetEstimateService(repo timesheetEstimateRepository) *TimesheetEstimateService {
	return &TimesheetEstimateService{repo: repo, now: time.Now}
}

// Recalculate -- hitung ulang SELURUH estimasi jam task hasil import project
// ini (idempoten: entri estimasi lama dihapus lalu ditulis ulang) dan catat
// di audit trail. Otorisasi dilakukan pemanggil (import / endpoint PM-AW).
func (s *TimesheetEstimateService) Recalculate(ctx context.Context, exec db.Executor, projectID, workspaceID, projectName, actorID, actorRole string) (*EstimateResult, error) {
	sources, err := s.repo.ListSources(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.TimesheetRecalculate: %w", err)
	}
	phases := make([]EstimatePhase, 0, len(sources))
	for i := range sources {
		src := &sources[i]
		if src.Phase == "BACKLOG" && src.End == nil {
			continue // task belum dinyatakan lengkap: masih disiapkan, belum ada jam
		}
		p := EstimatePhase{TaskID: src.TaskID, TaskCode: src.TaskCode, Phase: src.Phase, Start: src.Start, PICs: src.PICs}
		if src.End != nil {
			p.End = *src.End
		}
		phases = append(phases, p)
	}

	entries := EstimateEntries(phases, s.now())
	inserts := make([]repository.EstimateInsert, len(entries))
	tasks := map[string]struct{}{}
	minutes := 0
	for i := range entries {
		e := &entries[i]
		inserts[i] = repository.EstimateInsert{TaskID: e.TaskID, UserID: e.UserID, Phase: e.Phase, StartedAt: e.StartedAt, EndedAt: e.EndedAt, Minutes: e.Minutes}
		tasks[e.TaskID] = struct{}{}
		minutes += e.Minutes
	}
	if err := s.repo.ReplaceEstimates(ctx, exec, projectID, inserts); err != nil {
		return nil, fmt.Errorf("service.TimesheetRecalculate: %w", err)
	}
	res := &EstimateResult{Entries: len(entries), Tasks: len(tasks), Minutes: minutes}
	if err := s.repo.LogRecalculation(ctx, exec, actorID, actorRole, projectID, workspaceID, projectName, res.Entries, res.Tasks, res.Minutes); err != nil {
		return nil, fmt.Errorf("service.TimesheetRecalculate: audit: %w", err)
	}
	return res, nil
}
