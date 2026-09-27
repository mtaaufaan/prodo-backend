package handler

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/middleware"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/response"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
	"github.com/mtaaufaan/prodo-backend/internal/service"
)

var validProjectScopedRoles = map[string]bool{"editor": true, "approver": true, "viewer": true}

// ProjectMemberHandler -- S3-21/22/23/24/25/27, US-009b.
type ProjectMemberHandler struct {
	projects *service.ProjectMemberService
	logger   *zap.Logger
}

func NewProjectMemberHandler(projects *service.ProjectMemberService, logger *zap.Logger) *ProjectMemberHandler {
	return &ProjectMemberHandler{projects: projects, logger: logger}
}

type addProjectMemberRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

// AddMember menangani POST /projects/:id/members (S3-21). TIDAK
// digerbangi middleware role -- target scope-nya projectID, otorisasi
// penuh di ProjectMemberService.
func (h *ProjectMemberHandler) AddMember(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ProjectMemberHandler.AddMember dipanggil tanpa DBContextMiddleware -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectMemberHandler.AddMember dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	var req addProjectMemberRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	if req.UserID == "" || !validProjectScopedRoles[req.Role] {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "user_id dan role wajib diisi",
			[]response.FieldError{{Field: "role", Message: "harus salah satu dari editor, approver, viewer"}}))
	}

	if err := h.projects.AddMember(c.Context(), exec, projectID, req.UserID, req.Role, actorUserID, claims.PlatformRole); err != nil {
		return h.mapProjectMemberError(c, err, "Gagal menambahkan member project")
	}

	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{"project_id": projectID, "user_id": req.UserID, "role": req.Role}))
}

type addMembersBulkRequest struct {
	Emails []string `json:"emails"`
	Role   string   `json:"role"`
}

// AddMembersBulk menangani POST /projects/:id/members/bulk (IG-100
// susulan, desain "AW Invite Member.dc.html" actingRole='Project
// Manager'/"PM Member Project.dc.html" tombol "+ MEMBER") -- daftar email
// sekaligus, BOLEH dari luar workspace/organisasi ini. Email yang sudah
// terdaftar langsung dapat akses; yang belum dapat undangan 72 jam (lihat
// InvitationService.CreateBulkInvitations projectScopedOnly=true).
func (h *ProjectMemberHandler) AddMembersBulk(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ProjectMemberHandler.AddMembersBulk dipanggil tanpa DBContextMiddleware -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectMemberHandler.AddMembersBulk dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	var req addMembersBulkRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	if len(req.Emails) == 0 || !validProjectScopedRoles[req.Role] {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "emails dan role wajib diisi",
			[]response.FieldError{{Field: "role", Message: "harus salah satu dari editor, approver, viewer"}}))
	}

	result, err := h.projects.AddMembersBulk(c.Context(), exec, projectID, req.Emails, req.Role, actorUserID, claims.PlatformRole)
	if err != nil {
		return h.mapProjectMemberError(c, err, "Gagal menambahkan member project")
	}

	invitationIDs := make([]string, len(result.Created))
	for i, inv := range result.Created {
		invitationIDs[i] = inv.ID
	}
	return c.Status(fiber.StatusCreated).JSON(response.Success(fiber.Map{
		"invitation_ids": invitationIDs,
		"added_directly": result.AddedDirectly,
		"errors":         result.Errors,
	}))
}

