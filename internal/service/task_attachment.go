// Package service -- TaskAttachmentService (H20-22, S4W-19/20/21, EPIC 10
// Attachment Management, US-064/064b/065/066, desain "Task Detail.dc.html"
// tab Attachments + "AW Documents.dc.html"). Cakupan HANYA attachment di
// deskripsi task -- attachment di komentar (bagian lain US-064) dan
// integrasi Version History (US-064c) di luar cakupan, dependency-nya
// (`task_comments`/version history task) belum ada sama sekali.
package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

const maxAttachmentSizeBytes = 50 * 1024 * 1024

// allowedAttachmentExt/blockedAttachmentExt -- persis daftar US-064 AC
// ("Task Detail.dc.html" attachment tab menyalin daftar yang sama).
var allowedAttachmentExt = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true, "svg": true,
	"pdf": true, "doc": true, "docx": true, "xls": true, "xlsx": true, "ppt": true, "pptx": true, "txt": true, "md": true, "csv": true,
	"zip": true, "rar": true, "7z": true,
	"json": true, "xml": true,
}
var blockedAttachmentExt = map[string]bool{"exe": true, "bat": true, "sh": true, "cmd": true, "msi": true, "apk": true, "dmg": true}

// executableMagicPrefixes -- signature biner umum yang menandai file ini
// SEBENARNYA executable meski ekstensinya di-rename ke whitelist (mis.
// virus.exe -> laporan.pdf). Dicek MANUAL, bukan lewat
// http.DetectContentType -- sniffer stdlib itu TIDAK PUNYA signature untuk
// format executable sama sekali (dikonfirmasi lewat percobaan langsung:
// header MZ dikembalikan sebagai "application/octet-stream" generik, ELF
// murni malah kebaca "text/plain"), jadi pendekatan awal (cek substring
// "x-msdownload"/"x-elf"/dst pada hasil DetectContentType) TIDAK PERNAH
// bisa menolak apa pun -- baru ketahuan lewat test unit, bukan observasi
// manual. MZ (PE/DOS Windows), \x7fELF (Linux), Mach-O 4 varian byte-order
// (macOS) -- ponytail: bukan daftar signature pihak ketiga yang lengkap,
// upgrade kalau ada format berbahaya lain yang lolos di produksi.
var executableMagicPrefixes = [][]byte{
	{0x4D, 0x5A},                   // MZ -- PE/DOS
	{0x7F, 'E', 'L', 'F'},          // ELF
	{0xFE, 0xED, 0xFA, 0xCE},       // Mach-O 32-bit BE
	{0xFE, 0xED, 0xFA, 0xCF},       // Mach-O 64-bit BE
	{0xCE, 0xFA, 0xED, 0xFE},       // Mach-O 32-bit LE
	{0xCF, 0xFA, 0xED, 0xFE},       // Mach-O 64-bit LE
}

// validateAttachmentFile -- US-064 AC "validasi MIME type di sisi server,
// bukan hanya ekstensi": whitelist/blacklist ekstensi DULU, lalu magic-byte
// check langsung terhadap executableMagicPrefixes (lihat komentarnya) --
// BUKAN crosswalk lengkap ekstensi->MIME per tipe (di luar cakupan
// realistis tanpa daftar signature pihak ketiga). http.DetectContentType
// tetap dipakai untuk isImage/mimeType yang ditampilkan FE (akurat untuk
// gambar), TAPI TIDAK dipakai lagi sebagai dasar penolakan.
func validateAttachmentFile(filename string, data []byte) (mimeType string, isImage bool, err error) {
	ext := fileExt(filename)
	if blockedAttachmentExt[ext] {
		return "", false, domain.ErrAttachmentTypeNotAllowed
	}
	if !allowedAttachmentExt[ext] {
		return "", false, domain.ErrAttachmentTypeNotAllowed
	}
	for _, magic := range executableMagicPrefixes {
		if bytes.HasPrefix(data, magic) {
			return "", false, domain.ErrAttachmentTypeNotAllowed
		}
	}
	sniffLen := 512
	if len(data) < sniffLen {
		sniffLen = len(data)
	}
	mimeType = http.DetectContentType(data[:sniffLen])
	return mimeType, strings.HasPrefix(mimeType, "image/"), nil
}

func fileExt(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx < 0 || idx == len(name)-1 {
		return ""
	}
	return strings.ToLower(name[idx+1:])
}

