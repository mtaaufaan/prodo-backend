package service

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

// S6E-10 (IG-141): catat jam Editor -- validasi, hak akses, penempatan, batas harian, audit.

// Rabu 14 Okt 2026 15:30 WIB (= 08:30 UTC) -- tanggal WIB sama dengan UTC, tapi bukan awal/akhir hari.
var logNow = time.Date(2026, 10, 14, 8, 30, 0, 0, time.UTC)

func logSvc(role string, notOwner bool) (*TimeEntryService, *fakeTimeEntryRepo) {
	repo := &fakeTimeEntryRepo{}
	svc := NewTimeEntryService(repo, fakeTimeEntryTasks{notOwner: notOwner}, fakeTimeEntryProjects{}, &fakePerformanceRoleChecker{role: role}, &fakeSprintProjectRoles{found: false})
	return svc, repo
}

func TestLogHours_Validation(t *testing.T) {
	cases := []struct {
		name  string
		hours float64
		note  string
		ok    bool
	}{
		{"2,5 jam", 2.5, "", true},
		{"tepat 12 jam", 12, "", true},
		{"menit terkecil (1 menit)", 1.0 / 60, "", true},
		{"nol", 0, "", false},
		{"negatif", -1, "", false},
		{"12,5 jam", 12.5, "", false},
		{"13 jam", 13, "", false},
		{"NaN", math.NaN(), "", false},
		{"Inf", math.Inf(1), "", false},
		{"kurang dari 1 menit", 0.001, "", false},
		{"catatan 500 karakter", 1, strings.Repeat("x", 500), true},
		{"catatan 501 karakter", 1, strings.Repeat("x", 501), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := logSvc("editor", false)
			_, err := svc.LogHours(context.Background(), nil, "t1", "u1", "member", tc.hours, tc.note, logNow)
			if tc.ok && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if !tc.ok {
				if !errors.Is(err, domain.ErrInvalidInput) {
					t.Fatalf("err = %v, want ErrInvalidInput", err)
				}
				if repo.logged != nil {
					t.Fatalf("entri tersimpan padahal input tidak valid")
				}
			}
		})
	}
}

func TestLogHours_RoleMatrix(t *testing.T) {
	cases := []struct {
		role      string
		actorRole string
		notOwner  bool
		wantErr   error
	}{
		{"editor", "member", false, nil},
		{"editor", "member", true, domain.ErrNotYourTask},
		{"project_manager", "member", true, nil},
		{"admin_workspace", "member", true, nil},
		{"approver", "member", false, domain.ErrForbidden},
		{"viewer", "member", false, domain.ErrForbidden},
		{"division_viewer", "member", false, domain.ErrForbidden},
		{"", "member", false, domain.ErrForbidden}, // bukan anggota
		{"", "platform_admin", true, nil},
	}
	for _, tc := range cases {
		name := tc.role + "/" + tc.actorRole + map[bool]string{true: "/bukan-milik", false: "/milik"}[tc.notOwner]
		t.Run(name, func(t *testing.T) {
			svc, repo := logSvc(tc.role, tc.notOwner)
			_, err := svc.LogHours(context.Background(), nil, "t1", "u1", tc.actorRole, 2, "", logNow)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if repo.logged != nil || len(repo.audits) != 0 {
					t.Fatalf("ada efek samping padahal ditolak")
				}
			}
		})
	}
}

func TestLogHours_Placement(t *testing.T) {
	// 09:00 WIB hari itu = 02:00 UTC
	nine := time.Date(2026, 10, 14, 2, 0, 0, 0, time.UTC)
	t.Run("hari kosong: mulai 09:00 WIB, durasi sesuai jam", func(t *testing.T) {
		svc, repo := logSvc("editor", false)
		if _, err := svc.LogHours(context.Background(), nil, "t1", "u1", "member", 2.5, "uji", logNow); err != nil {
			t.Fatal(err)
		}
		e := repo.logged
		if !e.StartedAt.Equal(nine) || *e.DurationMinutes != 150 || !e.EndedAt.Equal(nine.Add(150*time.Minute)) {
			t.Fatalf("start=%v dur=%d end=%v", e.StartedAt, *e.DurationMinutes, e.EndedAt)
		}
		if e.IsApproved == nil || !*e.IsApproved {
			t.Fatalf("entri harus langsung disetujui")
		}
		if repo.loggedNote == nil || *repo.loggedNote != "uji" {
			t.Fatalf("catatan tidak tersimpan: %v", repo.loggedNote)
		}
	})
	t.Run("berurutan setelah entri terakhir hari itu", func(t *testing.T) {
		svc, repo := logSvc("editor", false)
		last := nine.Add(3 * time.Hour) // 12:00 WIB
		repo.dayMinutes, repo.dayLastEnd = 180, &last
		if _, err := svc.LogHours(context.Background(), nil, "t1", "u1", "member", 1, "", logNow); err != nil {
			t.Fatal(err)
		}
		if !repo.logged.StartedAt.Equal(last) {
			t.Fatalf("start=%v, want %v", repo.logged.StartedAt, last)
		}
	})
	t.Run("entri terakhir sebelum 09:00 tidak memundurkan mulai", func(t *testing.T) {
		svc, repo := logSvc("editor", false)
		early := nine.Add(-2 * time.Hour)
		repo.dayLastEnd = &early
		if _, err := svc.LogHours(context.Background(), nil, "t1", "u1", "member", 1, "", logNow); err != nil {
			t.Fatal(err)
		}
		if !repo.logged.StartedAt.Equal(nine) {
			t.Fatalf("start=%v, want %v", repo.logged.StartedAt, nine)
		}
	})
}

