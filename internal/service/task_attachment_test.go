package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeAttachmentRepo struct {
	byID map[string]*repository.TaskAttachment

	createErr   error
	createCalls int

	softDeleteCalls []struct {
		id      string
		purgeAt time.Time
	}
	permanentDeleteCalls []string
	restoreCalls         []string

	orgUsageBytes int64
	orgUsageErr   error

	groupAdmins []repository.GroupAdminContact

	quotaRequestLogged bool
}

func (f *fakeAttachmentRepo) Create(_ context.Context, _ db.Executor, taskID, uploaderID, originalName, displayName, storageKey, mimeType string, sizeBytes int64, isImage bool, _, _ string) (*repository.TaskAttachment, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.createCalls++
	return &repository.TaskAttachment{ID: "att-new", TaskID: taskID, UploaderID: uploaderID, OriginalName: originalName, DisplayName: displayName, StorageKey: storageKey, MimeType: mimeType, SizeBytes: sizeBytes, IsImage: isImage}, nil
}

func (f *fakeAttachmentRepo) Get(_ context.Context, _ db.Executor, id string) (*repository.TaskAttachment, error) {
	a, ok := f.byID[id]
	if !ok {
		return nil, domain.ErrAttachmentNotFound
	}
	return a, nil
}

func (f *fakeAttachmentRepo) ListForTask(_ context.Context, _ db.Executor, _ string) ([]repository.TaskAttachment, error) {
	return nil, nil
}

func (f *fakeAttachmentRepo) ListForWorkspace(_ context.Context, _ db.Executor, _ string, _ *repository.AttachmentFilter) ([]repository.TaskAttachment, int, error) {
	return nil, 0, nil
}

func (f *fakeAttachmentRepo) Rename(_ context.Context, _ db.Executor, _, _, _, _, _ string, _ *repository.TaskAttachment) error {
	return nil
}

func (f *fakeAttachmentRepo) SoftDelete(_ context.Context, _ db.Executor, id string, purgeAt time.Time, _, _, _ string, _ *repository.TaskAttachment) error {
	f.softDeleteCalls = append(f.softDeleteCalls, struct {
		id      string
		purgeAt time.Time
	}{id, purgeAt})
	return nil
}

func (f *fakeAttachmentRepo) PermanentDelete(_ context.Context, _ db.Executor, id, _, _, _ string, _ *repository.TaskAttachment) error {
	f.permanentDeleteCalls = append(f.permanentDeleteCalls, id)
	return nil
}

func (f *fakeAttachmentRepo) Restore(_ context.Context, _ db.Executor, id, _, _, _ string, _ *repository.TaskAttachment) error {
	f.restoreCalls = append(f.restoreCalls, id)
	return nil
}

func (f *fakeAttachmentRepo) OrgUsageBytes(_ context.Context, _ db.Executor, _ string) (int64, error) {
	return f.orgUsageBytes, f.orgUsageErr
}

func (f *fakeAttachmentRepo) PerProjectUsage(_ context.Context, _ db.Executor, _ string) ([]repository.ProjectUsage, error) {
	return nil, nil
}

func (f *fakeAttachmentRepo) ListGroupAdmins(_ context.Context, _ db.Executor, _ string) ([]repository.GroupAdminContact, error) {
	return f.groupAdmins, nil
}

func (f *fakeAttachmentRepo) LogQuotaRequest(_ context.Context, _ db.Executor, _, _, _, _ string, _ int, _ string) error {
	f.quotaRequestLogged = true
	return nil
}

type fakeAttachmentTaskResolver struct{ projectID string }

func (f *fakeAttachmentTaskResolver) GetProjectID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return f.projectID, nil
}

type fakeAttachmentProjectResolver struct{ workspaceID string }

func (f *fakeAttachmentProjectResolver) GetWorkspaceID(_ context.Context, _ db.Executor, _ string) (string, error) {
	return f.workspaceID, nil
}

type fakeAttachmentWorkspaceInfo struct{ ws *repository.Workspace }

