package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// Skenario ilustrasi Member Performance berbasis PIC (IG-126): Daniel PIC backlog,
// Taufan dan Joyse memegang fase kerja bergantian.
var memBase = time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)

func memAt(days float64) time.Time { return memBase.Add(time.Duration(days*24) * time.Hour) }

func memTask(id, priority, status string, readyDays *float64, reopens int) repository.PerfTask {
	complete := "complete"
	if status == "BACKLOG" && readyDays == nil {
		complete = "incomplete"
	}
	t := repository.PerfTask{TaskID: id, Priority: priority, StatusName: status, CreatedAt: memBase, Completeness: &complete, ReopenCount: reopens}
	if readyDays != nil {
		r := memAt(*readyDays)
		t.ReadyAt = &r
	}
	return t
}

func memPhase(task, userID, user, priority, phaseStatus, taskStatus string, start float64, end *float64) repository.PerfMemberPhase {
	ph := repository.PerfMemberPhase{
		TaskID: task, UserID: userID, UserName: user, Priority: priority, PhaseStatus: phaseStatus, TaskStatus: taskStatus,
		IsActive: end == nil, ActivatedAt: memAt(start),
	}
	if end != nil {
		e := memAt(*end)
		ph.DeactivatedAt = &e
	}
	return ph
}

func fp(v float64) *float64 { return &v }

func memberByName(ms []MemberStat, name string) *MemberStat {
	for i := range ms {
		if ms[i].UserName == name {
			return &ms[i]
		}
	}
	return nil
}

func near(got, want float64) bool { return got-want < 0.01 && got-want > -0.01 }

func TestMemberPerformance_PICBased_Scenario(t *testing.T) {
	now := memAt(6)
	tasks := []repository.PerfTask{
		memTask("t1", "high", "DONE", fp(1), 0),
		memTask("t2", "medium", "DONE", fp(3), 1), // sempat dikembalikan ke belum lengkap 1x
		memTask("t3", "high", "IN PROGRESS", fp(2), 0),
		memTask("t4", "low", "DONE", fp(1), 0),
		memTask("t5", "medium", "BACKLOG", nil, 0), // belum lengkap: sedang disiapkan
		memTask("t6", "critical", "IN PROGRESS", nil, 0),
		memTask("t7", "medium", "UNDER REVIEW", nil, 0),
	}
	phases := []repository.PerfMemberPhase{
		// PIC backlog Daniel
		memPhase("t1", "d", "Daniel", "high", "BACKLOG", "DONE", 0, fp(1)),
		memPhase("t2", "d", "Daniel", "medium", "BACKLOG", "DONE", 0, fp(3)),
		memPhase("t3", "d", "Daniel", "high", "BACKLOG", "IN PROGRESS", 0, fp(2)),
		memPhase("t4", "d", "Daniel", "low", "BACKLOG", "DONE", 0, fp(1)),
		memPhase("t5", "d", "Daniel", "medium", "BACKLOG", "BACKLOG", 0, nil),
		// fase kerja
		memPhase("t1", "t", "Taufan", "high", "IN PROGRESS", "DONE", 1, fp(4)),
		memPhase("t1", "j", "Joyse", "high", "UNDER REVIEW", "DONE", 4, fp(5)),
		memPhase("t2", "t", "Taufan", "medium", "IN PROGRESS", "DONE", 3, fp(5)),
		memPhase("t2", "j", "Joyse", "medium", "UNDER REVIEW", "DONE", 5, fp(7)),
		memPhase("t3", "t", "Taufan", "high", "IN PROGRESS", "IN PROGRESS", 2, nil),
		memPhase("t4", "j", "Joyse", "low", "IN PROGRESS", "DONE", 1, fp(6)),
		memPhase("t4", "t", "Taufan", "low", "UNDER REVIEW", "DONE", 6, fp(7)),
		memPhase("t6", "t", "Taufan", "critical", "IN PROGRESS", "IN PROGRESS", 3, nil),
		memPhase("t7", "t", "Taufan", "medium", "IN PROGRESS", "UNDER REVIEW", 1, fp(3)),
		memPhase("t7", "j", "Joyse", "medium", "UNDER REVIEW", "UNDER REVIEW", 3, nil),
		// fase DONE lama tidak menyumbang apa pun
		memPhase("t1", "j", "Joyse", "high", "DONE", "DONE", 5, fp(5)),
	}
	out := memberPerformance(tasks, phases, nil, now)
	if len(out) != 3 || out[0].UserName != "Taufan" || out[1].UserName != "Daniel" || out[2].UserName != "Joyse" {
		t.Fatalf("urutan = %+v, want Taufan (load 2), Daniel (1), Joyse (1)", out)
	}

	tf := memberByName(out, "Taufan")
	if tf.ActiveByPriority["critical"] != 1 || tf.ActiveByPriority["high"] != 1 || activeTotal(tf) != 2 {
		t.Errorf("Taufan task load = %+v, want critical 1 + high 1", tf.ActiveByPriority)
	}
	if tf.TotalCount != 6 || tf.DoneCount != 3 || !near(tf.CompletionRateRaw, 50) || !near(tf.CompletionRateWeighted, 37.5) {
		t.Errorf("Taufan completion = %d/%d raw %.1f weighted %.1f, want 3/6, 50, 37.5", tf.DoneCount, tf.TotalCount, tf.CompletionRateRaw, tf.CompletionRateWeighted)
	}
	if tf.AvgCompletionHours == nil || !near(*tf.AvgCompletionHours, 48) {
		t.Errorf("Taufan avg selesai = %v, want 48 jam (72+48+24)/3", tf.AvgCompletionHours)
	}
	if tf.ReadinessCount != 0 || tf.ReadinessAvgHours != nil {
		t.Errorf("Taufan bukan PIC backlog, readiness = %+v", tf)
	}

	jy := memberByName(out, "Joyse")
	if jy.TotalCount != 4 || jy.DoneCount != 3 || !near(jy.CompletionRateRaw, 75) || !near(jy.CompletionRateWeighted, 75) {
		t.Errorf("Joyse completion = %d/%d raw %.1f weighted %.1f, want 3/4, 75, 75", jy.DoneCount, jy.TotalCount, jy.CompletionRateRaw, jy.CompletionRateWeighted)
	}
	if jy.AvgCompletionHours == nil || !near(*jy.AvgCompletionHours, 64) {
		t.Errorf("Joyse avg selesai = %v, want 64 jam (24+48+120)/3", jy.AvgCompletionHours)
	}
	if jy.ActiveByPriority["medium"] != 1 {
		t.Errorf("Joyse task load = %+v, want medium 1 (t7)", jy.ActiveByPriority)
	}

	dn := memberByName(out, "Daniel")
	if dn.TotalCount != 0 || dn.CompletionRateRaw != 0 || dn.AvgCompletionHours != nil {
		t.Errorf("Daniel tanpa fase kerja: %+v", dn)
	}
	if dn.ActiveByPriority["medium"] != 1 {
		t.Errorf("Daniel task load = %+v, want medium 1 (t5 backlog, PIC aktif)", dn.ActiveByPriority)
	}
	// siap: t1 24j, t2 72j, t3 48j, t4 24j -> rata-rata 42j; 1 sedang disiapkan (t5); 1 reopen (t2)
	if dn.ReadinessCount != 4 || dn.ReadinessAvgHours == nil || !near(*dn.ReadinessAvgHours, 42) || dn.ReadinessPreparing != 1 || dn.ReadinessReopens != 1 {
		t.Errorf("Daniel readiness = count %d avg %v preparing %d reopens %d, want 4, 42, 1, 1", dn.ReadinessCount, dn.ReadinessAvgHours, dn.ReadinessPreparing, dn.ReadinessReopens)
	}
}

