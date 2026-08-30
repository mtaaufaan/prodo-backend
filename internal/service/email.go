package service

import (
	"context"
	"fmt"
	"net/smtp"
	"time"
)

// EmailService mengirim email transaksional lewat SMTP. Dev pakai Mailpit
// (tanpa auth, lihat infra/docker-compose.dev.yml) -- SMTPUser/Pass kosong
// berarti smtp.PlainAuth tidak dipakai sama sekali.
type EmailService struct {
	host string
	port int
	from string
	auth smtp.Auth
}

func NewEmailService(host string, port int, from, user, pass string) *EmailService {
	var auth smtp.Auth
	if user != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}
	return &EmailService{host: host, port: port, from: from, auth: auth}
}

// SendActivationEmail mengirim email aktivasi Group Admin (US-073 AC: berisi
// one-time link + masa berlaku, TIDAK memuat password).
//
// ponytail: net/smtp.SendMail tidak menerima context (stdlib tidak
// mendukungnya) jadi ctx tidak dipakai untuk timeout dial -- Mailpit lokal,
// latensi bukan risiko nyata. Tambah wrapper context/deadline kalau nanti
// diarahkan ke SMTP relay produksi yang bisa lambat/hang.
func (e *EmailService) SendActivationEmail(_ context.Context, to, displayName, activationLink string, expiresAt time.Time) error {
	msg := buildActivationEmailMessage(e.from, to, displayName, activationLink, expiresAt)

	addr := fmt.Sprintf("%s:%d", e.host, e.port)
	if err := smtp.SendMail(addr, e.auth, e.from, []string{to}, msg); err != nil {
		return fmt.Errorf("service.SendActivationEmail: %w", err)
	}
	return nil
}

// SendWorkspaceInvitationEmail mengirim email undangan bergabung ke
// workspace (S2-17, US-006) -- berisi one-time link (TTL 72 jam), nama
// workspace, dan role yang akan diberikan.
func (e *EmailService) SendWorkspaceInvitationEmail(_ context.Context, to, workspaceName, inviterName, role, acceptLink string, expiresAt time.Time) error {
	msg := buildWorkspaceInvitationEmailMessage(e.from, to, workspaceName, inviterName, role, acceptLink, expiresAt)

	addr := fmt.Sprintf("%s:%d", e.host, e.port)
	if err := smtp.SendMail(addr, e.auth, e.from, []string{to}, msg); err != nil {
		return fmt.Errorf("service.SendWorkspaceInvitationEmail: %w", err)
	}
	return nil
}

// SendPlatformAdminLoginAlertEmail mengirim notifikasi setiap login
// Platform Admin (S4P-16, implementation_gaps.md IG-20) -- waktu, IP, dan
// device, sesuai AC US-070. Dikirim best-effort SETELAH login benar-benar
// sukses (AuthService.Login) -- kegagalan kirim email TIDAK boleh
// menggagalkan login itu sendiri (beda dari RecordLogin/RecordSession
// yang memang harus menggagalkan login kalau gagal, karena keduanya
// bagian dari audit/keamanan inti, bukan notifikasi tambahan).
func (e *EmailService) SendPlatformAdminLoginAlertEmail(_ context.Context, to, displayName, ip, device string, loginTime time.Time) error {
	msg := buildPlatformAdminLoginAlertMessage(e.from, to, displayName, ip, device, loginTime)

	addr := fmt.Sprintf("%s:%d", e.host, e.port)
	if err := smtp.SendMail(addr, e.auth, e.from, []string{to}, msg); err != nil {
		return fmt.Errorf("service.SendPlatformAdminLoginAlertEmail: %w", err)
	}
	return nil
}

// SendTierChangedEmail memberitahu Group Admin saat tier grupnya diubah
// Platform Admin (S4P-09). Best-effort seperti email lain -- gagal kirim
// TIDAK menggagalkan perubahan tier itu sendiri (sudah tersimpan lewat
// AccountService.UpdateGroupAdmin sebelum email ini dipanggil).
func (e *EmailService) SendTierChangedEmail(_ context.Context, to, displayName, oldTier, newTier string) error {
	msg := buildTierChangedEmailMessage(e.from, to, displayName, oldTier, newTier)

	addr := fmt.Sprintf("%s:%d", e.host, e.port)
	if err := smtp.SendMail(addr, e.auth, e.from, []string{to}, msg); err != nil {
		return fmt.Errorf("service.SendTierChangedEmail: %w", err)
	}
	return nil
}

// SendWorkspaceAdminChangedEmail memberitahu admin lama+baru saat Admin
// Workspace penanggung jawab dialihkan (S4G-04, Track S4G, desain
// "GA Workspaces.dc.html": "Pengalihan memberi notifikasi ke admin lama
// dan baru"). Best-effort seperti email lain -- gagal kirim TIDAK
// menggagalkan pengalihan role itu sendiri.
func (e *EmailService) SendWorkspaceAdminChangedEmail(_ context.Context, to, displayName, workspaceName string, isNewAdmin bool) error {
	msg := buildWorkspaceAdminChangedEmailMessage(e.from, to, displayName, workspaceName, isNewAdmin)

	addr := fmt.Sprintf("%s:%d", e.host, e.port)
	if err := smtp.SendMail(addr, e.auth, e.from, []string{to}, msg); err != nil {
		return fmt.Errorf("service.SendWorkspaceAdminChangedEmail: %w", err)
	}
	return nil
}