func (f *fakeAttachmentWorkspaceInfo) Get(_ context.Context, _ db.Executor, _ string) (*repository.Workspace, error) {
	return f.ws, nil
}

type fakeAttachmentOrgQuota struct{ info *repository.AttachmentQuotaInfo }

func (f *fakeAttachmentOrgQuota) GetAttachmentQuotaInfo(_ context.Context, _ db.Executor, _ string) (*repository.AttachmentQuotaInfo, error) {
	return f.info, nil
}

type fakeAttachmentRoleChecker struct{ role string }

func (f *fakeAttachmentRoleChecker) GetMemberRole(_ context.Context, _ db.Executor, _, _ string) (string, error) {
	return f.role, nil
}

type fakeAttachmentProjectRoleChecker struct {
	role  string
	found bool
}

func (f *fakeAttachmentProjectRoleChecker) GetRole(_ context.Context, _ db.Executor, _, _ string) (role string, found bool, err error) {
	return f.role, f.found, nil
}

type fakeAttachmentStorage struct {
	uploadCalls []string
	deleteCalls []string
}

func (f *fakeAttachmentStorage) Upload(_ context.Context, key string, _ []byte, _ string) error {
	f.uploadCalls = append(f.uploadCalls, key)
	return nil
}
func (f *fakeAttachmentStorage) Download(_ context.Context, _ string) ([]byte, error) {
	return []byte("data"), nil
}
func (f *fakeAttachmentStorage) Delete(_ context.Context, key string) error {
	f.deleteCalls = append(f.deleteCalls, key)
	return nil
}

type fakeAttachmentRefresher struct{ calls []string }

func (f *fakeAttachmentRefresher) Enqueue(_ context.Context, orgID string) error {
	f.calls = append(f.calls, orgID)
	return nil
}

func newTestAttachmentService(repo *fakeAttachmentRepo, role string, quota *repository.AttachmentQuotaInfo, storage *fakeAttachmentStorage, refresher *fakeAttachmentRefresher) *TaskAttachmentService {
	if quota == nil {
		quota = &repository.AttachmentQuotaInfo{OrgID: "org-1", OrgName: "Org 1", GroupID: "group-1", QuotaBytes: 100 * 1024 * 1024, RetentionDays: 90}
	}
	if storage == nil {
		storage = &fakeAttachmentStorage{}
	}
	if refresher == nil {
		refresher = &fakeAttachmentRefresher{}
	}
	return NewTaskAttachmentService(
		repo,
		&fakeAttachmentTaskResolver{projectID: "proj-1"},
		&fakeAttachmentProjectResolver{workspaceID: "ws-1"},
		&fakeAttachmentWorkspaceInfo{ws: &repository.Workspace{ID: "ws-1", Name: "Marketing"}},
		&fakeAttachmentOrgQuota{info: quota},
		&fakeAttachmentRoleChecker{role: role},
		&fakeAttachmentProjectRoleChecker{},
		storage,
		refresher,
	)
}

// --- validateAttachmentFile ---

func TestValidateAttachmentFile_RejectsBlockedExtension(t *testing.T) {
	_, _, err := validateAttachmentFile("virus.exe", []byte("MZ..."))
	if !errors.Is(err, domain.ErrAttachmentTypeNotAllowed) {
		t.Errorf("err = %v, want ErrAttachmentTypeNotAllowed", err)
	}
}

func TestValidateAttachmentFile_RejectsExtensionNotOnWhitelist(t *testing.T) {
	_, _, err := validateAttachmentFile("archive.tar.gz", []byte("data"))
	if !errors.Is(err, domain.ErrAttachmentTypeNotAllowed) {
		t.Errorf("err = %v, want ErrAttachmentTypeNotAllowed (ekstensi .gz tidak di whitelist)", err)
	}
}

