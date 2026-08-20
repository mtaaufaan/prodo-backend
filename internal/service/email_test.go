package service

import (
	"strings"
	"testing"
	"time"
)

func TestBuildActivationEmailMessage(t *testing.T) {
	expiresAt := time.Date(2026, 8, 23, 17, 25, 0, 0, time.UTC)
	msg := string(buildActivationEmailMessage(
		"noreply@prodo.local", "ga@example.com", "Budi Santoso",
		"http://localhost:5173/activate?token=abc123", expiresAt,
	))

	checks := []string{
		"From: noreply@prodo.local",
		"To: ga@example.com",
		"Subject: Aktivasi Akun Group Admin - PRODO",
		"Halo Budi Santoso,",
		"http://localhost:5173/activate?token=abc123",
		"23 August 2026",
	}
	for _, want := range checks {
		if !strings.Contains(msg, want) {
			t.Errorf("pesan email tidak mengandung %q\n---\n%s", want, msg)
		}
	}

	if strings.Contains(strings.ToLower(msg), "password") && !strings.Contains(msg, "menyetel password") {
		t.Error("email tidak boleh memuat password asli (US-073 AC), hanya link untuk menyetelnya")
	}
}