// attachmentRepository -- interface didefinisikan di consumer, §3.9.
type attachmentRepository interface {
	Create(ctx context.Context, exec db.Executor, taskID, uploaderID, originalName, displayName, storageKey, mimeType string, sizeBytes int64, isImage bool, actorRole, workspaceID string) (*repository.TaskAttachment, error)
	Get(ctx context.Context, exec db.Executor, id string) (*repository.TaskAttachment, error)
	ListForTask(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskAttachment, error)
	ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string, f *repository.AttachmentFilter) ([]repository.TaskAttachment, int, error)
	Rename(ctx context.Context, exec db.Executor, id, displayName, actorID, actorRole, workspaceID string, before *repository.TaskAttachment) error
	SoftDelete(ctx context.Context, exec db.Executor, id string, purgeAt time.Time, actorID, actorRole, workspaceID string, before *repository.TaskAttachment) error
	PermanentDelete(ctx context.Context, exec db.Executor, id, actorID, actorRole, workspaceID string, before *repository.TaskAttachment) error
	Restore(ctx context.Context, exec db.Executor, id, actorID, actorRole, workspaceID string, before *repository.TaskAttachment) error
	OrgUsageBytes(ctx context.Context, exec db.Executor, orgID string) (int64, error)
	PerProjectUsage(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.ProjectUsage, error)
	ListGroupAdmins(ctx context.Context, exec db.Executor, groupID string) ([]repository.GroupAdminContact, error)
	LogQuotaRequest(ctx context.Context, exec db.Executor, actorID, actorRole, workspaceID, orgID string, additionalGB int, reason string) error
}

// attachmentTaskResolver -- reuse TaskRepository.GetProjectID.
type attachmentTaskResolver interface {
	GetProjectID(ctx context.Context, exec db.Executor, taskID string) (string, error)
}

// attachmentProjectResolver -- reuse ProjectRepository.GetWorkspaceID.
// Interface SENDIRI (bukan pakai taskProjectResolver dari task.go) --
// tidak butuh GetAllowEditorStoryPoints, duplikasi kecil disengaja, pola
// sama service lain di package ini yang masing-masing punya interface
// minimal sendiri.
type attachmentProjectResolver interface {
	GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error)
}

// attachmentOrgQuota -- reuse OrganizationRepository.GetAttachmentQuotaInfo
// (batas kuota + retention_days -- HANYA field yang aman dibaca lewat RLS
// SELECT dari transaksi Admin Workspace biasa).
type attachmentOrgQuota interface {
	GetAttachmentQuotaInfo(ctx context.Context, exec db.Executor, workspaceID string) (*repository.AttachmentQuotaInfo, error)
}

// attachmentQuotaRefresher -- antre job Asynq TRUSTED (bypass RLS) yang
// menulis ULANG `organizations.storage_used_mb` (lihat komentar package
// worker/refresh_org_storage.go kenapa ini WAJIB lewat job, bukan tulis
// langsung dari transaksi request). Best-effort, TIDAK PERNAH menggagalkan
// upload/hapus yang sudah berhasil -- error di-log, bukan dilempar balik.
type attachmentQuotaRefresher interface {
	Enqueue(ctx context.Context, orgID string) error
}

// attachmentWorkspaceInfo -- reuse WorkspaceRepository.Get (dipakai
// konfirmasi ketik-ulang nama workspace saat hapus permanen, dan notice
// "Minta Tambah Kuota").
type attachmentWorkspaceInfo interface {
	Get(ctx context.Context, exec db.Executor, workspaceID string) (*repository.Workspace, error)
}