// TestValidateAttachmentFile_RejectsRenamedExecutable -- US-064 AC "validasi
// MIME, bukan hanya ekstensi": .exe di-rename jadi .pdf harus tetap ditolak
// via magic-byte sniff (MZ header -- signature PE/EXE Windows).
func TestValidateAttachmentFile_RejectsRenamedExecutable(t *testing.T) {
	mzHeader := []byte{0x4D, 0x5A, 0x90, 0x00, 0x03, 0x00, 0x00, 0x00}
	_, _, err := validateAttachmentFile("laporan.pdf", mzHeader)
	if !errors.Is(err, domain.ErrAttachmentTypeNotAllowed) {
		t.Errorf("err = %v, want ErrAttachmentTypeNotAllowed (MZ header disamarkan .pdf)", err)
	}
}

func TestValidateAttachmentFile_AllowsPlainText(t *testing.T) {
	mimeType, isImage, err := validateAttachmentFile("catatan.txt", []byte("halo dunia"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isImage {
		t.Errorf("isImage = true, want false untuk .txt")
	}
	if mimeType == "" {
		t.Errorf("mimeType kosong, want terisi dari http.DetectContentType")
	}
}

// --- canEditAttachment / canManageAttachment ---

func TestCanEditAttachment_ViewerDenied(t *testing.T) {
	if canEditAttachment("viewer") {
		t.Errorf("viewer should not be able to edit attachments")
	}
	if canEditAttachment("division_viewer") {
		t.Errorf("division_viewer should not be able to edit attachments")
	}
	if !canEditAttachment("editor") {
		t.Errorf("editor should be able to edit attachments")
	}
}

func TestCanManageAttachment_UploaderOrAdminOnly(t *testing.T) {
	cases := []struct {
		role       string
		isUploader bool
		want       bool
	}{
		{"viewer", true, true},           // uploader sendiri, selalu boleh
		{"viewer", false, false},         // bukan uploader, bukan admin
		{"", false, true},                // role kosong = PA/GA, selalu Full
		{"admin_workspace", false, true}, // AW
		{"project_manager", false, true}, // PM
		{"editor", false, false},         // editor biasa, bukan uploader
	}
	for _, c := range cases {
		got := canManageAttachment(c.role, c.isUploader)
		if got != c.want {
			t.Errorf("canManageAttachment(%q, %v) = %v, want %v", c.role, c.isUploader, got, c.want)
		}
	}
}

// --- Upload quota gate ---

func TestUpload_RejectsWhenLiveUsageOverQuota(t *testing.T) {
	repo := &fakeAttachmentRepo{orgUsageBytes: 99 * 1024 * 1024} // hampir penuh
	quota := &repository.AttachmentQuotaInfo{OrgID: "org-1", QuotaBytes: 100 * 1024 * 1024, RetentionDays: 90}
	svc := newTestAttachmentService(repo, "editor", quota, nil, nil)

	data := make([]byte, 2*1024*1024) // 2MB, akan melampaui sisa 1MB
	_, err := svc.Upload(context.Background(), nil, "task-1", "berkas.txt", data, "user-1", "member")
	if !errors.Is(err, domain.ErrStorageQuotaFull) {
		t.Errorf("err = %v, want ErrStorageQuotaFull", err)
	}
	if repo.createCalls != 0 {
		t.Errorf("createCalls = %d, want 0 (upload ditolak sebelum tulis DB)", repo.createCalls)
	}
}

// TestUpload_UsesLiveUsageNotStaleColumn -- gate kuota HARUS pakai
// OrgUsageBytes (live SELECT), bukan quota.UsedBytes (kolom yang cuma
// diperbarui async lewat job refresh) -- kalau service keliru baca
// UsedBytes yang basi, upload bisa lolos padahal kuota organisasi
// sebenarnya sudah penuh.
func TestUpload_UsesLiveUsageNotStaleColumn(t *testing.T) {
	repo := &fakeAttachmentRepo{orgUsageBytes: 99 * 1024 * 1024} // live: hampir penuh
	quota := &repository.AttachmentQuotaInfo{OrgID: "org-1", QuotaBytes: 100 * 1024 * 1024, UsedBytes: 0, RetentionDays: 90} // kolom basi: masih 0
	svc := newTestAttachmentService(repo, "editor", quota, nil, nil)

	data := make([]byte, 2*1024*1024)
	_, err := svc.Upload(context.Background(), nil, "task-1", "berkas.txt", data, "user-1", "member")
	if !errors.Is(err, domain.ErrStorageQuotaFull) {
		t.Errorf("err = %v, want ErrStorageQuotaFull (harus pakai live usage, bukan UsedBytes basi)", err)
	}
}

func TestUpload_RejectsOverMaxSize(t *testing.T) {
	repo := &fakeAttachmentRepo{}
	svc := newTestAttachmentService(repo, "editor", nil, nil, nil)

	data := make([]byte, maxAttachmentSizeBytes+1)
	_, err := svc.Upload(context.Background(), nil, "task-1", "besar.zip", data, "user-1", "member")
	if !errors.Is(err, domain.ErrAttachmentTooLarge) {
		t.Errorf("err = %v, want ErrAttachmentTooLarge", err)
	}
}

func TestUpload_ForbiddenForViewer(t *testing.T) {
	repo := &fakeAttachmentRepo{}
	svc := newTestAttachmentService(repo, "viewer", nil, nil, nil)

	_, err := svc.Upload(context.Background(), nil, "task-1", "berkas.txt", []byte("data"), "user-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
	if repo.createCalls != 0 {
		t.Errorf("createCalls = %d, want 0", repo.createCalls)
	}
}

func TestUpload_SuccessEnqueuesQuotaRefresh(t *testing.T) {
	repo := &fakeAttachmentRepo{orgUsageBytes: 0}
	storage := &fakeAttachmentStorage{}
	refresher := &fakeAttachmentRefresher{}
	svc := newTestAttachmentService(repo, "editor", nil, storage, refresher)

	att, err := svc.Upload(context.Background(), nil, "task-1", "berkas.txt", []byte("halo"), "user-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.ID == "" {
		t.Errorf("att.ID kosong, want terisi")
	}
	if len(storage.uploadCalls) != 1 {
		t.Fatalf("uploadCalls = %d, want 1", len(storage.uploadCalls))
	}
	if len(refresher.calls) != 1 || refresher.calls[0] != "org-1" {
		t.Errorf("refresher.calls = %v, want [org-1] (refresh kuota WAJIB di-enqueue setelah upload sukses)", refresher.calls)
	}
}

// --- Delete / Restore / PermanentDelete authorization ---

func TestDelete_ForbiddenForNonUploaderNonAdmin(t *testing.T) {
	repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{
		"att-1": {ID: "att-1", TaskID: "task-1", UploaderID: "other-user"},
	}}
	svc := newTestAttachmentService(repo, "editor", nil, nil, nil)

	err := svc.Delete(context.Background(), nil, "att-1", "user-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
	if len(repo.softDeleteCalls) != 0 {
		t.Errorf("softDeleteCalls = %d, want 0", len(repo.softDeleteCalls))
	}
}

func TestDelete_AllowedForUploader(t *testing.T) {
	repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{
		"att-1": {ID: "att-1", TaskID: "task-1", UploaderID: "user-1"},
	}}
	quota := &repository.AttachmentQuotaInfo{OrgID: "org-1", QuotaBytes: 100, RetentionDays: 30}
	svc := newTestAttachmentService(repo, "editor", quota, nil, nil)

	if err := svc.Delete(context.Background(), nil, "att-1", "user-1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.softDeleteCalls) != 1 {
		t.Fatalf("softDeleteCalls = %d, want 1", len(repo.softDeleteCalls))
	}
	wantPurge := time.Now().Add(30 * 24 * time.Hour)
	gotPurge := repo.softDeleteCalls[0].purgeAt
	if gotPurge.Sub(wantPurge) > time.Minute || wantPurge.Sub(gotPurge) > time.Minute {
		t.Errorf("purgeAt = %v, want ~%v (retention_days=30)", gotPurge, wantPurge)
	}
}

func TestDelete_AllowedForAdminWorkspaceEvenIfNotUploader(t *testing.T) {
	repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{
		"att-1": {ID: "att-1", TaskID: "task-1", UploaderID: "other-user"},
	}}
	svc := newTestAttachmentService(repo, "admin_workspace", nil, nil, nil)

	if err := svc.Delete(context.Background(), nil, "att-1", "aw-1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.softDeleteCalls) != 1 {
		t.Errorf("softDeleteCalls = %d, want 1", len(repo.softDeleteCalls))
	}
}

func TestRestore_ForbiddenForNonUploaderNonAdmin(t *testing.T) {
	repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{
		"att-1": {ID: "att-1", TaskID: "task-1", UploaderID: "other-user"},
	}}
	svc := newTestAttachmentService(repo, "viewer", nil, nil, nil)

	err := svc.Restore(context.Background(), nil, "att-1", "user-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

// --- authorizeWorkspace / PermanentDelete / checkWorkspaceNameConfirm ---

func TestPermanentDelete_ForbiddenForNonAdminWorkspace(t *testing.T) {
	repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{
		"att-1": {ID: "att-1", TaskID: "task-1"},
	}}
	svc := newTestAttachmentService(repo, "editor", nil, nil, nil)

	err := svc.PermanentDelete(context.Background(), nil, "ws-1", "att-1", "Marketing", "user-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden (hapus permanen AW-only)", err)
	}
	if len(repo.permanentDeleteCalls) != 0 {
		t.Errorf("permanentDeleteCalls = %d, want 0", len(repo.permanentDeleteCalls))
	}
}

