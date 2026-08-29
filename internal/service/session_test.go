package service

import (
	"context"
	"testing"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/cache"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type stubSessionRepository struct {
	createErr                 error
	createdUserID, createdJTI string
	createdDevice             repository.DeviceInfo
	createdExpiresAt          time.Time

	listResult []repository.Session
	listErr    error

	touchValid bool
	touchErr   error
	touchedJTI string

	revokeRemaining time.Duration
	revokeErr       error

	revokeAllResult []repository.RevokedSession
	revokeAllErr    error

	isUserInOrgResult bool
	isUserInOrgErr    error

	renewValid    bool
	renewErr      error
	renewedOldJTI string
	renewedNewJTI string
}

func (f *stubSessionRepository) CreateSession(_ context.Context, userID, jti string, device repository.DeviceInfo, expiresAt time.Time) error {
	f.createdUserID, f.createdJTI, f.createdDevice, f.createdExpiresAt = userID, jti, device, expiresAt
	return f.createErr
}

func (f *stubSessionRepository) ListActiveSessions(_ context.Context, _ string) ([]repository.Session, error) {
	return f.listResult, f.listErr
}

func (f *stubSessionRepository) TouchSession(_ context.Context, jti string, _ time.Duration) (bool, error) {
	f.touchedJTI = jti
	return f.touchValid, f.touchErr
}

func (f *stubSessionRepository) TouchSessionFixed(_ context.Context, jti string) (bool, error) {
	f.touchedJTI = jti
	return f.touchValid, f.touchErr
}

func (f *stubSessionRepository) RevokeSession(_ context.Context, _, _ string) (time.Duration, error) {
	return f.revokeRemaining, f.revokeErr
}

func (f *stubSessionRepository) RevokeAllSessions(_ context.Context, _, _ string) ([]repository.RevokedSession, error) {
	return f.revokeAllResult, f.revokeAllErr
}

func (f *stubSessionRepository) IsUserInOrg(_ context.Context, _, _ string) (bool, error) {
	return f.isUserInOrgResult, f.isUserInOrgErr
}

func (f *stubSessionRepository) RenewSessionJTI(_ context.Context, oldJTI, newJTI string, _ time.Duration, _ time.Time) (bool, error) {
	f.renewedOldJTI, f.renewedNewJTI = oldJTI, newJTI
	return f.renewValid, f.renewErr
}

type stubCache struct {
	store map[string]string
}

func newStubCache() *stubCache { return &stubCache{store: map[string]string{}} }

func (c *stubCache) Get(_ context.Context, key string) (string, error) {
	v, ok := c.store[key]
	if !ok {
		return "", cache.ErrNotFound
	}
	return v, nil
}

func (c *stubCache) Set(_ context.Context, key, value string, _ time.Duration) error {
	c.store[key] = value
	return nil
}

func (c *stubCache) Del(_ context.Context, key string) error {
	delete(c.store, key)
	return nil
}

func (c *stubCache) Close() error { return nil }

func TestSessionService_RecordSession_DecodesJTIAndExp(t *testing.T) {
	repo := &stubSessionRepository{}
	svc := NewSessionService(repo, newStubCache())

	token := testAccessTokenJWT(t, "jti-123")
	err := svc.RecordSession(context.Background(), "user-1", token, "Mozilla/5.0 Chrome/125.0 Safari/537.36", "10.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.createdUserID != "user-1" || repo.createdJTI != "jti-123" {
		t.Errorf("CreateSession dipanggil dengan userID=%q jti=%q, want user-1/jti-123", repo.createdUserID, repo.createdJTI)
	}
	if repo.createdDevice.Browser != "Chrome 125" {
		t.Errorf("Browser = %q, want Chrome 125", repo.createdDevice.Browser)
	}
	if repo.createdDevice.IP != "10.0.0.1" {
		t.Errorf("IP = %q, want 10.0.0.1", repo.createdDevice.IP)
	}
	if repo.createdExpiresAt.IsZero() {
		t.Error("expiresAt tidak boleh kosong")
	}
}