// attachmentStorage -- reuse StorageService (MinIO).
type attachmentStorage interface {
	Upload(ctx context.Context, key string, data []byte, contentType string) error
	Download(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

type TaskAttachmentService struct {
	repo         attachmentRepository
	tasks        attachmentTaskResolver
	projects     attachmentProjectResolver
	workspaces   attachmentWorkspaceInfo
	orgs         attachmentOrgQuota
	rbac         sprintWorkspaceRoleChecker
	projectRoles sprintProjectRoleChecker
	storage      attachmentStorage
	refresher    attachmentQuotaRefresher
}

func NewTaskAttachmentService(repo attachmentRepository, tasks attachmentTaskResolver, projects attachmentProjectResolver, workspaces attachmentWorkspaceInfo, orgs attachmentOrgQuota, rbac sprintWorkspaceRoleChecker, projectRoles sprintProjectRoleChecker, storage attachmentStorage, refresher attachmentQuotaRefresher) *TaskAttachmentService {
	return &TaskAttachmentService{repo: repo, tasks: tasks, projects: projects, workspaces: workspaces, orgs: orgs, rbac: rbac, projectRoles: projectRoles, storage: storage, refresher: refresher}
}

// resolveTaskContext -- workspaceID+role aktor relatif task ini. role ""
// berarti Full mode (PA/GA, atau AW/PM lewat workspace_role).
func (s *TaskAttachmentService) resolveTaskContext(ctx context.Context, exec db.Executor, taskID, actorID, actorRole string) (workspaceID, role string, err error) {
	projectID, err := s.tasks.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return "", "", fmt.Errorf("service.resolveTaskContext: %w", err)
	}
	workspaceID, err = s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return "", "", fmt.Errorf("service.resolveTaskContext: %w", err)
	}
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return workspaceID, "", nil
	}
	if r, found, rerr := s.projectRoles.GetRole(ctx, exec, projectID, actorID); rerr == nil && found {
		return workspaceID, r, nil
	}
	role, err = s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return "", "", fmt.Errorf("service.resolveTaskContext: %w", err)
	}
	return workspaceID, role, nil
}

// canEdit -- US-064 persona: AW/PM/Editor/Approver boleh upload, Viewer
// tidak. Download/list TIDAK digerbangi method ini -- RLS `task_attachments`
// sudah cukup (siapa pun dengan akses task boleh lihat+unduh, US-064b AC).
func canEditAttachment(role string) bool {
	return role != "viewer" && role != "division_viewer"
}

// canManageAttachment -- US-064b: rename/hapus/restore hanya pengunggah,
// PM, atau AW (role "" = PA/GA, selalu Full).
func canManageAttachment(role string, isUploader bool) bool {
	return isUploader || role == "" || role == "admin_workspace" || role == "project_manager"
}

// Upload -- POST /tasks/:id/attachments. Urutan validasi PERSIS AC
// US-064/066: tipe file -> ukuran -> kuota (SEBELUM transfer ke MinIO,
// "menghindari penggunaan bandwidth yang sia-sia").
func (s *TaskAttachmentService) Upload(ctx context.Context, exec db.Executor, taskID, filename string, data []byte, actorID, actorRole string) (*repository.TaskAttachment, error) {
	if taskID == "" || filename == "" {
		return nil, fmt.Errorf("service.Upload: %w", domain.ErrInvalidInput)
	}
	if int64(len(data)) > maxAttachmentSizeBytes {
		return nil, fmt.Errorf("service.Upload: %w", domain.ErrAttachmentTooLarge)
	}
	mimeType, isImage, err := validateAttachmentFile(filename, data)
	if err != nil {
		return nil, err
	}

	workspaceID, role, err := s.resolveTaskContext(ctx, exec, taskID, actorID, actorRole)
	if err != nil {
		return nil, err
	}
	if !canEditAttachment(role) {
		return nil, fmt.Errorf("service.Upload: %w", domain.ErrForbidden)
	}

	quota, err := s.orgs.GetAttachmentQuotaInfo(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.Upload: %w", err)
	}
	// Gate kuota keras PAKAI ANGKA LIVE (SUM langsung dari task_attachments,
	// SELECT biasa yang lolos RLS untuk anggota workspace mana pun) --
	// BUKAN quota.UsedBytes (organizations.storage_used_mb) yang cuma
	// diperbarui ASYNC lewat job (lihat attachmentQuotaRefresher). Kalau
	// gate ini pakai kolom yang bisa basi, upload bisa lolos padahal kuota
	// sudah penuh -- fatal untuk AC US-066 "dicek SEBELUM file mulai
	// ditransfer".
	liveUsed, err := s.repo.OrgUsageBytes(ctx, exec, quota.OrgID)
	if err != nil {
		return nil, fmt.Errorf("service.Upload: %w", err)
	}
	if quota.QuotaBytes > 0 && liveUsed+int64(len(data)) > quota.QuotaBytes {
		return nil, fmt.Errorf("service.Upload: %w", domain.ErrStorageQuotaFull)
	}

	storageKey := fmt.Sprintf("orgs/%s/tasks/%s/%s-%s", quota.OrgID, taskID, uuid.NewString(), filename)
	if err := s.storage.Upload(ctx, storageKey, data, mimeType); err != nil {
		return nil, fmt.Errorf("service.Upload: %w", err)
	}

	att, err := s.repo.Create(ctx, exec, taskID, actorID, filename, filename, storageKey, mimeType, int64(len(data)), isImage, actorRole, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.Upload: %w", err)
	}
	// ponytail: enqueue TERJADI SEBELUM transaksi request ini commit --
	// worker bisa saja mulai hitung ulang sepersekian detik lebih awal
	// dari baris attachment ini benar-benar terlihat committed, membuat
	// hasil refresh itu meleset satu file. Dampaknya kecil dan self-
	// correcting (upload/hapus BERIKUTNYA memicu recompute lagi) --
	// upgrade ke pola transactional outbox kalau staleness ini pernah
	// benar-benar jadi masalah nyata.
	s.refreshQuota(ctx, quota.OrgID)
	return att, nil
}