// buildWorkspaceAdminChangedEmailMessage -- dipisah dari
// SendWorkspaceAdminChangedEmail supaya bisa di-unit-test tanpa koneksi
// SMTP nyata (pola sama dengan buildActivationEmailMessage).
func buildWorkspaceAdminChangedEmailMessage(from, to, displayName, workspaceName string, isNewAdmin bool) []byte {
	subject := fmt.Sprintf("Admin Workspace %s Berubah - PRODO", workspaceName)
	body := fmt.Sprintf(
		"Halo %s,\r\n\r\n"+
			"Peran Admin Workspace penanggung jawab untuk workspace \"%s\" telah\r\n"+
			"dialihkan ke orang lain. Anda tidak lagi memegang peran ini, tapi\r\n"+
			"akses Anda sebagai member workspace tetap berjalan.\r\n\r\n"+
			"-- Tim PRODO\r\n",
		displayName, workspaceName,
	)
	if isNewAdmin {
		body = fmt.Sprintf(
			"Halo %s,\r\n\r\n"+
				"Anda ditunjuk sebagai Admin Workspace penanggung jawab baru untuk\r\n"+
				"workspace \"%s\". Anda kini mengelola member, role, dan pengaturan\r\n"+
				"workspace ini.\r\n\r\n"+
				"-- Tim PRODO\r\n",
			displayName, workspaceName,
		)
	}

	return []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"UTF-8\"\r\n\r\n%s",
		from, to, subject, body,
	))
}

// buildTierChangedEmailMessage -- dipisah dari SendTierChangedEmail supaya
// bisa di-unit-test tanpa koneksi SMTP nyata (pola sama dengan
// buildActivationEmailMessage).
func buildTierChangedEmailMessage(from, to, displayName, oldTier, newTier string) []byte {
	subject := "Tier Grup Anda Berubah - PRODO"
	body := fmt.Sprintf(
		"Halo %s,\r\n\r\n"+
			"Tier grup Anda di PRODO diubah oleh Platform Admin, dari %s menjadi %s.\r\n"+
			"Batas fitur, kuota, dan retensi data grup Anda kini mengikuti tier baru.\r\n\r\n"+
			"-- Tim PRODO\r\n",
		displayName, oldTier, newTier,
	)

	return []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"UTF-8\"\r\n\r\n%s",
		from, to, subject, body,
	))
}

// buildPlatformAdminLoginAlertMessage -- dipisah dari
// SendPlatformAdminLoginAlertEmail supaya bisa di-unit-test tanpa koneksi
// SMTP nyata (pola sama dengan buildActivationEmailMessage).
func buildPlatformAdminLoginAlertMessage(from, to, displayName, ip, device string, loginTime time.Time) []byte {
	subject := "Peringatan Login Platform Admin - PRODO"
	body := fmt.Sprintf(
		"Halo %s,\r\n\r\n"+
			"Login berhasil pada %s dari %s menggunakan %s.\r\n\r\n"+
			"Jika ini bukan Anda, segera akhiri semua sesi lewat panel keamanan\r\n"+
			"Platform Admin dan hubungi tim operasional PRODO.\r\n\r\n"+
			"-- Tim PRODO\r\n",
		displayName, loginTime.Format("2 January 2006 15:04 MST"), ip, device,
	)

	return []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"UTF-8\"\r\n\r\n%s",
		from, to, subject, body,
	))
}

// buildWorkspaceInvitationEmailMessage -- dipisah dari
// SendWorkspaceInvitationEmail supaya bisa di-unit-test tanpa koneksi SMTP
// nyata (pola sama dengan buildActivationEmailMessage).
func buildWorkspaceInvitationEmailMessage(from, to, workspaceName, inviterName, role, acceptLink string, expiresAt time.Time) []byte {
	subject := fmt.Sprintf("Undangan Bergabung ke Workspace %s - PRODO", workspaceName)
	body := fmt.Sprintf(
		"Halo,\r\n\r\n"+
			"%s mengundang Anda untuk bergabung ke workspace \"%s\" di PRODO\r\n"+
			"dengan role %s. Klik link berikut untuk menerima undangan dan\r\n"+
			"menyetel password Anda:\r\n\r\n"+
			"%s\r\n\r\n"+
			"Link ini berlaku sampai %s (72 jam sejak dikirim) dan hanya bisa\r\n"+
			"dipakai satu kali. Jika Anda tidak mengenal pengirimnya, abaikan email ini.\r\n\r\n"+
			"-- Tim PRODO\r\n",
		inviterName, workspaceName, role, acceptLink, expiresAt.Format("2 January 2006 15:04 MST"),
	)

	return []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"UTF-8\"\r\n\r\n%s",
		from, to, subject, body,
	))
}

// buildActivationEmailMessage menyusun pesan RFC 5322 mentah (header +
// body) -- dipisah dari SendActivationEmail supaya bisa di-unit-test tanpa
// koneksi SMTP nyata.
func buildActivationEmailMessage(from, to, displayName, activationLink string, expiresAt time.Time) []byte {
	subject := "Aktivasi Akun Group Admin - PRODO"
	body := fmt.Sprintf(
		"Halo %s,\r\n\r\n"+
			"Akun Group Admin Anda di PRODO telah dibuat. Klik link berikut untuk\r\n"+
			"mengaktifkan akun dan menyetel password Anda:\r\n\r\n"+
			"%s\r\n\r\n"+
			"Link ini berlaku sampai %s (72 jam sejak dibuat) dan hanya bisa dipakai\r\n"+
			"satu kali. Jika Anda tidak merasa meminta ini, abaikan email ini.\r\n\r\n"+
			"-- Tim PRODO\r\n",
		displayName, activationLink, expiresAt.Format("2 January 2006 15:04 MST"),
	)

	return []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"UTF-8\"\r\n\r\n%s",
		from, to, subject, body,
	))
}