// Kesiapan dinilai pada PIC backlog yang AKTIF saat dinyatakan lengkap; task tanpa
// ready_at (import / data lama) tidak ikut.
func TestMemberPerformance_Readiness_CreditsActivePICAtReadyTime(t *testing.T) {
	tasks := []repository.PerfTask{
		memTask("a", "high", "IN PROGRESS", fp(2), 0),
		memTask("imp", "high", "DONE", nil, 0), // hasil import: ReadyAt nil
	}
	phases := []repository.PerfMemberPhase{
		memPhase("a", "x", "Awal", "high", "BACKLOG", "IN PROGRESS", 0, fp(1)),    // sudah menyerahkan sebelum lengkap
		memPhase("a", "y", "Penerus", "high", "BACKLOG", "IN PROGRESS", 1, fp(2)), // PIC saat dinyatakan lengkap
		memPhase("imp", "x", "Awal", "high", "BACKLOG", "DONE", 0, fp(1)),
	}
	out := memberPerformance(tasks, phases, nil, memAt(6))
	if m := memberByName(out, "Penerus"); m == nil || m.ReadinessCount != 1 || m.ReadinessAvgHours == nil || !near(*m.ReadinessAvgHours, 48) {
		t.Errorf("Penerus = %+v, want 1 task siap, 48 jam", m)
	}
	if m := memberByName(out, "Awal"); m == nil || m.ReadinessCount != 0 {
		t.Errorf("Awal = %+v, tidak dinilai (bukan PIC saat dinyatakan lengkap; task import tanpa data)", m)
	}
}

// Fase BLOCKED dan status kustom: BLOCKED bukan fase kerja, QA (kustom) fase kerja.
func TestMemberPerformance_WorkPhaseRule(t *testing.T) {
	tasks := []repository.PerfTask{memTask("a", "medium", "DONE", nil, 0)}
	phases := []repository.PerfMemberPhase{
		memPhase("a", "u", "Budi", "medium", "BLOCKED", "DONE", 0, fp(5)),
		memPhase("a", "u", "Budi", "medium", "QA", "DONE", 5, fp(6)),
	}
	out := memberPerformance(tasks, phases, nil, memAt(7))
	m := memberByName(out, "Budi")
	if m == nil || m.TotalCount != 1 || m.AvgCompletionHours == nil || !near(*m.AvgCompletionHours, 24) {
		t.Errorf("Budi = %+v, want 1 task kerja, hanya fase QA (24 jam) yang dihitung", m)
	}
}

