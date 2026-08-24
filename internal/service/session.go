package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/mtaaufaan/prodo-backend/internal/cache"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// slidingIdleTimeout -- US-004 AC "sliding expiration idle > 30 menit"
// (S1-28): sesi dianggap berakhir kalau tidak ada request selama ini,
// terlepas dari masa berlaku JWT sendiri.
const slidingIdleTimeout = 30 * time.Minute

// sessionRepository -- interface didefinisikan di consumer, lihat §3.9.
type sessionRepository interface {
	CreateSession(ctx context.Context, userID, jti string, device repository.DeviceInfo, expiresAt time.Time) error
	ListActiveSessions(ctx context.Context, userID string) ([]repository.Session, error)
	TouchSession(ctx context.Context, jti string, idleTimeout time.Duration) (valid bool, err error)
	TouchSessionFixed(ctx context.Context, jti string, fixedTimeout time.Duration) (valid bool, err error)
	RevokeSession(ctx context.Context, userID, jti string) (remaining time.Duration, err error)
	RevokeAllSessions(ctx context.Context, userID, exceptJTI string) ([]repository.RevokedSession, error)
	IsUserInOrg(ctx context.Context, userID, orgID string) (bool, error)
}

// SessionService menangani tracking sesi JWT (S1-27/28/29/32/33/34/35,
// US-004/US-005): dibuat saat login, sliding expiration lewat idle
// timeout, revoke satu/semua sesi dengan Redis blacklist untuk penolakan
// cepat tanpa query DB (docs/DATABASE_SCHEMA.md §5.3).
type SessionService struct {
	repo          sessionRepository
	cache         cache.Cache
	paIdleTimeout time.Duration
}

func NewSessionService(repo sessionRepository, c cache.Cache, paIdleTimeout time.Duration) *SessionService {
	return &SessionService{repo: repo, cache: c, paIdleTimeout: paIdleTimeout}
}

// blacklistKey -- satu key per jti, TTL = sisa masa berlaku token (lewat
// itu JWT sendiri sudah expired, tidak perlu diingat lagi).
func blacklistKey(jti string) string {
	return "session:revoked:" + jti
}

// RecordSession menyimpan sesi baru tepat setelah login berhasil (S1-27).
// jti dan waktu expiry diambil dari klaim access_token yang diterbitkan
// Keycloak (diteruskan apa adanya, tidak diverifikasi ulang di sini --
// token ini baru saja diterbitkan langsung oleh Keycloak lewat panggilan
// server-to-server, sama seperti alasan LoginSSO tidak verifikasi ulang).
func (s *SessionService) RecordSession(ctx context.Context, userID, accessToken, userAgent, ip string) error {
	claims := &jwt.RegisteredClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(accessToken, claims); err != nil {
		return fmt.Errorf("service.RecordSession: decode access_token: %w", err)
	}
	if claims.ID == "" || claims.ExpiresAt == nil {
		return fmt.Errorf("service.RecordSession: access_token tidak punya klaim jti/exp")
	}

	browser, os := parseUserAgent(userAgent)
	device := repository.DeviceInfo{Browser: browser, OS: os, IP: ip}

	if err := s.repo.CreateSession(ctx, userID, claims.ID, device, claims.ExpiresAt.Time); err != nil {
		return fmt.Errorf("service.RecordSession: %w", err)
	}
	return nil
}

// SessionSummary -- satu baris respons GET /auth/sessions (API_CONTRACT.md §2).
type SessionSummary struct {
	JTI          string
	Browser      string
	OS           string
	IP           string
	CreatedAt    time.Time
	LastActiveAt time.Time
	IsCurrent    bool
}

// ListSessions mengembalikan sesi aktif milik user (S1-29), menandai
// currentJTI (dari token request yang sedang dipakai) sebagai is_current.
func (s *SessionService) ListSessions(ctx context.Context, userID, currentJTI string) ([]SessionSummary, error) {
	sessions, err := s.repo.ListActiveSessions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("service.ListSessions: %w", err)
	}
	out := make([]SessionSummary, len(sessions))
	for i := range sessions {
		sess := &sessions[i]
		out[i] = SessionSummary{
			JTI:          sess.JTI,
			Browser:      sess.DeviceInfo.Browser,
			OS:           sess.DeviceInfo.OS,
			IP:           sess.DeviceInfo.IP,
			CreatedAt:    sess.CreatedAt,
			LastActiveAt: sess.LastActiveAt,
			IsCurrent:    sess.JTI == currentJTI,
		}
	}
	return out, nil
}