func TestPermanentDelete_RejectsWrongWorkspaceNameConfirm(t *testing.T) {
	repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{
		"att-1": {ID: "att-1", TaskID: "task-1", StorageKey: "orgs/org-1/tasks/task-1/x-berkas.txt"},
	}}
	storage := &fakeAttachmentStorage{}
	svc := newTestAttachmentService(repo, "admin_workspace", nil, storage, nil)

	err := svc.PermanentDelete(context.Background(), nil, "ws-1", "att-1", "Nama Salah", "aw-1", "member")
	if !errors.Is(err, domain.ErrWorkspaceNameConfirmMismatch) {
		t.Errorf("err = %v, want ErrWorkspaceNameConfirmMismatch", err)
	}
	if len(repo.permanentDeleteCalls) != 0 || len(storage.deleteCalls) != 0 {
		t.Errorf("permanentDeleteCalls=%d deleteCalls=%d, want 0/0 (gagal SEBELUM hapus DB/objek)", len(repo.permanentDeleteCalls), len(storage.deleteCalls))
	}
}

// TestPermanentDelete_CaseInsensitiveAndTrimmed -- konfirmasi ketik nama
// workspace toleran spasi di ujung dan beda kapitalisasi (pola sama modal
// konfirmasi ketik-ulang lain di sistem ini).
func TestPermanentDelete_CaseInsensitiveAndTrimmed(t *testing.T) {
	repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{
		"att-1": {ID: "att-1", TaskID: "task-1", StorageKey: "orgs/org-1/tasks/task-1/x-berkas.txt"},
	}}
	storage := &fakeAttachmentStorage{}
	svc := newTestAttachmentService(repo, "admin_workspace", nil, storage, nil)

	if err := svc.PermanentDelete(context.Background(), nil, "ws-1", "att-1", "  marketing  ", "aw-1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.permanentDeleteCalls) != 1 {
		t.Fatalf("permanentDeleteCalls = %d, want 1", len(repo.permanentDeleteCalls))
	}
	if len(storage.deleteCalls) != 1 || storage.deleteCalls[0] != "orgs/org-1/tasks/task-1/x-berkas.txt" {
		t.Errorf("deleteCalls = %v, want objek MinIO asli terhapus SEKETIKA", storage.deleteCalls)
	}
}