func TestLogHours_DailyLimit(t *testing.T) {
	svc, repo := logSvc("editor", false)
	repo.dayMinutes = 23 * 60
	if _, err := svc.LogHours(context.Background(), nil, "t1", "u1", "member", 2, "", logNow); !errors.Is(err, domain.ErrDailyTimeLimit) {
		t.Fatalf("err = %v, want ErrDailyTimeLimit", err)
	}
	if repo.logged != nil {
		t.Fatal("entri tersimpan padahal melewati batas harian")
	}
	if _, err := svc.LogHours(context.Background(), nil, "t1", "u1", "member", 1, "", logNow); err != nil {
		t.Fatalf("tepat 24 jam harus boleh: %v", err)
	}
}

func TestLogHours_AuditAndTotals(t *testing.T) {
	svc, repo := logSvc("editor", false)
	repo.userTotal = 270
	res, err := svc.LogHours(context.Background(), nil, "t1", "u1", "member", 2.5, "", logNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.TaskTotalMinutes != 270 || res.TaskCode != "P-1" {
		t.Fatalf("hasil = %+v", res)
	}
	if len(repo.audits) != 1 || repo.audits[0] != "task.time_entry_added" {
		t.Fatalf("audit = %v", repo.audits)
	}
	if repo.auditRoles[0] != "editor" {
		t.Fatalf("role audit = %q, want editor (role efektif, bukan role rute)", repo.auditRoles[0])
	}
	if repo.auditStates[0]["approval_status"] != "approved" {
		t.Fatalf("state audit = %v, want approval_status approved", repo.auditStates[0])
	}
	if len(repo.notified) != 0 {
		t.Fatalf("entri yang langsung disetujui tidak boleh memicu notifikasi persetujuan: %v", repo.notified)
	}
}

func TestStartTimerAndManualEntry_AreGated(t *testing.T) {
	for _, tc := range []struct {
		role     string
		notOwner bool
		wantErr  error
	}{
		{"viewer", false, domain.ErrForbidden},
		{"approver", false, domain.ErrForbidden},
		{"editor", true, domain.ErrNotYourTask},
		{"editor", false, nil},
	} {
		svc, _ := logSvc(tc.role, tc.notOwner)
		start := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
		_, errManual := svc.CreateManual(context.Background(), nil, "t1", "u1", "member", start, start.Add(time.Hour), nil)
		_, errTimer := svc.StartTimer(context.Background(), nil, "t1", "u1", "member")
		for name, err := range map[string]error{"CreateManual": errManual, "StartTimer": errTimer} {
			if tc.wantErr == nil && err != nil {
				t.Fatalf("%s %s err = %v, want nil", tc.role, name, err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("%s %s err = %v, want %v", tc.role, name, err, tc.wantErr)
			}
		}
	}
}

func TestWeekBounds(t *testing.T) {
	// Rabu 14 Okt 2026 15:30 WIB -> minggu Senin 12 Okt 00:00 WIB .. Senin 19 Okt 00:00 WIB
	s, e := weekBounds(logNow)
	if got := s.In(wib).Format("2006-01-02 15:04"); got != "2026-10-12 00:00" {
		t.Fatalf("start = %s", got)
	}
	if got := e.In(wib).Format("2006-01-02 15:04"); got != "2026-10-19 00:00" {
		t.Fatalf("end = %s", got)
	}
	// Minggu 18 Okt 2026 23:30 WIB masih minggu yang sama; Senin 00:10 WIB pindah minggu
	sun := time.Date(2026, 10, 18, 23, 30, 0, 0, wib)
	if s2, _ := weekBounds(sun); s2.In(wib).Format("2006-01-02") != "2026-10-12" {
		t.Fatalf("minggu = %v", s2)
	}
	mon := time.Date(2026, 10, 19, 0, 10, 0, 0, wib)
	if s3, _ := weekBounds(mon); s3.In(wib).Format("2006-01-02") != "2026-10-19" {
		t.Fatalf("minggu = %v", s3)
	}
	// 17:30 UTC Minggu = 00:30 WIB Senin -> sudah minggu berikutnya menurut WIB
	utc := time.Date(2026, 10, 18, 17, 30, 0, 0, time.UTC)
	if s4, _ := weekBounds(utc); s4.In(wib).Format("2006-01-02") != "2026-10-19" {
		t.Fatalf("minggu menurut WIB = %v", s4)
	}
}
