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

func TestBuildWorkspaceInvitationEmailMessage(t *testing.T) {
	expiresAt := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	msg := string(buildWorkspaceInvitationEmailMessage(
		"noreply@prodo.local", "budi@example.com", "Tim Marketing", "Siti Aminah", "editor",
		"http://localhost:5173/invitations/accept?token=xyz789", expiresAt,
	))

	checks := []string{
		"From: noreply@prodo.local",
		"To: budi@example.com",
		"Subject: Undangan Bergabung ke Workspace Tim Marketing - PRODO",
		"Siti Aminah mengundang",
		"workspace \"Tim Marketing\"",
		"role editor",
		"http://localhost:5173/invitations/accept?token=xyz789",
		"27 August 2026",
	}
	for _, want := range checks {
		if !strings.Contains(msg, want) {
			t.Errorf("pesan email tidak mengandung %q\n---\n%s", want, msg)
		}
	}

	if strings.Contains(strings.ToLower(msg), "password") && !strings.Contains(msg, "menyetel password") {
		t.Error("email tidak boleh memuat password asli, hanya link untuk menyetelnya")
	}
}

func TestBuildPlatformAdminLoginAlertMessage(t *testing.T) {
	loginTime := time.Date(2026, 8, 24, 9, 15, 0, 0, time.UTC)
	msg := string(buildPlatformAdminLoginAlertMessage(
		"noreply@prodo.local", "pa@example.com", "PA Demo", "203.0.113.5", "Chrome 125 di Windows", loginTime,
	))

	checks := []string{
		"From: noreply@prodo.local",
		"To: pa@example.com",
		"Subject: Peringatan Login Platform Admin - PRODO",
		"Halo PA Demo,",
		"203.0.113.5",
		"Chrome 125 di Windows",
		"24 August 2026",
	}
	for _, want := range checks {
		if !strings.Contains(msg, want) {
			t.Errorf("pesan email tidak mengandung %q\n---\n%s", want, msg)
		}
	}
}
