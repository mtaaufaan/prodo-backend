package domain

import "time"

// PlatformRole adalah role level platform (bukan role workspace) -- lihat
// docs/DATABASE_SCHEMA.md §5.1 kolom users.platform_role.
type PlatformRole string

const (
	PlatformRoleAdmin      PlatformRole = "platform_admin"
	PlatformRoleGroupAdmin PlatformRole = "group_admin"
	PlatformRoleMember     PlatformRole = "member"
)

// User merepresentasikan baris tabel users. Kredensial (password, MFA
// secret) tidak ada di sini -- dikelola sepenuhnya oleh Keycloak, lihat
// user_auth_providers (§5.2) dan keputusan Keycloak-delegated di
// docs/s1-kickoff.html.
type User struct {
	ID           string
	Email        string
	DisplayName  string
	PlatformRole PlatformRole
	IsActive     bool
	CreatedAt    time.Time
}
