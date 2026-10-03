package handler

import (
	"testing"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

func TestTaskJSONDatesAreDateOnly(t *testing.T) {
	start := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	due := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	got := taskJSON(&repository.Task{StartDate: &start, DueDate: &due})

	if s := got["start_date"].(*string); s == nil || *s != "2026-10-03" {
		t.Errorf("start_date = %v, want 2026-10-03", got["start_date"])
	}
	if d := got["due_date"].(*string); d == nil || *d != "2026-10-06" {
		t.Errorf("due_date = %v, want 2026-10-06", got["due_date"])
	}

	empty := taskJSON(&repository.Task{})
	if s := empty["start_date"].(*string); s != nil {
		t.Errorf("start_date kosong harus nil, got %v", *s)
	}
}
