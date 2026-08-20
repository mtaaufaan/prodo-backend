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