// refreshQuota -- antre job Asynq (lihat attachmentQuotaRefresher),
// best-effort, tidak pernah menggagalkan aksi utama (upload/hapus
// permanen) yang sudah berhasil.
func (s *TaskAttachmentService) refreshQuota(ctx context.Context, orgID string) {
	if s.refresher == nil {
		return
	}
	_ = s.refresher.Enqueue(ctx, orgID)
}

// ListForTask -- GET /tasks/:id/attachments, tab Lampiran. Tanpa
// authorize tambahan -- RLS task_attachments + RLS tasks (task itu sendiri
// harus lolos dulu untuk di-load FE) sudah cukup, pola sama TaskService.Get.
func (s *TaskAttachmentService) ListForTask(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskAttachment, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.ListForTask: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.ListForTask(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.ListForTask: %w", err)
	}
	return list, nil
}

// Download -- GET /attachments/:id/download. Tanpa authorize tambahan,
// sama alasan ListForTask (RLS sudah menjadi gerbang akses).
func (s *TaskAttachmentService) Download(ctx context.Context, exec db.Executor, id string) (*repository.TaskAttachment, []byte, error) {
	att, err := s.repo.Get(ctx, exec, id)
	if err != nil {
		return nil, nil, fmt.Errorf("service.Download: %w", err)
	}
	data, err := s.storage.Download(ctx, att.StorageKey)
	if err != nil {
		return nil, nil, fmt.Errorf("service.Download: %w", err)
	}
	return att, data, nil
}

// Rename -- PUT /attachments/:id.
func (s *TaskAttachmentService) Rename(ctx context.Context, exec db.Executor, id, displayName, actorID, actorRole string) error {
	displayName = strings.TrimSpace(displayName)
	if id == "" || displayName == "" {
		return fmt.Errorf("service.Rename: %w", domain.ErrInvalidInput)
	}
	before, err := s.repo.Get(ctx, exec, id)
	if err != nil {
		return err
	}
	workspaceID, role, err := s.resolveTaskContext(ctx, exec, before.TaskID, actorID, actorRole)
	if err != nil {
		return err
	}
	if !canManageAttachment(role, before.UploaderID == actorID) {
		return fmt.Errorf("service.Rename: %w", domain.ErrForbidden)
	}
	if err := s.repo.Rename(ctx, exec, id, displayName, actorID, actorRole, workspaceID, before); err != nil {
		return fmt.Errorf("service.Rename: %w", err)
	}
	return nil
}