// --- BulkDelete ---

func TestBulkDelete_PermanentModeRequiresConfirmOnce(t *testing.T) {
	repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{
		"att-1": {ID: "att-1", TaskID: "task-1"},
		"att-2": {ID: "att-2", TaskID: "task-1"},
	}}
	svc := newTestAttachmentService(repo, "admin_workspace", nil, nil, nil)

	_, err := svc.BulkDelete(context.Background(), nil, "ws-1", []string{"att-1", "att-2"}, "permanen", "Salah", "aw-1", "member")
	if !errors.Is(err, domain.ErrWorkspaceNameConfirmMismatch) {
		t.Errorf("err = %v, want ErrWorkspaceNameConfirmMismatch", err)
	}
	if len(repo.permanentDeleteCalls) != 0 {
		t.Errorf("permanentDeleteCalls = %d, want 0 (gagal sebelum baris manapun diproses)", len(repo.permanentDeleteCalls))
	}
}

func TestBulkDelete_RetentionModePartialFailureDoesNotStopOthers(t *testing.T) {
	repo := &fakeAttachmentRepo{byID: map[string]*repository.TaskAttachment{
		"att-1": {ID: "att-1", TaskID: "task-1"},
		// att-2 sengaja TIDAK ada di byID -- Get akan gagal, baris ini
		// harus dilewati tanpa menggagalkan att-1/att-3.
		"att-3": {ID: "att-3", TaskID: "task-1"},
	}}
	svc := newTestAttachmentService(repo, "admin_workspace", nil, nil, nil)

	succeeded, err := svc.BulkDelete(context.Background(), nil, "ws-1", []string{"att-1", "att-2", "att-3"}, "retensi", "", "aw-1", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if succeeded != 2 {
		t.Errorf("succeeded = %d, want 2 (att-2 gagal, att-1/att-3 tetap sukses)", succeeded)
	}
	if len(repo.softDeleteCalls) != 2 {
		t.Errorf("softDeleteCalls = %d, want 2", len(repo.softDeleteCalls))
	}
}

func TestBulkDelete_ForbiddenForNonAdminWorkspace(t *testing.T) {
	repo := &fakeAttachmentRepo{}
	svc := newTestAttachmentService(repo, "editor", nil, nil, nil)

	_, err := svc.BulkDelete(context.Background(), nil, "ws-1", []string{"att-1"}, "retensi", "", "user-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

// --- RequestQuota ---

func TestRequestQuota_NotifiesEveryGroupAdmin(t *testing.T) {
	repo := &fakeAttachmentRepo{groupAdmins: []repository.GroupAdminContact{
		{ID: "ga-1", Email: "ga1@example.com", Name: "GA Satu"},
		{ID: "ga-2", Email: "ga2@example.com", Name: "GA Dua"},
	}}
	svc := newTestAttachmentService(repo, "admin_workspace", nil, nil, nil)

	spy := &spyExecutor{}
	if err := svc.RequestQuota(context.Background(), spy, "ws-1", 10, "butuh arsip vendor", "aw-1", "member"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spy.execCalls != 2 {
		t.Errorf("notifications inserted = %d, want 2 (satu per Group Admin)", spy.execCalls)
	}
	if !repo.quotaRequestLogged {
		t.Errorf("LogQuotaRequest tidak dipanggil, want tercatat di audit")
	}
}

func TestRequestQuota_RejectsNonPositiveAmount(t *testing.T) {
	repo := &fakeAttachmentRepo{}
	svc := newTestAttachmentService(repo, "admin_workspace", nil, nil, nil)

	err := svc.RequestQuota(context.Background(), nil, "ws-1", 0, "alasan", "aw-1", "member")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want ErrInvalidInput", err)
	}
}

// spyExecutor -- db.Executor minimal, cukup untuk menghitung berapa kali
// Exec dipanggil (RequestQuota INSERT notifikasi langsung lewat exec, tanpa
// lewat repository).
type spyExecutor struct {
	execCalls int
}

func (s *spyExecutor) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	s.execCalls++
	return pgconn.CommandTag{}, nil
}
func (s *spyExecutor) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, nil
}
func (s *spyExecutor) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return nil
}
