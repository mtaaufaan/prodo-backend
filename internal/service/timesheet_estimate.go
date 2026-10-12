// Package service -- penghitung ESTIMASI jam tercatat dari riwayat tanggal
// status task hasil import CSV (IG-129). Data import hanya membawa tanggal
// perpindahan status dan PIC tiap fase, bukan jam kerja, sehingga jam
// diperkirakan: 1 hari kerja (Senin-Jumat) = 8 jam per orang.
//
// Aturan (disepakati user, "OK default"):
//  1. fase kerja = status selain BACKLOG/DONE/BLOCKED/CANCELED (selaras Member
//     Performance IG-126); BACKLOG dihitung dari task dibuat sampai dinyatakan
//     lengkap, dikreditkan ke PIC backlog;
//  2. hari kerja Senin-Jumat dari tanggal masuk fase sampai SEBELUM tanggal
//     keluar; fase yang selesai di hari yang sama (atau tanpa hari kerja)
//     dihitung 1 hari;
//  3. maksimal 10 hari kerja pertama per fase;
//  4. beberapa PIC pada satu fase membagi rata jam fase itu;
//  5. satu orang paling banyak 8 jam per hari -- bila memegang beberapa task
//     paralel, 8 jam hari itu dibagi proporsional;
//  6. satu entri per orang per task per hari, berurutan mulai 09:00 WIB.
package service

import (
	"sort"
	"time"
)

const (
	estimateDayMinutes = 8 * 60
	estimateCapDays    = 10
)

// estimateZone -- WIB tetap (UTC+7): entri harian dimulai 09:00 WIB.
var estimateZone = time.FixedZone("WIB", 7*60*60)

// EstimatePhase -- satu fase yang diestimasi. End kosong (IsZero) = fase masih
// berjalan, dihitung sampai `now`.
type EstimatePhase struct {
	TaskID   string
	TaskCode string
	Phase    string
	Start    time.Time
	End      time.Time
	PICs     []string
}

// EstimateEntry -- satu entri waktu hasil estimasi.
type EstimateEntry struct {
	TaskID    string
	UserID    string
	Phase     string
	StartedAt time.Time
	EndedAt   time.Time
	Minutes   int
}

func isWorkday(d time.Time) bool {
	w := d.Weekday()
	return w != time.Saturday && w != time.Sunday
}

func dateOf(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// phaseDays -- hari kerja yang dikreditkan sebuah fase (lihat aturan 2-3).
func phaseDays(start, end time.Time) []time.Time {
	s, e := dateOf(start), dateOf(end)
	days := make([]time.Time, 0, estimateCapDays)
	for d := s; d.Before(e) && len(days) < estimateCapDays; d = d.AddDate(0, 0, 1) {
		if isWorkday(d) {
			days = append(days, d)
		}
	}
	if len(days) == 0 {
		d := s
		for !isWorkday(d) {
			d = d.AddDate(0, 0, 1)
		}
		days = append(days, d)
	}
	return days
}

type estimateDemand struct {
	taskID, taskCode, phase string
	weight                  float64
}

type personDay struct {
	user string
	day  time.Time
}

// EstimateEntries -- fungsi murni: fase -> entri harian (deterministik).
func EstimateEntries(phases []EstimatePhase, now time.Time) []EstimateEntry {
	demands := map[personDay][]estimateDemand{}
	for i := range phases {
		p := &phases[i]
		if len(p.PICs) == 0 {
			continue
		}
		end := p.End
		if end.IsZero() {
			end = now
		}
		w := 1 / float64(len(p.PICs))
		for _, day := range phaseDays(p.Start, end) {
			for _, u := range p.PICs {
				k := personDay{u, day}
				demands[k] = append(demands[k], estimateDemand{p.TaskID, p.TaskCode, p.Phase, w})
			}
		}
	}

	keys := make([]personDay, 0, len(demands))
	for k := range demands {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].user != keys[j].user {
			return keys[i].user < keys[j].user
		}
		return keys[i].day.Before(keys[j].day)
	})

	var out []EstimateEntry
	for _, k := range keys {
		ds := demands[k]
		sort.Slice(ds, func(i, j int) bool {
			if ds[i].taskCode != ds[j].taskCode {
				return ds[i].taskCode < ds[j].taskCode
			}
			return ds[i].phase < ds[j].phase
		})
		total := 0.0
		for _, d := range ds {
			total += d.weight
		}
		scale := 1.0
		if total > 1 {
			scale = 1 / total
		}
		cursor := k.day.Add(2 * time.Hour) // 09:00 WIB = 02:00 UTC
		for _, d := range ds {
			mins := int(estimateDayMinutes * d.weight * scale)
			if mins < 1 {
				mins = 1
			}
			end := cursor.Add(time.Duration(mins) * time.Minute)
			out = append(out, EstimateEntry{TaskID: d.taskID, UserID: k.user, Phase: d.phase, StartedAt: cursor.In(estimateZone), EndedAt: end.In(estimateZone), Minutes: mins})
			cursor = end
		}
	}
	return out
}
