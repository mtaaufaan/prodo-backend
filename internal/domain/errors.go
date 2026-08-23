package domain

import "errors"

var (
	// ErrInvalidInput dikembalikan saat field wajib pada request kosong.
	ErrInvalidInput = errors.New("invalid input")

	// ErrEmailAlreadyExists dikembalikan saat email sudah terdaftar --
	// baik di tabel users maupun di Keycloak.
	ErrEmailAlreadyExists = errors.New("email already registered")

	// ErrInvitationNotFound dikembalikan saat token aktivasi tidak
	// ditemukan, sudah dipakai, atau sudah kedaluwarsa (US-073 AC).
	ErrInvitationNotFound = errors.New("activation token not found, used, or expired")

	// ErrInvitationAlreadyPending dikembalikan saat mengundang email yang
	// sudah punya undangan pending di workspace yang sama (S2-16/17,
	// constraint uq_invitation_pending) -- re-invite baru boleh setelah
	// undangan lama expired/accepted/cancelled.
	ErrInvitationAlreadyPending = errors.New("invitation already pending for this email in this workspace")

	// ErrUserNotFound dikembalikan saat lookup user (misal by Keycloak
	// provider_sub) tidak menemukan baris.
	ErrUserNotFound = errors.New("user not found")

	// ErrWeakPassword dikembalikan saat password tidak memenuhi syarat
	// kompleksitas (US-073 AC: 12+ karakter, huruf besar, kecil, angka,
	// karakter spesial).
	ErrWeakPassword = errors.New("password does not meet complexity requirements")

	// ErrInvalidOTP dikembalikan saat kode OTP TOTP tidak cocok (S1-07).
	ErrInvalidOTP = errors.New("invalid otp code")

	// ErrInvalidCredentials dikembalikan saat email tidak terdaftar atau
	// password salah (S1-14). Kedua kasus sengaja disamakan -- tidak
	// membocorkan apakah suatu email terdaftar di PRODO (user enumeration).
	ErrInvalidCredentials = errors.New("invalid email or password")

	// ErrAccountInactive dikembalikan saat users.is_active masih FALSE
	// (belum menyelesaikan alur aktivasi US-073) (S1-14).
	ErrAccountInactive = errors.New("account is not active")

	// ErrMFARequired dikembalikan saat Group Admin login tapi belum punya
	// MFA aktif (S1-17) -- seharusnya tidak pernah terjadi lewat alur
	// onboarding normal (S1-06/07 mewajibkan setup MFA sebelum is_active
	// TRUE), jadi ini murni pengaman untuk state yang tidak konsisten.
	ErrMFARequired = errors.New("mfa is required for this account but not configured")

	// ErrSessionNotFound dikembalikan saat jti tidak ditemukan di
	// user_sessions, atau ditemukan tapi milik user lain (S1-33) --
	// disamakan supaya tidak membocorkan keberadaan sesi user lain.
	ErrSessionNotFound = errors.New("session not found")

	// ErrForbidden dikembalikan service layer saat actor tidak berhak atas
	// resource target (mis. Group Admin bukan pengelola grup organisasi
	// yang dituju, S3-02/03/04) -- beda dari middleware.RequireRole yang
	// menolak di edge berdasar klaim, ini scoping berbasis DATA yang cuma
	// bisa diketahui setelah query.
	ErrForbidden = errors.New("actor is not authorized for this resource")

	// ErrOrganizationNotFound dikembalikan saat organizations.id tidak
	// ditemukan (S3-03/04).
	ErrOrganizationNotFound = errors.New("organization not found")

	// ErrSlugAlreadyExists dikembalikan saat organizations.slug bentrok
	// UNIQUE constraint (S3-02/03).
	ErrSlugAlreadyExists = errors.New("organization slug already exists")

	// ErrOrganizationHasWorkspaces dikembalikan DELETE /organizations/:id
	// (S3-05) saat masih ada workspace aktif (archived_at IS NULL) di
	// organisasi tsb -- US-007 AC: "hanya bisa dihapus jika semua workspace
	// sudah dihapus/dipindahkan".
	ErrOrganizationHasWorkspaces = errors.New("organization still has active workspaces")

	// ErrWorkspaceNotFound dikembalikan saat workspaces.id tidak ditemukan
	// (S3-10/11/12).
	ErrWorkspaceNotFound = errors.New("workspace not found")

	// ErrWorkspaceHasProjects dikembalikan DELETE /workspaces/:id (S3-12)
	// saat masih ada project aktif (is_archived = FALSE) di workspace tsb.
	// Guard ini awalnya DEFERRED (implementation_gaps.md IG-17, tabel
	// projects belum ada) -- ditambahkan begitu forward-pull projects
	// selesai (S3 H9).
	ErrWorkspaceHasProjects = errors.New("workspace still has active projects")

	// ErrMemberNotFound dikembalikan saat target bukan member workspace
	// (S3-15) -- tidak ada baris workspace_members yang cocok.
	ErrMemberNotFound = errors.New("workspace member not found")

	// ErrProjectNotFound dikembalikan saat projects.id tidak ditemukan
	// (S3-21/22/23).
	ErrProjectNotFound = errors.New("project not found")

	// ErrProjectMemberAlreadyExists dikembalikan POST /projects/:id/members
	// (S3-21) saat user sudah jadi project member (PK (project_id, user_id)
	// bentrok) -- pakai PUT .../role (S3-22) untuk ubah role yang sudah ada.
	ErrProjectMemberAlreadyExists = errors.New("project member already exists")

	// ErrProjectMemberNotFound dikembalikan saat target bukan project
	// member (S3-22/23).
	ErrProjectMemberNotFound = errors.New("project member not found")
)
