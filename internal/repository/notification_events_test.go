package repository

import "testing"

func TestNotificationEventsForScope(t *testing.T) {
	group, ok := NotificationEventsForScope(NotificationScopeGroup)
	if !ok || len(group) != len(GroupAdminNotificationEvents) {
		t.Fatalf("scope group = %d event ok=%v, want %d", len(group), ok, len(GroupAdminNotificationEvents))
	}
	ws, ok := NotificationEventsForScope(NotificationScopeWorkspace)
	if !ok || len(ws) != 6 {
		t.Fatalf("scope workspace = %d event ok=%v, want 6 (desain User Pengaturan Akun)", len(ws), ok)
	}
	if _, ok := NotificationEventsForScope("bukan-scope"); ok {
		t.Error("scope tidak dikenal harus ok=false")
	}
}

func TestIsKnownNotificationEvent(t *testing.T) {
	for _, key := range []string{"org.storage_quota_threshold", "task.assigned", "account.security_activity"} {
		if !IsKnownNotificationEvent(key) {
			t.Errorf("%s harus dikenal", key)
		}
	}
	if IsKnownNotificationEvent("event.palsu") {
		t.Error("event.palsu tidak boleh dikenal")
	}
}

// Kunci event dalam satu daftar tidak boleh ganda -- satu preferensi tersimpan
// per (user, event_type).
func TestNotificationEventKeysUnique(t *testing.T) {
	for name, list := range map[string][]NotificationEventType{"group": GroupAdminNotificationEvents, "workspace": WorkspaceNotificationEvents} {
		seen := map[string]bool{}
		for _, ev := range list {
			if seen[ev.Key] {
				t.Errorf("daftar %s: key ganda %s", name, ev.Key)
			}
			seen[ev.Key] = true
		}
	}
}
