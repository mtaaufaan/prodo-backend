package domain

import (
	"errors"
	"fmt"
)

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

	// ErrAccountSuspended dikembalikan saat users.suspended_at TERISI
	// (S4P-02, US-067) -- SENGAJA kolom terpisah dari is_active: akun yang
	// baru diundang (belum pernah aktif) dan akun yang disuspend (pernah
	// aktif, sengaja dinonaktifkan PA) butuh pesan berbeda ("selesaikan
	// aktivasi" vs "hubungi Platform Admin"), dan reaktivasi tidak boleh
	// memaksa GA mengulang alur invite+aktivasi dari nol.
	ErrAccountSuspended = errors.New("account has been suspended")

	// ErrMFARequired dikembalikan saat Group Admin login tapi belum punya
	// MFA aktif (S1-17) -- seharusnya tidak pernah terjadi lewat alur
	// onboarding normal (S1-06/07 mewajibkan setup MFA sebelum is_active
	// TRUE), jadi ini murni pengaman untuk state yang tidak konsisten.
	ErrMFARequired = errors.New("mfa is required for this account but not configured")

	// ErrMFASetupRequired dikembalikan saat Platform Admin login tapi belum
	// punya MFA aktif (S4P-14, implementation_gaps.md IG-20) -- BEDA dari
	// ErrMFARequired: ini alur yang DIHARAPKAN terjadi (akun PA tidak
	// melalui onboarding invite+aktivasi seperti Group Admin/US-073, jadi
	// tidak ada langkah wajib setup MFA sebelumnya). Caller (AuthService.
	// Login) menerbitkan tantangan setup MFA saat error ini muncul, bukan
	// menolak login begitu saja.
	ErrMFASetupRequired = errors.New("mfa setup is required for this platform admin account")

	// ErrIPNotAllowed dikembalikan saat Platform Admin login dari IP di
	// luar allowlist yang dia konfigurasi sendiri (S4P-17, opsional --
	// hanya berlaku kalau akun tersebut PUNYA baris allowlist sama sekali).
	ErrIPNotAllowed = errors.New("login is not allowed from this ip address")

	// ErrSessionNotFound dikembalikan saat jti tidak ditemukan di
	// user_sessions, atau ditemukan tapi milik user lain (S1-33) --
	// disamakan supaya tidak membocorkan keberadaan sesi user lain.
	ErrSessionNotFound = errors.New("session not found")

	// ErrSessionExpired dikembalikan POST /auth/refresh (ditambahkan
	// 2026-08-29) saat sesi lama (jti) sudah di-revoke atau sudah lewat
	// idle-timeout-nya sendiri (sliding 30 menit / fixed per-akun PA) --
	// refresh ditolak, klien harus login ulang.
	ErrSessionExpired = errors.New("session already expired or revoked")

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

	// ErrStorageQuotaExceedsMax dikembalikan PUT /organizations/:id/storage-quota
	// (S3-34) saat kuota yang diminta melebihi storage_max_bytes (batas
	// dari Platform Admin).
	ErrStorageQuotaExceedsMax = errors.New("storage quota exceeds max allowed")

	// ErrStorageQuotaBelowUsed dikembalikan PUT /organizations/:id/storage-quota
	// (S4G-02, Track S4G) saat kuota yang diminta lebih kecil dari storage
	// yang sudah terpakai organisasi ini.
	ErrStorageQuotaBelowUsed = errors.New("storage quota below currently used storage")

	// ErrGroupStorageQuotaExceedsCeiling dikembalikan PUT
	// /organizations/:id/storage-quota (S4P-12) saat total kuota seluruh
	// organisasi dalam satu grup (termasuk perubahan yang diminta) akan
	// melebihi plafon groups.storage_quota_gb milik grup itu -- menegakkan
	// plafon yang sebelumnya cuma disimpan/ditampilkan (S4P-06/07).
	ErrGroupStorageQuotaExceedsCeiling = errors.New("group storage quota ceiling exceeded")

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

	// ErrProjectCodeTaken dikembalikan POST/PUT project (S4-02) saat kode
	// task (prefiks nomor task) sudah dipakai project lain DI WORKSPACE
	// YANG SAMA -- unik per workspace, bukan global (AW Add Project.dc.html).
	ErrProjectCodeTaken = errors.New("project code already used in this workspace")

	// ErrProjectNotDeleted dikembalikan POST /projects/:id/restore (S4-02)
	// saat project target tidak sedang dalam status soft-deleted.
	ErrProjectNotDeleted = errors.New("project is not soft-deleted")

	// ErrInvalidCIDR dikembalikan saat input allowlist IP Platform Admin
	// (S4P-18) bukan notasi CIDR yang valid.
	ErrInvalidCIDR = errors.New("invalid cidr notation")

	// ErrIPAllowlistEntryNotFound dikembalikan saat entry allowlist yang
	// mau dihapus tidak ada, atau milik akun Platform Admin lain (S4P-18) --
	// disamakan supaya satu PA tidak bisa menebak ID entry milik PA lain.
	ErrIPAllowlistEntryNotFound = errors.New("ip allowlist entry not found")

	// ErrSessionTimeoutTooShort dikembalikan saat Platform Admin mencoba
	// set session timeout di bawah batas minimum 10 menit (US-070 AC,
	// S4P-18).
	ErrSessionTimeoutTooShort = errors.New("session timeout below minimum allowed")

	// ErrInvalidTransferTarget dikembalikan saat target transfer grup
	// (S4P-03/04) bukan akun Group Admin yang valid.
	ErrInvalidTransferTarget = errors.New("transfer target is not a valid group admin")

	// ErrGroupTransferRequired dikembalikan saat mencoba menghapus akun
	// Group Admin (S4P-05) yang masih mengelola minimal satu grup --
	// transfer (S4P-03/04) dulu sebelum bisa dihapus, supaya organisasi di
	// grup itu tidak kehilangan pengelola.
	ErrGroupTransferRequired = errors.New("group must be transferred before this account can be deleted")

	// ErrInvalidStatusTransition dikembalikan saat form Ubah Group Admin
	// (S4P-06) mencoba set status ke nilai selain "AKTIF"/"SUSPENDED" --
	// "TIDAK AKTIF" (pending, belum aktivasi) cuma bisa dicapai lewat
	// alur onboarding yang belum selesai, tidak bisa diset manual mundur.
	ErrInvalidStatusTransition = errors.New("invalid group admin status transition")

	// ErrInvalidTier dikembalikan saat tier_id yang diminta (S4P-06/07)
	// tidak ditemukan, atau nonaktif/archived (S4P-11 -- tier nonaktif/
	// archived tidak bisa di-assign ke GA baru).
	ErrInvalidTier = errors.New("invalid service tier")

	// ErrTierNameAlreadyExists dikembalikan POST/PUT /platform/tiers
	// (S4P-11) saat nama tier bentrok UNIQUE constraint dengan tier lain.
	ErrTierNameAlreadyExists = errors.New("service tier name already exists")

	// ErrInvalidSubscriptionPeriod dikembalikan saat subscription_period
	// kontrak grup (dikonfirmasi user 2026-08-29) bukan salah satu dari
	// "monthly"/"quarterly"/"yearly".
	ErrInvalidSubscriptionPeriod = errors.New("invalid subscription period")

	// ErrTierNotFound dikembalikan saat tier_id tidak ditemukan di
	// service_tiers (S4P-11).
	ErrTierNotFound = errors.New("service tier not found")

	// ErrTierInUse dikembalikan DELETE /platform/tiers/:id (S4P-11) saat
	// masih ada grup yang mereferensikan tier ini -- harus di-archive dan
	// menunggu seluruh GA pindah tier dulu sebelum bisa dihapus permanen.
	ErrTierInUse = errors.New("service tier still in use by one or more groups")

	// ErrTierNotDeletable dikembalikan DELETE /platform/tiers/:id (S4P-11)
	// untuk tier standar (starter/business/enterprise, is_custom=false) --
	// tier standar bisa dinonaktifkan/archived tapi tidak pernah bisa
	// dihapus permanen, beda dari tier custom yang PA tambahkan sendiri.
	ErrTierNotDeletable = errors.New("standard service tier cannot be deleted")

	// ErrErasureRequestNotFound dikembalikan saat erasure_requests.id yang
	// dituju (execute/reject, S4P-31) tidak ditemukan.
	ErrErasureRequestNotFound = errors.New("erasure request not found")

	// ErrErasureRequestAlreadyProcessed dikembalikan saat execute/reject
	// (S4P-31) dipanggil pada request yang statusnya sudah DONE/REJECTED --
	// tidak boleh diproses dua kali.
	ErrErasureRequestAlreadyProcessed = errors.New("erasure request already processed")

	// ErrErasureConfirmationRequired dikembalikan POST .../execute (S4P-31)
	// saat body confirmation tidak persis "KONFIRMASI" -- konfirmasi dua
	// langkah untuk aksi pseudonymization yang irreversible (AC sprint_backlog
	// S4P-31; desain "PA Erasure Confirm" cuma modal 1 klik, lihat
	// implementation_gaps.md untuk gap ini).
	ErrErasureConfirmationRequired = errors.New("typed confirmation required to execute erasure")

	// ErrCannotDeactivateSelf dikembalikan PUT /platform/admins/:id/deactivate
	// (S4P-38) saat target = actor sendiri -- Platform Admin tidak boleh
	// menonaktifkan akunnya sendiri (akan langsung terkunci dari sesi
	// berikutnya tanpa cara memulihkan lewat aplikasi).
	ErrCannotDeactivateSelf = errors.New("platform admin cannot deactivate their own account")

	// ErrMinimumActiveAdminRequired dikembalikan saat deactivate (S4P-38)
	// akan menyisakan 0 Platform Admin aktif -- selalu harus ada minimal
	// satu akun PA aktif supaya platform tidak pernah kehilangan admin.
	ErrMinimumActiveAdminRequired = errors.New("at least one active platform admin must remain")

	// ErrCannotResetOwnMFA dikembalikan POST /platform/admins/:id/reset-mfa
	// (S4P-39) saat target = actor sendiri -- mereset MFA sendiri akan
	// mengunci actor dari MFA yang sedang dia pakai untuk sesi ini juga.
	ErrCannotResetOwnMFA = errors.New("platform admin cannot reset their own mfa")

	// ErrPlatformAdminNotFound dikembalikan saat target
	// deactivate/reactivate/reset-mfa (S4P-38/39) bukan akun platform_admin
	// yang valid.
	ErrPlatformAdminNotFound = errors.New("platform admin not found")
)

// StorageQuotaBelowUsageError dikembalikan PUT /platform/group-admins/:id
// (IG-23) saat storage_quota_gb yang diminta lebih kecil dari pemakaian
// grup saat ini -- meniru aturan plafonError() di desain "PA Group Admin
// Form" yang sebelumnya tidak ditegakkan sama sekali (FE cuma cek > 0, BE
// cuma cek field wajib tidak kosong). Butuh tipe error sendiri (bukan
// sentinel biasa) karena pesannya perlu menyertakan angka pemakaian
// aktual, bukan cuma pesan statis.
type StorageQuotaBelowUsageError struct {
	MinimumGB int
}

func (e *StorageQuotaBelowUsageError) Error() string {
	return fmt.Sprintf("storage quota must be at least %d GB (current usage)", e.MinimumGB)
}
