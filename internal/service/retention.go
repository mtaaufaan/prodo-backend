package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// retentionRepository -- interface didefinisikan di consumer, §3.9.
type retentionRepository interface {
	ListSchedule(ctx context.Context, exec db.Executor, groupID string) ([]repository.RetentionScheduleItem, error)
	CreateExport(ctx context.Context, exec db.Executor, groupID, kind, itemName string, payload []byte, tokenHash, requestedBy string, expiresAt time.Time) (string, error)
	FindExportByTokenHash(ctx context.Context, exec db.Executor, tokenHash string) (*repository.RetentionExport, error)
}

// retentionGroupAuthorizer -- reuse OrganizationRepository.IsGroupAdminOfGroup
// langsung (sama pola WorkspaceService.ListWorkspacesByGroup), tanpa
// perantara OrganizationService supaya tidak menambah dependensi silang
// antar service untuk satu pengecekan sederhana.
type retentionGroupAuthorizer interface {
	IsGroupAdminOfGroup(ctx context.Context, exec db.Executor, userID, groupID string) (bool, error)
}

// retentionUserLookup -- email+nama actor untuk email hasil ekspor (Data
// Retention, "tautan dikirim ke email Group Admin").
type retentionUserLookup interface {
	GetUserContact(ctx context.Context, exec db.Executor, userID string) (email, displayName string, err error)
}

// RetentionExportTTL -- masa berlaku tautan unduhan (desain "GA Data
// Retention.dc.html": "berlaku 72 jam").
const RetentionExportTTL = 72 * time.Hour

// RetentionService -- Data Retention (Track S4G, desain "GA Data
// Retention.dc.html"). Restore SENGAJA tidak ada method di sini --
// dilakukan lewat endpoint yang SUDAH ADA per kind (organization.Reactivate,
// workspace.Restore, project.Restore), FE yang memilih endpoint sesuai
// `kind` baris terpilih. Service ini cuma baca jadwal + kelola ekspor.
type RetentionService struct {
	repo       retentionRepository
	orgs       retentionGroupAuthorizer
	users      retentionUserLookup
	emailer    *EmailService
	appBaseURL string
}

func NewRetentionService(repo retentionRepository, orgs retentionGroupAuthorizer, users retentionUserLookup, emailer *EmailService, appBaseURL string) *RetentionService {
	return &RetentionService{repo: repo, orgs: orgs, users: users, emailer: emailer, appBaseURL: appBaseURL}
}

func (s *RetentionService) authorizeGroup(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) error {
	if actorRole == "platform_admin" {
		return nil
	}
	isGA, err := s.orgs.IsGroupAdminOfGroup(ctx, exec, actorID, groupID)
	if err != nil {
		return fmt.Errorf("service.authorizeGroup: %w", err)
	}
	if !isGA {
		return fmt.Errorf("service.authorizeGroup: %w", domain.ErrForbidden)
	}
	return nil
}

// GetSchedule mengembalikan Jadwal Penghapusan grup (organisasi nonaktif +
// workspace/project soft-deleted), urut sisa hari.
func (s *RetentionService) GetSchedule(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) ([]repository.RetentionScheduleItem, error) {
	if groupID == "" {
		return nil, fmt.Errorf("service.GetSchedule: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return nil, err
	}
	list, err := s.repo.ListSchedule(ctx, exec, groupID)
	if err != nil {
		return nil, fmt.Errorf("service.GetSchedule: %w", err)
	}
	return list, nil
}

// exportManifest -- isi payload JSON tersimpan + dikembalikan rute
// unduhan. CUMA metadata yang sungguhan ada di DB (dikonfirmasi user) --
// folder /data (task/komentar) dan /attachments dari desain SENGAJA
// dikosongkan, dicatat di `note` -- lihat implementation_gaps.md.
type exportManifest struct {
	Kind        string    `json:"kind"`
	ItemName    string    `json:"item_name"`
	OrgName     string    `json:"org_name"`
	RequestedAt time.Time `json:"requested_at"`
	Note        string    `json:"note"`
}

const exportManifestNote = "Arsip ini cuma berisi metadata yang tersedia di database saat ini (nama & kaitan organisasi). " +
	"Folder /data (task, komentar) dan /attachments dari rancangan asli belum bisa disertakan -- " +
	"fitur task management dan upload attachment belum dibangun."

// RequestExport membuat permintaan ekspor untuk satu item jadwal + kirim
// email tautan unduhan (72 jam) ke actor yang meminta (desain: "tautan
// dikirim ke email Group Admin"). itemName/orgName SENGAJA diresolve
// server-side lewat ListSchedule (bukan dipercaya dari body request) --
// sekaligus menegakkan item itu benar ada dan milik groupID ini.
func (s *RetentionService) RequestExport(ctx context.Context, exec db.Executor, groupID, kind, itemID, actorID, actorRole string) error {
	if groupID == "" || kind == "" || itemID == "" {
		return fmt.Errorf("service.RequestExport: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return err
	}

	schedule, err := s.repo.ListSchedule(ctx, exec, groupID)
	if err != nil {
		return fmt.Errorf("service.RequestExport: %w", err)
	}
	var itemName, orgName string
	found := false
	for i := range schedule {
		it := &schedule[i]
		if it.Kind == kind && it.ItemID == itemID {
			itemName, orgName = it.ItemName, it.OrgName
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("service.RequestExport: %w", domain.ErrRetentionExportNotFound)
	}

	manifest := exportManifest{Kind: kind, ItemName: itemName, OrgName: orgName, RequestedAt: time.Now(), Note: exportManifestNote}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("service.RequestExport: encode manifest: %w", err)
	}

	rawToken, tokenHash, err := generateActivationToken()
	if err != nil {
		return fmt.Errorf("service.RequestExport: %w", err)
	}
	expiresAt := time.Now().Add(RetentionExportTTL)

	if _, err := s.repo.CreateExport(ctx, exec, groupID, kind, itemName, payload, tokenHash, actorID, expiresAt); err != nil {
		return fmt.Errorf("service.RequestExport: %w", err)
	}

	email, displayName, err := s.users.GetUserContact(ctx, exec, actorID)
	if err != nil {
		return fmt.Errorf("service.RequestExport: %w", err)
	}
	downloadLink := fmt.Sprintf("%s/retention-exports/%s", s.appBaseURL, rawToken)
	if err := s.emailer.SendRetentionExportEmail(ctx, email, displayName, itemName, downloadLink, expiresAt); err != nil {
		return fmt.Errorf("service.RequestExport: kirim email: %w", err)
	}
	return nil
}

// DownloadExport mengembalikan payload manifest untuk token mentah dari
// tautan email -- rute PUBLIK (tanpa sesi JWT), otorisasi sepenuhnya lewat
// kepemilikan token (sama pola AcceptInvitation).
func (s *RetentionService) DownloadExport(ctx context.Context, exec db.Executor, rawToken string) (*repository.RetentionExport, error) {
	if rawToken == "" {
		return nil, fmt.Errorf("service.DownloadExport: %w", domain.ErrInvalidInput)
	}
	export, err := s.repo.FindExportByTokenHash(ctx, exec, hashActivationToken(rawToken))
	if err != nil {
		return nil, fmt.Errorf("service.DownloadExport: %w", err)
	}
	return export, nil
}