// ListCandidates menangani GET /projects/:id/member-candidates (IG-100
// susulan, "candidate pool" modal Tambah Member Project) -- akun lintas
// SELURUH organisasi yang belum jadi member workspace project ini dan
// tidak sedang punya undangan pending.
func (h *ProjectMemberHandler) ListCandidates(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ProjectMemberHandler.ListCandidates dipanggil tanpa DBContextMiddleware -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectMemberHandler.ListCandidates dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	accounts, err := h.projects.ListCandidates(c.Context(), exec, projectID, actorUserID, claims.PlatformRole)
	if err != nil {
		return h.mapProjectMemberError(c, err, "Gagal mengambil daftar kandidat member")
	}

	data := make([]fiber.Map, len(accounts))
	for i, a := range accounts {
		data[i] = fiber.Map{
			"user_id":      a.UserID,
			"email":        a.Email,
			"display_name": a.DisplayName,
			"org_id":       a.OrgID,
			"org_name":     a.OrgName,
		}
	}
	return c.Status(fiber.StatusOK).JSON(response.Success(data))
}

type updateProjectMemberRoleRequest struct {
	Role string `json:"role"`
}

// UpdateMemberRole menangani PUT /projects/:id/members/:userId/role (S3-22).
func (h *ProjectMemberHandler) UpdateMemberRole(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ProjectMemberHandler.UpdateMemberRole dipanggil tanpa DBContextMiddleware -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectMemberHandler.UpdateMemberRole dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")
	targetUserID := c.Params("userId")

	var req updateProjectMemberRoleRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("INVALID_REQUEST", "Body request tidak valid", nil))
	}
	if !validProjectScopedRoles[req.Role] {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("VALIDATION_ERROR", "role tidak valid",
			[]response.FieldError{{Field: "role", Message: "harus salah satu dari editor, approver, viewer"}}))
	}

	if err := h.projects.UpdateMemberRole(c.Context(), exec, projectID, targetUserID, req.Role, actorUserID, claims.PlatformRole); err != nil {
		return h.mapProjectMemberError(c, err, "Gagal mengubah role project member")
	}

	return c.JSON(response.Success(fiber.Map{"project_id": projectID, "user_id": targetUserID, "role": req.Role}))
}