// Delete -- DELETE /attachments/:id. Mode "retensi" (default): objek
// dipertahankan, restorable selama masa retensi organisasi. TIDAK
// membebaskan kuota seketika (US-064b AC "membebaskan kuota" berlaku
// SETELAH retensi berakhir -- lihat komentar package repository soal
// belum adanya job purge fisik, gap yang sudah diterima di seluruh sistem).
func (s *TaskAttachmentService) Delete(ctx context.Context, exec db.Executor, id, actorID, actorRole string) error {
	before, err := s.repo.Get(ctx, exec, id)
	if err != nil {
		return err
	}
	workspaceID, role, err := s.resolveTaskContext(ctx, exec, before.TaskID, actorID, actorRole)
	if err != nil {
		return err
	}
	if !canManageAttachment(role, before.UploaderID == actorID) {
		return fmt.Errorf("service.Delete: %w", domain.ErrForbidden)
	}
	quota, err := s.orgs.GetAttachmentQuotaInfo(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	purgeAt := time.Now().Add(time.Duration(quota.RetentionDays) * 24 * time.Hour)
	if err := s.repo.SoftDelete(ctx, exec, id, purgeAt, actorID, actorRole, workspaceID, before); err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	return nil
}

// Restore -- POST /attachments/:id/restore.
func (s *TaskAttachmentService) Restore(ctx context.Context, exec db.Executor, id, actorID, actorRole string) error {
	before, err := s.repo.Get(ctx, exec, id)
	if err != nil {
		return err
	}
	workspaceID, role, err := s.resolveTaskContext(ctx, exec, before.TaskID, actorID, actorRole)
	if err != nil {
		return err
	}
	if !canManageAttachment(role, before.UploaderID == actorID) {
		return fmt.Errorf("service.Restore: %w", domain.ErrForbidden)
	}
	if err := s.repo.Restore(ctx, exec, id, actorID, actorRole, workspaceID, before); err != nil {
		return fmt.Errorf("service.Restore: %w", err)
	}
	return nil
}

// authorizeWorkspace -- AW-only, PERSIS pola RuleService/WebhookService
// (dipakai seluruh operasi "AW Documents.dc.html": list workspace,
// hapus permanen, bulk delete, minta tambah kuota).
func (s *TaskAttachmentService) authorizeWorkspace(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return nil
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("service.authorizeWorkspace: %w", err)
	}
	if role != "admin_workspace" {
		return fmt.Errorf("service.authorizeWorkspace: %w", domain.ErrForbidden)
	}
	return nil
}

