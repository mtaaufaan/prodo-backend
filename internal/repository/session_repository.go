// Package repository -- SessionRepository (S1-27/28/29/30/33/34/35, US-004/
// US-005). Tabel platform-level (users-scoped, bukan tenant-scoped) sama
// seperti AccountRepository, lihat docs/RLS_DESIGN.md §8 -- tidak di-RLS.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type SessionRepository struct {
	db *pgxpool.Pool
}

func NewSessionRepository(db *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{db: db}
}

// IsUserInOrg mengecek apakah userID adalah member SALAH SATU workspace
// dalam orgID (S3-40, implementation_gaps.md IG-01) -- dasar otorisasi
// Group Admin ter-scope di endpoint sesi admin (S1-30/35). Tidak ada kolom
// org_id langsung di `users`; keanggotaan org diturunkan lewat rantai
// workspace_members -> workspaces.org_id, sama pola dengan
// middleware.RequireGroupAdminInOrg (S3-39) yang memanggil ini per org di
// claims.ProdoOrgIDs GA.
// Query lewat function SQL SECURITY DEFINER prodo_user_in_org (migrasi
// 20260827120000), BUKAN JOIN langsung ke workspace_members/workspaces dari
// sini. Route ini (SessionHandler.ListForUser/RevokeAllForUser) SENGAJA
// tidak pakai DBContextMiddleware (SessionRepository pool langsung, bukan
// db.Executor) -- begitu workspace_members/workspaces kena RLS (S3-42),
// JOIN langsung tanpa session variable RLS akan diam-diam kena filter (0
// baris) meski secara logis benar. Lihat implementation_gaps.md IG-14.
func (r *SessionRepository) IsUserInOrg(ctx context.Context, userID, orgID string) (bool, error) {
	var exists bool
	if err := r.db.QueryRow(ctx, `SELECT prodo_user_in_org($1, $2)`, userID, orgID).Scan(&exists); err != nil {
		return false, fmt.Errorf("repository.IsUserInOrg: %w", err)
	}
	return exists, nil
}

// DeviceInfo -- lihat docs/DATABASE_SCHEMA.md §5.3. Browser/OS sudah
// diparse dari User-Agent SEKALI saat login (bukan disimpan mentah lalu
// diparse ulang tiap GET /auth/sessions, lihat parseUserAgent di
// service/session.go), supaya sesuai bentuk respons API_CONTRACT.md §2 apa
// adanya tanpa transform di response path.
type DeviceInfo struct {
	Browser string `json:"browser"`
	OS      string `json:"os"`
	IP      string `json:"ip"`
}

// Session adalah satu baris user_sessions.
type Session struct {
	JTI          string
	UserID       string
	DeviceInfo   DeviceInfo
	CreatedAt    time.Time
	ExpiresAt    time.Time
	LastActiveAt time.Time
}

// CreateSession menyimpan sesi baru (S1-27) tepat setelah login berhasil.
func (r *SessionRepository) CreateSession(ctx context.Context, userID, jti string, device DeviceInfo, expiresAt time.Time) error {
	deviceJSON, err := json.Marshal(device)
	if err != nil {
		return fmt.Errorf("repository.CreateSession: encode device_info: %w", err)
	}
	if _, err := r.db.Exec(ctx, `
		INSERT INTO user_sessions (user_id, jti, device_info, expires_at)
		VALUES ($1, $2, $3::jsonb, $4)
	`, userID, jti, string(deviceJSON), expiresAt); err != nil {
		return fmt.Errorf("repository.CreateSession: %w", err)
	}
	return nil
}