func TestSessionService_ListSessions_MarksCurrent(t *testing.T) {
	repo := &stubSessionRepository{listResult: []repository.Session{
		{JTI: "jti-a", DeviceInfo: repository.DeviceInfo{Browser: "Chrome 125", OS: "Windows", IP: "1.1.1.1"}},
		{JTI: "jti-b", DeviceInfo: repository.DeviceInfo{Browser: "Safari 17", OS: "macOS", IP: "2.2.2.2"}},
	}}
	svc := NewSessionService(repo, newStubCache())

	out, err := svc.ListSessions(context.Background(), "user-1", "jti-b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if out[0].IsCurrent {
		t.Error("jti-a bukan sesi saat ini, harusnya IsCurrent=false")
	}
	if !out[1].IsCurrent {
		t.Error("jti-b adalah sesi saat ini, harusnya IsCurrent=true")
	}
}

func TestSessionService_IsValidSession_Blacklisted(t *testing.T) {
	c := newStubCache()
	c.store[blacklistKey("jti-revoked")] = "1"
	svc := NewSessionService(&stubSessionRepository{touchValid: true}, c)

	valid, err := svc.IsValidSession(context.Background(), "jti-revoked", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if valid {
		t.Error("sesi di blacklist harusnya invalid")
	}
}

func TestSessionService_IsValidSession_NotBlacklisted_DelegatesToTouchSession(t *testing.T) {
	repo := &stubSessionRepository{touchValid: true}
	svc := NewSessionService(repo, newStubCache())

	valid, err := svc.IsValidSession(context.Background(), "jti-fresh", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !valid {
		t.Error("sesi tidak di-blacklist dan TouchSession bilang valid -- harusnya lolos")
	}
	if repo.touchedJTI != "jti-fresh" {
		t.Errorf("TouchSession dipanggil dengan jti=%q, want jti-fresh", repo.touchedJTI)
	}
}

func TestSessionService_IsValidSession_IdleTimeout(t *testing.T) {
	// TouchSession repo-level yang menegakkan idle timeout (S1-28) --
	// diuji di sini lewat stub yang mensimulasikan repo bilang "tidak
	// valid" (baris tidak ke-update karena WHERE clause idle gagal).
	svc := NewSessionService(&stubSessionRepository{touchValid: false}, newStubCache())

	valid, err := svc.IsValidSession(context.Background(), "jti-idle", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if valid {
		t.Error("sesi idle > 30 menit harusnya invalid")
	}
}

func TestSessionService_IsValidSession_PlatformAdmin_UsesFixedNonSlidingTimeout(t *testing.T) {
	// S4P-14/15 (implementation_gaps.md IG-20): Platform Admin harus lewat
	// TouchSessionFixed (non-sliding), BUKAN TouchSession biasa.
	repo := &stubSessionRepository{touchValid: true}
	svc := NewSessionService(repo, newStubCache())

	valid, err := svc.IsValidSession(context.Background(), "jti-pa", "platform_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !valid {
		t.Error("sesi PA valid harusnya lolos lewat TouchSessionFixed")
	}
	if repo.touchedJTI != "jti-pa" {
		t.Errorf("TouchSessionFixed dipanggil dengan jti=%q, want jti-pa", repo.touchedJTI)
	}
}

func TestSessionService_IsValidSession_PlatformAdmin_FixedTimeoutExpired(t *testing.T) {
	svc := NewSessionService(&stubSessionRepository{touchValid: false}, newStubCache())

	valid, err := svc.IsValidSession(context.Background(), "jti-pa-idle", "platform_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if valid {
		t.Error("sesi PA melewati paIdleTimeout harusnya invalid, terlepas dari aktivitas (non-sliding)")
	}
}

func TestSessionService_RefreshSession_Blacklisted(t *testing.T) {
	c := newStubCache()
	c.store[blacklistKey("jti-revoked")] = "1"
	svc := NewSessionService(&stubSessionRepository{renewValid: true}, c)

	valid, err := svc.RefreshSession(context.Background(), "jti-revoked", "jti-new", time.Now().Add(5*time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if valid {
		t.Error("sesi di blacklist harusnya tidak bisa di-refresh")
	}
}

func TestSessionService_RefreshSession_DelegatesToRenewSessionJTI(t *testing.T) {
	repo := &stubSessionRepository{renewValid: true}
	svc := NewSessionService(repo, newStubCache())
	newExpiry := time.Now().Add(5 * time.Minute)

	valid, err := svc.RefreshSession(context.Background(), "jti-old", "jti-new", newExpiry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !valid {
		t.Error("RenewSessionJTI bilang valid -- RefreshSession harusnya lolos")
	}
	if repo.renewedOldJTI != "jti-old" || repo.renewedNewJTI != "jti-new" {
		t.Errorf("RenewSessionJTI dipanggil dengan oldJTI=%q newJTI=%q, want jti-old/jti-new", repo.renewedOldJTI, repo.renewedNewJTI)
	}
}

func TestSessionService_RefreshSession_IdleTimeoutExpired(t *testing.T) {
	// RenewSessionJTI repo-level yang menegakkan idle timeout (sliding
	// ATAU fixed PA, tergantung role di dalam query) -- diuji di sini
	// lewat stub yang mensimulasikan repo bilang "tidak valid" (baris
	// tidak ke-update karena WHERE clause idle gagal).
	svc := NewSessionService(&stubSessionRepository{renewValid: false}, newStubCache())

	valid, err := svc.RefreshSession(context.Background(), "jti-idle", "jti-new", time.Now().Add(5*time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if valid {
		t.Error("sesi yang sudah lewat idle-timeout harusnya tidak bisa di-refresh")
	}
}

func TestSessionService_RevokeSession_AddsToBlacklist(t *testing.T) {
	repo := &stubSessionRepository{revokeRemaining: 5 * time.Minute}
	c := newStubCache()
	svc := NewSessionService(repo, c)

	if err := svc.RevokeSession(context.Background(), "user-1", "jti-x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := c.store[blacklistKey("jti-x")]; !ok {
		t.Error("jti yang di-revoke harusnya masuk Redis blacklist")
	}
}

func TestSessionService_RevokeSession_AlreadyExpired_SkipsBlacklist(t *testing.T) {
	repo := &stubSessionRepository{revokeRemaining: -1 * time.Second}
	c := newStubCache()
	svc := NewSessionService(repo, c)

	if err := svc.RevokeSession(context.Background(), "user-1", "jti-x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.store) != 0 {
		t.Error("token yang sudah expired sendiri tidak perlu masuk blacklist")
	}
}

func TestSessionService_RevokeAllSessions_BlacklistsEach(t *testing.T) {
	repo := &stubSessionRepository{revokeAllResult: []repository.RevokedSession{
		{JTI: "jti-1", Remaining: time.Minute},
		{JTI: "jti-2", Remaining: 2 * time.Minute},
	}}
	c := newStubCache()
	svc := NewSessionService(repo, c)

	if err := svc.RevokeAllSessions(context.Background(), "user-1", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.store) != 2 {
		t.Errorf("len(c.store) = %d, want 2", len(c.store))
	}
}

func TestParseUserAgent(t *testing.T) {
	cases := []struct {
		ua          string
		wantBrowser string
		wantOS      string
	}{
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36", "Chrome 125", "Windows"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", "Safari 605", "macOS"},
		{"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Firefox/126.0", "Firefox 126", "Linux"},
		{"curl/8.4.0", "Browser Lain", "OS Lain"},
	}
	for _, c := range cases {
		browser, os := parseUserAgent(c.ua)
		if browser != c.wantBrowser {
			t.Errorf("parseUserAgent(%q) browser = %q, want %q", c.ua, browser, c.wantBrowser)
		}
		if os != c.wantOS {
			t.Errorf("parseUserAgent(%q) os = %q, want %q", c.ua, os, c.wantOS)
		}
	}
}