// RemoveMember menangani DELETE /projects/:id/members/:userId (S3-23).
func (h *ProjectMemberHandler) RemoveMember(c *fiber.Ctx) error {
	actorUserID, _, ok := middleware.ActorFromContext(c)
	if !ok {
		h.logger.Error("ProjectMemberHandler.RemoveMember dipanggil tanpa DBContextMiddleware -- actor belum diresolve")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal mengidentifikasi user", nil))
	}
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(response.Error("INVALID_CREDENTIALS", "Token tidak ditemukan", nil))
	}
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectMemberHandler.RemoveMember dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")
	targetUserID := c.Params("userId")

	if err := h.projects.RemoveMember(c.Context(), exec, projectID, targetUserID, actorUserID, claims.PlatformRole); err != nil {
		return h.mapProjectMemberError(c, err, "Gagal mengeluarkan project member")
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// ListMembers menangani GET /projects/:id/members (S3-24 prasyarat FE).
// Dua query param opsional (mutually exclusive, `view` menang kalau
// keduanya dikirim):
//   - ?assignable=true (susulan S5): PM penanggung jawab project ikut
//     sertakan sebagai entri sintetis -- dipakai picker assignee/PIC
//     (AddTaskModal/TaskDetailModal/KanbanBoard).
//   - ?view=manage (IG-100 susulan, dikonfirmasi user "yang dikecualikan
//     itu admin group dan executive... AW, DV, PM, Editor, Approver, dan
//     Viewer yang ditampilkan hanya yang berhubungan dengan project"):
//     assignable + SEMUA Admin Workspace/Division Viewer di workspace
//     pemilik project ini -- dipakai KHUSUS halaman kelola member
//     ProjectMembersPage (lihat ProjectMemberRepository.ListMembersView).
//
// FE mengunci baris non-project-scoped (PM/AW/DV) di kedua mode lewat
// `is_pm`/role check, backend tetap menolak UpdateRole/RemoveMember
// terhadapnya sebagai defense in depth (baris itu tidak punya row
// project_members asli).
func (h *ProjectMemberHandler) ListMembers(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectMemberHandler.ListMembers dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	projectID := c.Params("id")

	var members []repository.ProjectMember
	var err error
	switch {
	case c.Query("view") == "manage":
		members, err = h.projects.ListMembersView(c.Context(), exec, projectID)
	case c.Query("assignable") == "true":
		members, err = h.projects.ListAssignableMembers(c.Context(), exec, projectID)
	default:
		members, err = h.projects.ListMembers(c.Context(), exec, projectID)
	}
	if err != nil {
		return h.mapProjectMemberError(c, err, "Gagal mengambil daftar project member")
	}

	data := make([]fiber.Map, len(members))
	for i := range members {
		m := &members[i]
		data[i] = fiber.Map{
			"user_id":        m.UserID,
			"email":          m.Email,
			"display_name":   m.Name,
			"role":           m.Role,
			"is_scoped":      m.IsScoped,
			"added_at":       m.AddedAt,
			"is_pm":          m.IsPM,
			"workspace_role": m.WorkspaceRole,
			"is_pending":     m.IsPending,
		}
	}
	return c.JSON(response.Success(data))
}

// ListCrossOrgMemberships menangani GET /groups/:groupId/cross-org-memberships
// (S3-25/27) -- digerbangi requireOrgAdmin (platform_admin/group_admin) di
// routing, GA sudah punya visibility penuh lewat RLS.
func (h *ProjectMemberHandler) ListCrossOrgMemberships(c *fiber.Ctx) error {
	exec, ok := middleware.DBTxFromContext(c)
	if !ok {
		h.logger.Error("ProjectMemberHandler.ListCrossOrgMemberships dipanggil tanpa DBContextMiddleware -- tidak ada transaksi RLS")
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", "Gagal menyiapkan koneksi database", nil))
	}
	groupID := c.Params("groupId")
	orgFilter := strings.TrimSpace(c.Query("org_id"))

	list, err := h.projects.ListCrossOrgMemberships(c.Context(), exec, groupID, orgFilter)
	if err != nil {
		return h.mapProjectMemberError(c, err, "Gagal mengambil daftar keanggotaan lintas organisasi")
	}

	data := make([]fiber.Map, len(list))
	for i := range list {
		m := &list[i]
		data[i] = fiber.Map{
			"user_id":      m.UserID,
			"email":        m.Email,
			"display_name": m.DisplayName,
			"role":         m.Role,
			"org_id":       m.OrgID,
			"org_name":     m.OrgName,
			"project_id":   m.ProjectID,
			"project_name": m.ProjectName,
		}
	}
	return c.JSON(response.Success(data))
}

func (h *ProjectMemberHandler) mapProjectMemberError(c *fiber.Ctx, err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return c.Status(fiber.StatusBadRequest).JSON(response.Error("VALIDATION_ERROR", "Input tidak valid", nil))
	case errors.Is(err, domain.ErrForbidden):
		return c.Status(fiber.StatusForbidden).JSON(response.Error("FORBIDDEN", "Anda tidak berwenang atas project ini.", nil))
	case errors.Is(err, domain.ErrProjectNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Project tidak ditemukan", nil))
	case errors.Is(err, domain.ErrProjectMemberAlreadyExists):
		return c.Status(fiber.StatusConflict).JSON(response.Error("PROJECT_MEMBER_ALREADY_EXISTS", "User sudah jadi member project ini", nil))
	case errors.Is(err, domain.ErrProjectMemberNotFound):
		return c.Status(fiber.StatusNotFound).JSON(response.Error("NOT_FOUND", "Project member tidak ditemukan", nil))
	case errors.Is(err, domain.ErrProjectAwaitingPM):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response.Error("PROJECT_AWAITING_PM", "Project ini masih menunggu Project Manager menerima undangan sebelum bisa menambah member lain", nil))
	default:
		h.logger.Error(fallbackMessage, zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(response.Error("INTERNAL_ERROR", fallbackMessage, nil))
	}
}
