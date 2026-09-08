// Package service -- Import Data (S4G-15/16/17/18, Track S4G, desain
// "GA Import Data.dc.html"). CUMA kind "member" yang dibangun -- kind
// "task" dari desain (migrasi task dari sistem lama) tidak bisa dibangun,
// tabel `tasks` belum ada sama sekali (Task Management Core belum
// dibangun), dikonfirmasi user 2026-09-08.
//
// Validasi (dry-run) dan eksekusi SENGAJA sinkron (bukan pola upload-lalu-
// proses-terpisah) untuk parsing/validasi -- CUMA eksekusi sungguhan yang
// lewat job Asynq (dikonfirmasi user, beda dari precedent
// InvitationService.CreateBulkInvitations yang sinkron penuh, karena desain
// ini eksplisit minta proses background + laporan hasil terpisah).
package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/pkg/validator"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// MemberImportRow -- satu baris CSV member-import, sebelum (dry-run) dan
// sesudah (final) eksekusi. Status dry-run: "valid" (email baru, akan
// dapat undangan) | "existing" (user sudah ada, langsung ditambahkan) |
// "skipped" (dilewati, lihat Reason). Setelah eksekusi, "valid"/"existing"
// yang berhasil ditulis DB tetap "valid"/"existing"; yang gagal saat
// eksekusi (mis. race condition) ditimpa jadi "skipped".
type MemberImportRow struct {
	RowNum        int    `json:"row"`
	Email         string `json:"email"`
	Name          string `json:"name,omitempty"`
	Role          string `json:"role"`
	WorkspaceName string `json:"workspace"`
	WorkspaceID   string `json:"workspace_id,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
}

var validWorkspaceRoles = map[string]bool{
	"admin_workspace": true, "project_manager": true, "editor": true,
	"approver": true, "viewer": true, "division_viewer": true,
}

// MemberImportTemplateCSV -- template kolom persis desain ("KOLOM: email,
// nama, role, workspace").
func MemberImportTemplateCSV() []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"email", "nama", "role", "workspace"})
	_ = w.Write([]string{"nama1@perusahaan.com", "Nama Lengkap", "editor", "Nama Workspace"})
	w.Flush()
	return buf.Bytes()
}

// maxCSVRows/maxCSVBytes -- batas AC eksplisit desain ("Maksimum 5.000
// baris per berkas, ukuran ≤ 10 MB").
const (
	maxCSVRows  = 5000
	maxCSVBytes = 10 * 1024 * 1024
)

// parseMemberCSV mem-parse berkas CSV member-import. Kolom dicocokkan
// case-insensitive; kolom wajib email/role/workspace, nama opsional.
// Duplikat email DALAM satu berkas ditandai skipped pada kemunculan kedua
// dst -- CreateBulkInvitations di InvitationService cuma dedupe DALAM satu
// panggilan batch, sedang CSVImportService memanggilnya SATU EMAIL per
// baris (lihat komentar Execute), jadi dedupe wajib dilakukan di sini.
func parseMemberCSV(data []byte) ([]MemberImportRow, error) {
	if len(data) > maxCSVBytes {
		return nil, fmt.Errorf("service.parseMemberCSV: %w", domain.ErrCSVTooLarge)
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("service.parseMemberCSV: baca header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, required := range []string{"email", "role", "workspace"} {
		if _, ok := col[required]; !ok {
			return nil, fmt.Errorf("service.parseMemberCSV: %w: kolom %q wajib ada", domain.ErrInvalidInput, required)
		}
	}
	get := func(rec []string, key string) string {
		i, ok := col[key]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	rows := make([]MemberImportRow, 0)
	rowNum := 1
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("service.parseMemberCSV: baris %d: %w", rowNum+1, err)
		}
		rowNum++
		if rowNum > maxCSVRows+1 {
			return nil, fmt.Errorf("service.parseMemberCSV: %w", domain.ErrCSVTooManyRows)
		}
		rows = append(rows, MemberImportRow{
			RowNum: rowNum, Email: strings.ToLower(get(rec, "email")), Name: get(rec, "nama"),
			Role: strings.ToLower(get(rec, "role")), WorkspaceName: get(rec, "workspace"),
		})
	}
	return rows, nil
}

// csvImportOrgLister -- interface didefinisikan di consumer, reuse
// OrganizationRepository.List (S4G-03) yang sudah ada.
type csvImportOrgLister interface {
	List(ctx context.Context, exec db.Executor, groupID string) ([]repository.Organization, int64, error)
}

// csvImportWorkspaceLister -- interface didefinisikan di consumer, reuse
// WorkspaceRepository.List (S3-13) yang sudah ada.
type csvImportWorkspaceLister interface {
	List(ctx context.Context, exec db.Executor, orgID string) ([]repository.Workspace, error)
}

// csvImportRepo -- interface didefinisikan di consumer.
type csvImportRepo interface {
	Create(ctx context.Context, exec db.Executor, groupID, orgID, importedBy, actorRole, filename, storageKey string, totalRows int, rowResults []byte) (string, error)
	Get(ctx context.Context, exec db.Executor, importID string) (*repository.CSVImport, error)
	ListByGroup(ctx context.Context, exec db.Executor, groupID string) ([]repository.CSVImport, error)
	MarkRunning(ctx context.Context, exec db.Executor, importID string) error
	Complete(ctx context.Context, exec db.Executor, importID string, successCount, failedCount int, rowResults []byte) error
	MarkFailed(ctx context.Context, exec db.Executor, importID string) error
	GetUserContact(ctx context.Context, exec db.Executor, userID string) (email, displayName string, err error)
}

// csvImportStorage -- interface didefinisikan di consumer, diimplementasikan
// *StorageService.
type csvImportStorage interface {
	Upload(ctx context.Context, key string, data []byte, contentType string) error
	Download(ctx context.Context, key string) ([]byte, error)
}

// csvImportEnqueuer -- interface didefinisikan di consumer, diimplementasikan
// *asynq.Client (cmd/api/main.go) -- PERTAMA KALI pola enqueue-dengan-payload
// dipakai di codebase ini, job periodik lain (StorageQuotaCheck/
// RetentionNotify) semuanya cron-only tanpa payload.
type csvImportEnqueuer interface {
	Enqueue(ctx context.Context, importID string) error
}

// CSVImportService mengorkestrasi validasi (dry-run, sinkron) dan memicu
// eksekusi (job Asynq) import member CSV. users/assigner dipakai untuk
// preview dry-run (cek existing user, TIDAK menulis apa pun) -- eksekusi
// sungguhan ada di CSVImportJob (internal/worker), yang memanggil
// InvitationService.CreateBulkInvitations langsung (reuse penuh S2-23).
type CSVImportService struct {
	repo    csvImportRepo
	orgs    csvImportOrgLister
	ws      csvImportWorkspaceLister
	users   existingUserFinder
	storage csvImportStorage
	queue   csvImportEnqueuer
}

func NewCSVImportService(repo csvImportRepo, orgs csvImportOrgLister, ws csvImportWorkspaceLister, users existingUserFinder, storage csvImportStorage, queue csvImportEnqueuer) *CSVImportService {
	return &CSVImportService{repo: repo, orgs: orgs, ws: ws, users: users, storage: storage, queue: queue}
}

// ValidateResult -- ringkasan dry-run dikembalikan ke handler.
type ValidateResult struct {
	ImportID  string
	Total     int
	ValidN    int
	ExistingN int
	SkippedN  int
	Preview   []MemberImportRow // 20 baris pertama, sama desain "PREVIEW DRY-RUN · 20 BARIS PERTAMA"
}

// Validate mem-parse+validasi CSV (sinkron, tanpa job) -- upload berkas asli
// ke MinIO untuk arsip/laporan, simpan hasil dry-run sebagai baris
// csv_imports status 'pending'. TIDAK menulis apa pun ke users/
// workspace_members -- itu baru terjadi saat Execute (job Asynq).
func (s *CSVImportService) Validate(ctx context.Context, exec db.Executor, groupID, orgID, filename string, data []byte, actorID, actorRole string) (*ValidateResult, error) {
	if groupID == "" || orgID == "" || len(data) == 0 {
		return nil, fmt.Errorf("service.Validate: %w", domain.ErrInvalidInput)
	}

	rows, err := parseMemberCSV(data)
	if err != nil {
		return nil, fmt.Errorf("service.Validate: %w", err)
	}

	orgs, _, err := s.orgs.List(ctx, exec, groupID)
	if err != nil {
		return nil, fmt.Errorf("service.Validate: %w", err)
	}
	var org *repository.Organization
	for i := range orgs {
		if orgs[i].ID == orgID {
			org = &orgs[i]
			break
		}
	}
	if org == nil {
		return nil, fmt.Errorf("service.Validate: %w", domain.ErrOrganizationNotFound)
	}

	workspaces, err := s.ws.List(ctx, exec, orgID)
	if err != nil {
		return nil, fmt.Errorf("service.Validate: %w", err)
	}
	wsByName := make(map[string]repository.Workspace, len(workspaces))
	for _, w := range workspaces {
		wsByName[strings.ToLower(w.Name)] = w
	}

	seen := make(map[string]bool, len(rows))
	var validN, existingN, skippedN int
	for i := range rows {
		row := &rows[i]
		switch {
		case row.Email == "":
			row.Status, row.Reason = "skipped", "Kolom email wajib diisi."
		case !validator.IsValidEmail(row.Email):
			row.Status, row.Reason = "skipped", "Format email tidak valid."
		case seen[row.Email]:
			row.Status, row.Reason = "skipped", "Duplikat email dalam berkas ini."
		case org.Domain != "" && !strings.HasSuffix(row.Email, "@"+org.Domain):
			row.Status, row.Reason = "skipped", fmt.Sprintf("Domain di luar domain email resmi organisasi (%s).", org.Domain)
		case !validWorkspaceRoles[row.Role]:
			row.Status, row.Reason = "skipped", "Role tidak dikenal. Gunakan nilai dari template."
		default:
			ws, ok := wsByName[strings.ToLower(row.WorkspaceName)]
			if !ok {
				row.Status, row.Reason = "skipped", fmt.Sprintf("Workspace %q tidak ditemukan di organisasi ini.", row.WorkspaceName)
			} else {
				row.WorkspaceID = ws.ID
				if _, err := s.users.FindUserIDByEmail(ctx, row.Email); err == nil {
					row.Status, row.Reason = "existing", "Sudah terdaftar — ditambahkan ke organisasi tanpa email registrasi ulang."
				} else {
					row.Status = "valid"
				}
			}
		}
		seen[row.Email] = true
		switch row.Status {
		case "valid":
			validN++
		case "existing":
			existingN++
		default:
			skippedN++
		}
	}

	storageKey := fmt.Sprintf("csv-imports/%s/%s-%s", groupID, uuid.NewString(), filename)
	if err := s.storage.Upload(ctx, storageKey, data, "text/csv"); err != nil {
		return nil, fmt.Errorf("service.Validate: %w", err)
	}

	rowResults, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("service.Validate: encode hasil: %w", err)
	}
	importID, err := s.repo.Create(ctx, exec, groupID, orgID, actorID, actorRole, filename, storageKey, len(rows), rowResults)
	if err != nil {
		return nil, fmt.Errorf("service.Validate: %w", err)
	}

	preview := rows
	if len(preview) > 20 {
		preview = preview[:20]
	}
	return &ValidateResult{ImportID: importID, Total: len(rows), ValidN: validN, ExistingN: existingN, SkippedN: skippedN, Preview: preview}, nil
}

// Execute mengecek import ada+belum dieksekusi, lalu mengantre job Asynq
// (CSVImportJob) -- penulisan sungguhan ke users/workspace_members/
// user_invitations terjadi DI JOB, bukan di sini (lihat komentar package).
func (s *CSVImportService) Execute(ctx context.Context, exec db.Executor, groupID, importID string) error {
	imp, err := s.repo.Get(ctx, exec, importID)
	if err != nil {
		return fmt.Errorf("service.Execute: %w", err)
	}
	if imp.GroupID != groupID {
		return fmt.Errorf("service.Execute: %w", domain.ErrCSVImportNotFound)
	}
	if imp.Status != "pending" {
		return fmt.Errorf("service.Execute: %w", domain.ErrCSVImportAlreadyStarted)
	}
	if err := s.queue.Enqueue(ctx, importID); err != nil {
		return fmt.Errorf("service.Execute: %w", err)
	}
	return nil
}

func (s *CSVImportService) Get(ctx context.Context, exec db.Executor, groupID, importID string) (*repository.CSVImport, error) {
	imp, err := s.repo.Get(ctx, exec, importID)
	if err != nil {
		return nil, fmt.Errorf("service.Get: %w", err)
	}
	if imp.GroupID != groupID {
		return nil, fmt.Errorf("service.Get: %w", domain.ErrCSVImportNotFound)
	}
	return imp, nil
}

func (s *CSVImportService) ListHistory(ctx context.Context, exec db.Executor, groupID string) ([]repository.CSVImport, error) {
	list, err := s.repo.ListByGroup(ctx, exec, groupID)
	if err != nil {
		return nil, fmt.Errorf("service.ListHistory: %w", err)
	}
	return list, nil
}

// Report menghasilkan CSV laporan hasil (baris berhasil+dilewati beserta
// alasan) dari row_results tersimpan -- TIDAK perlu download ulang dari
// MinIO, row_results sudah cukup.
func (s *CSVImportService) Report(ctx context.Context, exec db.Executor, groupID, importID string) ([]byte, error) {
	imp, err := s.Get(ctx, exec, groupID, importID)
	if err != nil {
		return nil, err
	}
	var rows []MemberImportRow
	if err := json.Unmarshal(imp.RowResults, &rows); err != nil {
		return nil, fmt.Errorf("service.Report: decode hasil: %w", err)
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"baris", "email", "role", "workspace", "status", "keterangan"})
	for _, r := range rows {
		_ = w.Write([]string{fmt.Sprintf("%d", r.RowNum), r.Email, r.Role, r.WorkspaceName, r.Status, r.Reason})
	}
	w.Flush()
	return buf.Bytes(), nil
}