// ListForWorkspace -- GET /workspaces/:wsId/documents, grid "AW Documents".
func (s *TaskAttachmentService) ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string, f *repository.AttachmentFilter, actorID, actorRole string) ([]repository.TaskAttachment, int, error) {
	if workspaceID == "" {
		return nil, 0, fmt.Errorf("service.ListForWorkspace: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListForWorkspace(ctx, exec, workspaceID, f)
	if err != nil {
		return nil, 0, fmt.Errorf("service.ListForWorkspace: %w", err)
	}
	return list, total, nil
}

// QuotaOverview -- kartu kuota + breakdown per-project "AW Documents.dc.html".
type QuotaOverview struct {
	QuotaBytes    int64
	UsedBytes     int64
	RetentionDays int
	PerProject    []repository.ProjectUsage
}

func (s *TaskAttachmentService) QuotaOverview(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) (*QuotaOverview, error) {
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return nil, err
	}
	quota, err := s.orgs.GetAttachmentQuotaInfo(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.QuotaOverview: %w", err)
	}
	perProject, err := s.repo.PerProjectUsage(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.QuotaOverview: %w", err)
	}
	return &QuotaOverview{QuotaBytes: quota.QuotaBytes, UsedBytes: quota.UsedBytes, RetentionDays: quota.RetentionDays, PerProject: perProject}, nil
}

// PermanentDelete -- AW-only, "AW Documents.dc.html" mode "permanen":
// wajib ketik ulang nama workspace persis (case-insensitive setelah
// trim, sama pola konfirmasi ketik di modal desain). Objek MinIO dihapus
// SEKETIKA (beda dari Delete/retensi biasa).
func (s *TaskAttachmentService) PermanentDelete(ctx context.Context, exec db.Executor, workspaceID, id, confirmWorkspaceName, actorID, actorRole string) error {
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.checkWorkspaceNameConfirm(ctx, exec, workspaceID, confirmWorkspaceName); err != nil {
		return err
	}
	return s.permanentDeleteOne(ctx, exec, workspaceID, id, actorID, actorRole)
}

func (s *TaskAttachmentService) checkWorkspaceNameConfirm(ctx context.Context, exec db.Executor, workspaceID, confirmWorkspaceName string) error {
	ws, err := s.workspaces.Get(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("service.checkWorkspaceNameConfirm: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(confirmWorkspaceName), strings.TrimSpace(ws.Name)) {
		return fmt.Errorf("service.checkWorkspaceNameConfirm: %w", domain.ErrWorkspaceNameConfirmMismatch)
	}
	return nil
}

func (s *TaskAttachmentService) permanentDeleteOne(ctx context.Context, exec db.Executor, workspaceID, id, actorID, actorRole string) error {
	before, err := s.repo.Get(ctx, exec, id)
	if err != nil {
		return err
	}
	if err := s.repo.PermanentDelete(ctx, exec, id, actorID, actorRole, workspaceID, before); err != nil {
		return fmt.Errorf("service.permanentDeleteOne: %w", err)
	}
	if err := s.storage.Delete(ctx, before.StorageKey); err != nil {
		return fmt.Errorf("service.permanentDeleteOne: hapus objek MinIO: %w", err)
	}
	quota, err := s.orgs.GetAttachmentQuotaInfo(ctx, exec, workspaceID)
	if err == nil {
		s.refreshQuota(ctx, quota.OrgID)
	}
	return nil
}

// BulkDelete -- "HAPUS TERPILIH" grid "AW Documents.dc.html". mode
// "retensi" (default) atau "permanen" (wajib confirmWorkspaceName cocok).
// Kegagalan SATU baris tidak menggagalkan baris lain -- caller (handler)
// melaporkan count sukses.
func (s *TaskAttachmentService) BulkDelete(ctx context.Context, exec db.Executor, workspaceID string, ids []string, mode, confirmWorkspaceName, actorID, actorRole string) (succeeded int, err error) {
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return 0, err
	}
	if mode == "permanen" {
		if err := s.checkWorkspaceNameConfirm(ctx, exec, workspaceID, confirmWorkspaceName); err != nil {
			return 0, err
		}
	}
	for _, id := range ids {
		if mode == "permanen" {
			if err := s.permanentDeleteOne(ctx, exec, workspaceID, id, actorID, actorRole); err == nil {
				succeeded++
			}
			continue
		}
		before, gerr := s.repo.Get(ctx, exec, id)
		if gerr != nil {
			continue
		}
		quota, qerr := s.orgs.GetAttachmentQuotaInfo(ctx, exec, workspaceID)
		if qerr != nil {
			continue
		}
		purgeAt := time.Now().Add(time.Duration(quota.RetentionDays) * 24 * time.Hour)
		if err := s.repo.SoftDelete(ctx, exec, id, purgeAt, actorID, actorRole, workspaceID, before); err == nil {
			succeeded++
		}
	}
	return succeeded, nil
}

// RequestQuota -- "MINTA TAMBAH KUOTA" (AW Documents.dc.html), versi
// MINIMAL yang dikonfirmasi user: kirim notifikasi in-app + audit ke
// SETIAP Group Admin pengelola grup organisasi ini -- TIDAK ada tabel
// request/alur approve-reject tersendiri (tidak ada mekanisme serupa di
// sistem ini sama sekali sebelum ini; membangun alur penuh di luar
// cakupan yang diminta).
func (s *TaskAttachmentService) RequestQuota(ctx context.Context, exec db.Executor, workspaceID string, additionalGB int, reason, actorID, actorRole string) error {
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return err
	}
	if additionalGB <= 0 {
		return fmt.Errorf("service.RequestQuota: %w", domain.ErrInvalidInput)
	}
	quota, err := s.orgs.GetAttachmentQuotaInfo(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("service.RequestQuota: %w", err)
	}
	ws, err := s.workspaces.Get(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("service.RequestQuota: %w", err)
	}
	admins, err := s.repo.ListGroupAdmins(ctx, exec, quota.GroupID)
	if err != nil {
		return fmt.Errorf("service.RequestQuota: %w", err)
	}
	title := fmt.Sprintf("Permintaan tambah kuota storage -- %s", quota.OrgName)
	body := fmt.Sprintf("Admin Workspace %q meminta tambahan %d GB untuk organisasi %s. Alasan: %s", ws.Name, additionalGB, quota.OrgName, reason)
	for _, ga := range admins {
		if _, execErr := exec.Exec(ctx, `
			INSERT INTO notifications (user_id, actor_id, type, entity_type, entity_id, title, body)
			VALUES ($1, $2, 'attachment_quota_requested', 'organization', $3, $4, $5)
		`, ga.ID, actorID, quota.OrgID, title, body); execErr != nil {
			continue
		}
	}
	if err := s.repo.LogQuotaRequest(ctx, exec, actorID, actorRole, workspaceID, quota.OrgID, additionalGB, reason); err != nil {
		return fmt.Errorf("service.RequestQuota: %w", err)
	}
	return nil
}