func TestSetCompleteness_OnlyActiveBacklogPIC(t *testing.T) {
	statuses := map[string]*repository.CustomStatus{}
	newSvc := func(status string, notPic bool) *TaskService {
		repo := &fakeTaskRepo{byID: map[string]*repository.Task{
			"t1": {ID: "t1", ProjectID: "p1", StatusName: status, CreatedBy: "creator"},
		}}
		return NewTaskService(repo, &fakeTaskPics{notPic: notPic}, &fakeTaskDeps{}, &fakeTaskSessions{},
			&fakeTaskProjects{workspaceID: "ws1"}, &fakeTaskStatuses{byID: statuses},
			&fakeSprintRBAC{role: "project_manager"}, &fakeSprintProjectRoles{found: false}, &fakeTaskRules{}, &fakeTaskSprints{})
	}

	if err := newSvc("BACKLOG", false).SetCompleteness(context.Background(), nil, "t1", "complete", "pic1", "member"); err != nil {
		t.Errorf("PIC backlog aktif harus boleh: %v", err)
	}
	// pembuat task yang BUKAN PIC aktif (mis. sudah menyerahkan) tidak boleh lagi
	if err := newSvc("BACKLOG", true).SetCompleteness(context.Background(), nil, "t1", "complete", "creator", "member"); !errors.Is(err, domain.ErrNotBacklogPic) {
		t.Errorf("pembuat non-PIC: err = %v, want ErrNotBacklogPic", err)
	}
	// PM yang bukan PIC juga tidak boleh
	if err := newSvc("BACKLOG", true).SetCompleteness(context.Background(), nil, "t1", "complete", "pm1", "member"); !errors.Is(err, domain.ErrNotBacklogPic) {
		t.Errorf("PM non-PIC: err = %v, want ErrNotBacklogPic", err)
	}
	// task di luar BACKLOG: PIC fase lain tidak boleh mengubah kelengkapan
	if err := newSvc("IN PROGRESS", false).SetCompleteness(context.Background(), nil, "t1", "incomplete", "pic1", "member"); !errors.Is(err, domain.ErrNotBacklogPic) {
		t.Errorf("task bukan BACKLOG: err = %v, want ErrNotBacklogPic", err)
	}
}

// Acknowledge Rate / Handoff Delay (IG-126 susulan): hanya serah terima NYATA yang
// dihitung; fase lama tanpa acknowledge bukan "pending".
func TestClassifyAck(t *testing.T) {
	act := memAt(0)
	later := memAt(0.25) // 6 jam
	cases := []struct {
		name  string
		phase repository.PerfPicPhase
		want  ackKind
		hours float64
	}{
		{"di-acknowledge 6 jam kemudian", repository.PerfPicPhase{ActivatedAt: act, AcknowledgedAt: &later}, ackDone, 6},
		{"fase aktif belum acknowledge", repository.PerfPicPhase{ActivatedAt: act, IsActive: true}, ackPending, 0},
		{"fase lama diserahkan tanpa acknowledge", repository.PerfPicPhase{ActivatedAt: act, IsActive: false}, ackUnconfirmed, 0},
		{"PIC backlog otomatis pembuat task (ditetapkan sendiri)", repository.PerfPicPhase{ActivatedAt: act, IsActive: true, SelfAssigned: true}, ackAuto, 0},
		{"diterima otomatis saat itu juga (import)", repository.PerfPicPhase{ActivatedAt: act, AcknowledgedAt: &act}, ackAuto, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, hours := classifyAck(&tc.phase)
			if kind != tc.want || !near(hours, tc.hours) {
				t.Errorf("classifyAck = (%v, %.2f), want (%v, %.2f)", kind, hours, tc.want, tc.hours)
			}
		})
	}
}

func TestMemberPerformance_AckCounts(t *testing.T) {
	act := memAt(0)
	ackAt := memAt(0.5) // 12 jam
	acks := []repository.PerfPicPhase{
		{UserID: "u", UserName: "Budi", ActivatedAt: act, AcknowledgedAt: &ackAt},             // delay 12 jam
		{UserID: "u", UserName: "Budi", ActivatedAt: act, IsActive: true},                     // pending
		{UserID: "u", UserName: "Budi", ActivatedAt: act},                                     // tidak dikonfirmasi
		{UserID: "u", UserName: "Budi", ActivatedAt: act, IsActive: true, SelfAssigned: true}, // otomatis: tidak dihitung
		{UserID: "u", UserName: "Budi", ActivatedAt: act, AcknowledgedAt: &act},               // import: tidak dihitung
	}
	out := memberPerformance(nil, nil, acks, memAt(1))
	m := memberByName(out, "Budi")
	if m == nil || m.AckAvgHours == nil || !near(*m.AckAvgHours, 12) || m.AckPendingCount != 1 || m.AckUnconfirmedCount != 1 {
		t.Errorf("Budi = %+v, want ack rata-rata 12 jam, 1 pending, 1 tidak dikonfirmasi", m)
	}
}