// ListActiveSessions mengembalikan sesi aktif (belum revoked, belum
// expired) milik satu user, terbaru dulu (S1-29).
func (r *SessionRepository) ListActiveSessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := r.db.Query(ctx, `
		SELECT jti, device_info, created_at, expires_at, last_active_at
		FROM user_sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY last_active_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListActiveSessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		var deviceJSON []byte
		if err := rows.Scan(&s.JTI, &deviceJSON, &s.CreatedAt, &s.ExpiresAt, &s.LastActiveAt); err != nil {
			return nil, fmt.Errorf("repository.ListActiveSessions: scan: %w", err)
		}
		if len(deviceJSON) > 0 {
			if err := json.Unmarshal(deviceJSON, &s.DeviceInfo); err != nil {
				return nil, fmt.Errorf("repository.ListActiveSessions: decode device_info: %w", err)
			}
		}
		s.UserID = userID
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListActiveSessions: rows: %w", err)
	}
	return sessions, nil
}

// TouchSession adalah pengecekan+update ATOMIK satu query (S1-28, sliding
// expiration): sesi dianggap valid HANYA kalau belum revoked, belum expired
// (exp JWT), DAN tidak idle lebih dari idleTimeout -- kalau valid,
// last_active_at langsung diperbarui di query yang sama. Dipanggil dari
// middleware JWT di setiap request terautentikasi (lihat
// docs/DATABASE_SCHEMA.md §5.3 "diperbarui setiap request berhasil").
func (r *SessionRepository) TouchSession(ctx context.Context, jti string, idleTimeout time.Duration) (valid bool, err error) {
	var id string
	err = r.db.QueryRow(ctx, `
		UPDATE user_sessions
		SET last_active_at = NOW()
		WHERE jti = $1
		  AND revoked_at IS NULL
		  AND expires_at > NOW()
		  AND last_active_at > NOW() - $2::interval
		RETURNING id
	`, jti, idleTimeout.String()).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("repository.TouchSession: %w", err)
	}
	return true, nil
}

// TouchSessionFixed adalah versi NON-sliding TouchSession (S4P-14/15,
// implementation_gaps.md IG-20): dipakai KHUSUS Platform Admin karena
// desain (Platform Admin Login.dc.html, "sliding disabled") mensyaratkan
// sesi berakhir tepat pada waktu tetap sejak login, TIDAK diperpanjang
// oleh aktivitas -- beda dari TouchSession (role lain) yang memperbarui
// last_active_at sebagai basis pengecekan berikutnya (sliding). Di sini
// last_active_at TETAP diperbarui (dipakai murni untuk tampilan "terakhir
// aktif" di GET /auth/sessions), tapi validitas sesi dicek terhadap
// created_at yang TIDAK PERNAH berubah.
//
// Timeout dibaca langsung dari platform_settings (S4P-18, singleton id=1)
// lewat subquery -- BUKAN parameter Go lagi seperti sebelum S4 H3, supaya
// perubahan lewat PlatformSecuritySettings langsung berlaku tanpa redeploy
// (env var PA_SESSION_IDLE_TIMEOUT dihapus, S4P-15 tergantikan penuh).
func (r *SessionRepository) TouchSessionFixed(ctx context.Context, jti string) (valid bool, err error) {
	var id string
	err = r.db.QueryRow(ctx, `
		UPDATE user_sessions
		SET last_active_at = NOW()
		WHERE jti = $1
		  AND revoked_at IS NULL
		  AND expires_at > NOW()
		  AND created_at > NOW() - (SELECT pa_session_idle_timeout FROM platform_settings WHERE id = 1)
		RETURNING id
	`, jti).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("repository.TouchSessionFixed: %w", err)
	}
	return true, nil
}

// RevokeSession meng-set revoked_at (S1-33) -- HANYA kalau jti benar-benar
// milik userID (ownership check langsung di WHERE, bukan query terpisah
// -- mencegah TOCTOU dan sekaligus jadi satu-satunya jalan untuk
// membedakan "tidak ada" dari "punya user lain" tanpa membocorkan yang mana).
// Mengembalikan sisa masa berlaku token (untuk TTL Redis blacklist,
// lihat DATABASE_SCHEMA.md §5.3) -- domain.ErrSessionNotFound kalau tidak
// ada baris yang cocok.
func (r *SessionRepository) RevokeSession(ctx context.Context, userID, jti string) (remaining time.Duration, err error) {
	var expiresAt time.Time
	err = r.db.QueryRow(ctx, `
		UPDATE user_sessions
		SET revoked_at = NOW()
		WHERE jti = $1 AND user_id = $2 AND revoked_at IS NULL
		RETURNING expires_at
	`, jti, userID).Scan(&expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("repository.RevokeSession: %w", domain.ErrSessionNotFound)
		}
		return 0, fmt.Errorf("repository.RevokeSession: %w", err)
	}
	return time.Until(expiresAt), nil
}

// RevokeAllSessions meng-set revoked_at untuk SEMUA sesi aktif milik
// userID (S1-35, force logout) -- opsional exceptJTI untuk "akhiri semua
// KECUALI sesi ini" (S1-34). Mengembalikan jti+sisa masa berlaku tiap sesi
// yang direvoke, untuk caller push ke Redis blacklist (S1-32).
func (r *SessionRepository) RevokeAllSessions(ctx context.Context, userID, exceptJTI string) ([]RevokedSession, error) {
	rows, err := r.db.Query(ctx, `
		UPDATE user_sessions
		SET revoked_at = NOW()
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW() AND jti != $2
		RETURNING jti, expires_at
	`, userID, exceptJTI)
	if err != nil {
		return nil, fmt.Errorf("repository.RevokeAllSessions: %w", err)
	}
	defer rows.Close()

	var revoked []RevokedSession
	for rows.Next() {
		var rs RevokedSession
		var expiresAt time.Time
		if err := rows.Scan(&rs.JTI, &expiresAt); err != nil {
			return nil, fmt.Errorf("repository.RevokeAllSessions: scan: %w", err)
		}
		rs.Remaining = time.Until(expiresAt)
		revoked = append(revoked, rs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.RevokeAllSessions: rows: %w", err)
	}
	return revoked, nil
}

// RevokedSession -- satu entri hasil RevokeAllSessions, dipakai caller
// (SessionService) untuk mengisi Redis blacklist per jti.
type RevokedSession struct {
	JTI       string
	Remaining time.Duration
}