// IsValidSession dipanggil JWT middleware di SETIAP request terautentikasi
// (S1-28): Redis blacklist dicek dulu (cepat, hindari query DB untuk token
// yang sudah jelas direvoke -- DATABASE_SCHEMA.md §5.3), baru TouchSession
// di Postgres yang sekaligus menegakkan idle timeout DAN memperbarui
// last_active_at dalam satu query atomik.
//
// platformRole menentukan kebijakan sesi mana yang berlaku (S4P-14/15,
// implementation_gaps.md IG-20): Platform Admin pakai TouchSessionFixed
// (non-sliding, sesuai desain "sliding disabled") dengan timeout jauh
// lebih ketat (paIdleTimeout); role lain TETAP pakai TouchSession sliding
// 30 menit seperti semula -- TIDAK terpengaruh perubahan ini.
func (s *SessionService) IsValidSession(ctx context.Context, jti, platformRole string) (bool, error) {
	_, err := s.cache.Get(ctx, blacklistKey(jti))
	if err == nil {
		return false, nil // ada di blacklist -> revoked
	}
	if err != cache.ErrNotFound {
		return false, fmt.Errorf("service.IsValidSession: cek blacklist: %w", err)
	}

	if platformRole == "platform_admin" {
		valid, err := s.repo.TouchSessionFixed(ctx, jti, s.paIdleTimeout)
		if err != nil {
			return false, fmt.Errorf("service.IsValidSession: %w", err)
		}
		return valid, nil
	}

	valid, err := s.repo.TouchSession(ctx, jti, slidingIdleTimeout)
	if err != nil {
		return false, fmt.Errorf("service.IsValidSession: %w", err)
	}
	return valid, nil
}

// RevokeSession meng-set revoked_at (S1-33, "akhiri sesi ini") dan
// menambahkan jti ke Redis blacklist supaya token yang masih valid di
// client langsung ditolak tanpa menunggu expiry alami.
func (s *SessionService) RevokeSession(ctx context.Context, userID, jti string) error {
	remaining, err := s.repo.RevokeSession(ctx, userID, jti)
	if err != nil {
		return fmt.Errorf("service.RevokeSession: %w", err)
	}
	return s.blacklist(ctx, jti, remaining)
}

// RevokeAllSessions mengakhiri semua sesi aktif milik userID (S1-34 kalau
// exceptJTI diisi dengan sesi yang sedang dipakai -- "akhiri semua sesi
// lain"; S1-35 kalau exceptJTI kosong -- force logout total oleh PA/GA).
func (s *SessionService) RevokeAllSessions(ctx context.Context, userID, exceptJTI string) error {
	revoked, err := s.repo.RevokeAllSessions(ctx, userID, exceptJTI)
	if err != nil {
		return fmt.Errorf("service.RevokeAllSessions: %w", err)
	}
	for _, rs := range revoked {
		if err := s.blacklist(ctx, rs.JTI, rs.Remaining); err != nil {
			return err
		}
	}
	return nil
}

// IsUserInOrg -- pass-through tipis ke repo (S3-40), dipakai
// middleware.RequireGroupAdminInOrg lewat handler.
func (s *SessionService) IsUserInOrg(ctx context.Context, userID, orgID string) (bool, error) {
	inOrg, err := s.repo.IsUserInOrg(ctx, userID, orgID)
	if err != nil {
		return false, fmt.Errorf("service.IsUserInOrg: %w", err)
	}
	return inOrg, nil
}

func (s *SessionService) blacklist(ctx context.Context, jti string, remaining time.Duration) error {
	if remaining <= 0 {
		return nil // token sudah/segera expired sendiri, tidak perlu masuk blacklist
	}
	if err := s.cache.Set(ctx, blacklistKey(jti), "1", remaining); err != nil {
		return fmt.Errorf("service.blacklist: %w", err)
	}
	return nil
}

// parseUserAgent mengekstrak nama browser+OS yang mudah dibaca dari header
// User-Agent (API_CONTRACT.md §2 respons GET /auth/sessions butuh
// "browser"/"os", bukan string User-Agent mentah). Heuristik substring
// sederhana untuk kombinasi umum -- BUKAN parser UA lengkap (tidak
// menambah dependency pihak ketiga untuk label kosmetik di halaman "Sesi
// Aktif"; kalau kombinasi browser/OS langka meleset, tampilannya cuma
// fallback ke "Browser Lain"/"OS Lain", bukan salah data keamanan).
func parseUserAgent(ua string) (browser, os string) {
	browser = detectToken(ua, []tokenMatch{
		{"Edg/", "Edge"},
		{"OPR/", "Opera"},
		{"Chrome/", "Chrome"},
		{"Firefox/", "Firefox"},
		{"Safari/", "Safari"},
	}, "Browser Lain")

	os = detectToken(ua, []tokenMatch{
		{"Windows NT", "Windows"},
		{"Mac OS X", "macOS"},
		{"Android", "Android"},
		{"iPhone", "iOS"},
		{"iPad", "iOS"},
		{"Linux", "Linux"},
	}, "OS Lain")

	return browser, os
}

type tokenMatch struct {
	marker string
	label  string
}

func detectToken(ua string, matches []tokenMatch, fallback string) string {
	for _, m := range matches {
		if idx := strings.Index(ua, m.marker); idx != -1 {
			version := extractVersion(ua[idx+len(m.marker):])
			if version != "" {
				return m.label + " " + version
			}
			return m.label
		}
	}
	return fallback
}

// extractVersion mengambil angka mayor di awal string versi (mis. "125.0.1" -> "125").
func extractVersion(s string) string {
	end := strings.IndexAny(s, ".; )")
	if end == -1 {
		end = len(s)
	}
	if _, err := strconv.Atoi(s[:end]); err != nil {
		return ""
	}
	return s[:end]
}
