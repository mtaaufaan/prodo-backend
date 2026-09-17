// Package service -- WebhookService (Webhook, Track S4G "GA Webhook.dc.html"+
// "GA Add Webhook.dc.html", diperluas S4W-14 "AW Webhook.dc.html"+"AW Add
// Webhook.dc.html" untuk cakupan workspace/project). Lihat komentar migrasi
// 20260920090000_webhooks/20261020090000_webhook_configs_workspace_scope
// untuk penyimpangan skema, dan implementation_gaps.md IG-44 untuk daftar 7
// dari 10 event desain yang belum bisa dibangun (tidak punya tabel/service)
// -- gap ini berlaku SAMA untuk cakupan workspace, tidak ditutup di sini.
package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// SupportedWebhookEvents -- HANYA 3 dari 10 event desain yang punya trigger
// nyata sekarang (project.created/updated/deleted, ProjectService). 7
// lainnya (task.*/comment.created/rule.executed) TIDAK ditawarkan sama
// sekali -- dikonfirmasi user, sama pola drop kind "task" di Import Data.
var SupportedWebhookEvents = []string{"project.created", "project.updated", "project.deleted"}

var webhookURLPattern = regexp.MustCompile(`^https://\S+\.\S+`)

const (
	webhookDeliveryTimeout = 10 * time.Second
	webhookMaxRetries      = 3
)

// webhookRepository -- interface didefinisikan di consumer, §3.9.
type webhookRepository interface {
	Create(ctx context.Context, exec db.Executor, groupID, workspaceID string, orgID, projectID *string, name, targetURL, secret string, events []string, actorID, actorRole string) (string, error)
	Get(ctx context.Context, exec db.Executor, webhookID string) (*repository.Webhook, error)
	ListByGroup(ctx context.Context, exec db.Executor, groupID string) ([]repository.Webhook, error)
	ListByWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.Webhook, error)
	ListActiveForEvent(ctx context.Context, exec db.Executor, groupID, orgID, eventType string) ([]repository.Webhook, error)
	ListActiveForEventWorkspace(ctx context.Context, exec db.Executor, workspaceID, projectID, eventType string) ([]repository.Webhook, error)
	DecryptSecret(ctx context.Context, exec db.Executor, webhookID string) (string, error)
	Update(ctx context.Context, exec db.Executor, webhookID, name, targetURL string, orgID, projectID *string, events []string, actorID, actorRole string, before *repository.Webhook) error
	SetActive(ctx context.Context, exec db.Executor, webhookID string, active bool, actorID, actorRole string, before *repository.Webhook) error
	RegenerateSecret(ctx context.Context, exec db.Executor, webhookID, newSecret string, actorID, actorRole string, before *repository.Webhook) error
	Delete(ctx context.Context, exec db.Executor, webhookID, actorID, actorRole string, before *repository.Webhook) error
	CreateDelivery(ctx context.Context, exec db.Executor, webhookID, eventType string, payload []byte, attemptNumber int, status string, httpStatus *int, responseBody, errMessage *string, durationMs int) error
	ListDeliveries(ctx context.Context, exec db.Executor, groupID, statusFilter, webhookIDFilter string) ([]repository.WebhookDelivery, error)
	ListDeliveriesForWorkspace(ctx context.Context, exec db.Executor, workspaceID, statusFilter, webhookIDFilter string) ([]repository.WebhookDelivery, error)
	GroupAdminContacts(ctx context.Context, exec db.Executor, groupID string) ([]repository.GroupAdminContact, error)
	WorkspaceAdminContacts(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.GroupAdminContact, error)
}

// webhookGroupAuthorizer -- reuse OrganizationRepository, pola sama
// RetentionService.
type webhookGroupAuthorizer interface {
	IsGroupAdminOfGroup(ctx context.Context, exec db.Executor, userID, groupID string) (bool, error)
	GetGroupID(ctx context.Context, exec db.Executor, orgID string) (string, error)
}

// webhookProjectWorkspaceResolver -- reuse ProjectRepository.GetWorkspaceID,
// dipakai memvalidasi project_id (LINGKUP "Project X") benar milik
// workspaceID yang sama sebelum Create/Update workspace-scope.
type webhookProjectWorkspaceResolver interface {
	GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error)
}

// webhookDeliveryEnqueuer -- diimplementasikan cmd/api/main.go
// (asynqWebhookEnqueuer), pola sama csvImportEnqueuer (Import Data).
type webhookDeliveryEnqueuer interface {
	Enqueue(ctx context.Context, webhookID, eventType string, payload []byte) error
}

type WebhookService struct {
	repo     webhookRepository
	orgs     webhookGroupAuthorizer
	rbac     sprintWorkspaceRoleChecker
	projects webhookProjectWorkspaceResolver
	enqueuer webhookDeliveryEnqueuer
	emailer  *EmailService
	logger   *zap.Logger
	client   *http.Client
}

func NewWebhookService(repo webhookRepository, orgs webhookGroupAuthorizer, rbac sprintWorkspaceRoleChecker, projects webhookProjectWorkspaceResolver, enqueuer webhookDeliveryEnqueuer, emailer *EmailService, logger *zap.Logger) *WebhookService {
	return &WebhookService{repo: repo, orgs: orgs, rbac: rbac, projects: projects, enqueuer: enqueuer, emailer: emailer, logger: logger, client: &http.Client{Timeout: webhookDeliveryTimeout}}
}

func (s *WebhookService) authorizeGroup(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) error {
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

// authorizeWorkspace (S4W-14) -- AW-only, sama persis pola
// CustomStatusService.authorizeAdmin: platform_admin/group_admin bypass,
// selain itu wajib role admin_workspace di workspace ini.
func (s *WebhookService) authorizeWorkspace(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error {
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

func validateWebhookEvents(events []string) error {
	if len(events) == 0 {
		return fmt.Errorf("service.validateWebhookEvents: %w", domain.ErrWebhookEventRequired)
	}
	for _, e := range events {
		ok := false
		for _, supported := range SupportedWebhookEvents {
			if e == supported {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("service.validateWebhookEvents: %w", domain.ErrWebhookEventRequired)
		}
	}
	return nil
}

func generateWebhookSecret() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("service.generateWebhookSecret: %w", err)
	}
	return "whsec_" + hex.EncodeToString(buf), nil
}

// List -- daftar webhook grup untuk tab Endpoint (stats bar + grid).
func (s *WebhookService) List(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) ([]repository.Webhook, error) {
	if groupID == "" {
		return nil, fmt.Errorf("service.List: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return nil, err
	}
	list, err := s.repo.ListByGroup(ctx, exec, groupID)
	if err != nil {
		return nil, fmt.Errorf("service.List: %w", err)
	}
	return list, nil
}

// ListForWorkspace (S4W-14) -- sejajar List, tab Endpoint "AW Webhook.dc.html".
func (s *WebhookService) ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) ([]repository.Webhook, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.ListForWorkspace: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return nil, err
	}
	list, err := s.repo.ListByWorkspace(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.ListForWorkspace: %w", err)
	}
	return list, nil
}

// Create -- secret plaintext dikembalikan HANYA di sini, sekali, untuk
// ditampilkan modal (desain: "DITAMPILKAN SATU KALI"). orgID kosong berarti
// cakupan "Seluruh grup".
func (s *WebhookService) Create(ctx context.Context, exec db.Executor, groupID, orgID, name, targetURL string, events []string, actorID, actorRole string) (webhookID, secret string, err error) {
	name = strings.TrimSpace(name)
	targetURL = strings.TrimSpace(targetURL)
	if groupID == "" || name == "" {
		return "", "", fmt.Errorf("service.Create: %w", domain.ErrInvalidInput)
	}
	if !webhookURLPattern.MatchString(targetURL) {
		return "", "", fmt.Errorf("service.Create: %w", domain.ErrWebhookURLNotHTTPS)
	}
	if err := validateWebhookEvents(events); err != nil {
		return "", "", err
	}
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return "", "", err
	}
	var orgIDPtr *string
	if orgID != "" {
		if err := s.checkOrgBelongsToGroup(ctx, exec, orgID, groupID); err != nil {
			return "", "", err
		}
		orgIDPtr = &orgID
	}

	secret, err = generateWebhookSecret()
	if err != nil {
		return "", "", fmt.Errorf("service.Create: %w", err)
	}
	id, err := s.repo.Create(ctx, exec, groupID, "", orgIDPtr, nil, name, targetURL, secret, events, actorID, actorRole)
	if err != nil {
		return "", "", fmt.Errorf("service.Create: %w", err)
	}
	return id, secret, nil
}

// CreateForWorkspace (S4W-14) -- sejajar Create, LINGKUP "AW Add Webhook.
// dc.html": projectID kosong berarti "Seluruh workspace", terisi berarti
// "Project X" (divalidasi milik workspaceID yang sama).
func (s *WebhookService) CreateForWorkspace(ctx context.Context, exec db.Executor, workspaceID, projectID, name, targetURL string, events []string, actorID, actorRole string) (webhookID, secret string, err error) {
	name = strings.TrimSpace(name)
	targetURL = strings.TrimSpace(targetURL)
	if workspaceID == "" || name == "" {
		return "", "", fmt.Errorf("service.CreateForWorkspace: %w", domain.ErrInvalidInput)
	}
	if !webhookURLPattern.MatchString(targetURL) {
		return "", "", fmt.Errorf("service.CreateForWorkspace: %w", domain.ErrWebhookURLNotHTTPS)
	}
	if err := validateWebhookEvents(events); err != nil {
		return "", "", err
	}
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return "", "", err
	}
	var projectIDPtr *string
	if projectID != "" {
		if err := s.checkProjectBelongsToWorkspace(ctx, exec, projectID, workspaceID); err != nil {
			return "", "", err
		}
		projectIDPtr = &projectID
	}

	secret, err = generateWebhookSecret()
	if err != nil {
		return "", "", fmt.Errorf("service.CreateForWorkspace: %w", err)
	}
	id, err := s.repo.Create(ctx, exec, "", workspaceID, nil, projectIDPtr, name, targetURL, secret, events, actorID, actorRole)
	if err != nil {
		return "", "", fmt.Errorf("service.CreateForWorkspace: %w", err)
	}
	return id, secret, nil
}

func (s *WebhookService) checkOrgBelongsToGroup(ctx context.Context, exec db.Executor, orgID, groupID string) error {
	orgGroupID, err := s.orgs.GetGroupID(ctx, exec, orgID)
	if err != nil {
		return fmt.Errorf("service.checkOrgBelongsToGroup: %w", err)
	}
	if orgGroupID != groupID {
		return fmt.Errorf("service.checkOrgBelongsToGroup: %w", domain.ErrInvalidInput)
	}
	return nil
}

func (s *WebhookService) checkProjectBelongsToWorkspace(ctx context.Context, exec db.Executor, projectID, workspaceID string) error {
	projectWorkspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("service.checkProjectBelongsToWorkspace: %w", err)
	}
	if projectWorkspaceID != workspaceID {
		return fmt.Errorf("service.checkProjectBelongsToWorkspace: %w", domain.ErrInvalidInput)
	}
	return nil
}

// loadForMutation -- Get + otorisasi kepemilikan (grup ATAU workspace,
// tergantung cakupan baris), dipakai Update/ToggleActive/RegenerateSecret/
// Delete/Test supaya cek kepemilikan konsisten di satu tempat sebelum
// mutasi/pengiriman -- satu chokepoint untuk risiko cross-scope (S4W-14
// kickoff plan: "AW workspace A tidak lihat webhook workspace B").
func (s *WebhookService) loadForMutation(ctx context.Context, exec db.Executor, webhookID, actorID, actorRole string) (*repository.Webhook, error) {
	w, err := s.repo.Get(ctx, exec, webhookID)
	if err != nil {
		return nil, fmt.Errorf("service.loadForMutation: %w", err)
	}
	if w.WorkspaceID != nil {
		if err := s.authorizeWorkspace(ctx, exec, *w.WorkspaceID, actorID, actorRole); err != nil {
			return nil, err
		}
		return w, nil
	}
	if err := s.authorizeGroup(ctx, exec, *w.GroupID, actorID, actorRole); err != nil {
		return nil, err
	}
	return w, nil
}

func (s *WebhookService) Update(ctx context.Context, exec db.Executor, webhookID, orgID, name, targetURL string, events []string, actorID, actorRole string) error {
	name = strings.TrimSpace(name)
	targetURL = strings.TrimSpace(targetURL)
	if webhookID == "" || name == "" {
		return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
	}
	if !webhookURLPattern.MatchString(targetURL) {
		return fmt.Errorf("service.Update: %w", domain.ErrWebhookURLNotHTTPS)
	}
	if err := validateWebhookEvents(events); err != nil {
		return err
	}
	before, err := s.loadForMutation(ctx, exec, webhookID, actorID, actorRole)
	if err != nil {
		return err
	}
	var orgIDPtr *string
	if orgID != "" {
		if before.GroupID == nil {
			return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
		}
		if err := s.checkOrgBelongsToGroup(ctx, exec, orgID, *before.GroupID); err != nil {
			return err
		}
		orgIDPtr = &orgID
	}
	if err := s.repo.Update(ctx, exec, webhookID, name, targetURL, orgIDPtr, nil, events, actorID, actorRole, before); err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	return nil
}

// UpdateForWorkspace (S4W-14, dikonfirmasi user "tambahkan kelola juga
// seperti GA") -- sejajar Update, LINGKUP lewat projectID (bukan orgID).
// webhookID WAJIB baris cakupan workspace -- loadForMutation sendiri sudah
// menolak actor yang bukan pemilik lewat authorizeWorkspace, before.
// WorkspaceID nil berarti caller salah pakai method (baris ini group-scope).
func (s *WebhookService) UpdateForWorkspace(ctx context.Context, exec db.Executor, webhookID, projectID, name, targetURL string, events []string, actorID, actorRole string) error {
	name = strings.TrimSpace(name)
	targetURL = strings.TrimSpace(targetURL)
	if webhookID == "" || name == "" {
		return fmt.Errorf("service.UpdateForWorkspace: %w", domain.ErrInvalidInput)
	}
	if !webhookURLPattern.MatchString(targetURL) {
		return fmt.Errorf("service.UpdateForWorkspace: %w", domain.ErrWebhookURLNotHTTPS)
	}
	if err := validateWebhookEvents(events); err != nil {
		return err
	}
	before, err := s.loadForMutation(ctx, exec, webhookID, actorID, actorRole)
	if err != nil {
		return err
	}
	if before.WorkspaceID == nil {
		return fmt.Errorf("service.UpdateForWorkspace: %w", domain.ErrInvalidInput)
	}
	var projectIDPtr *string
	if projectID != "" {
		if err := s.checkProjectBelongsToWorkspace(ctx, exec, projectID, *before.WorkspaceID); err != nil {
			return err
		}
		projectIDPtr = &projectID
	}
	if err := s.repo.Update(ctx, exec, webhookID, name, targetURL, nil, projectIDPtr, events, actorID, actorRole, before); err != nil {
		return fmt.Errorf("service.UpdateForWorkspace: %w", err)
	}
	return nil
}

func (s *WebhookService) SetActive(ctx context.Context, exec db.Executor, webhookID string, active bool, actorID, actorRole string) error {
	before, err := s.loadForMutation(ctx, exec, webhookID, actorID, actorRole)
	if err != nil {
		return err
	}
	if err := s.repo.SetActive(ctx, exec, webhookID, active, actorID, actorRole, before); err != nil {
		return fmt.Errorf("service.SetActive: %w", err)
	}
	return nil
}

// RegenerateSecret -- secret baru dikembalikan sekali, sama pola Create.
func (s *WebhookService) RegenerateSecret(ctx context.Context, exec db.Executor, webhookID, actorID, actorRole string) (string, error) {
	before, err := s.loadForMutation(ctx, exec, webhookID, actorID, actorRole)
	if err != nil {
		return "", err
	}
	secret, err := generateWebhookSecret()
	if err != nil {
		return "", fmt.Errorf("service.RegenerateSecret: %w", err)
	}
	if err := s.repo.RegenerateSecret(ctx, exec, webhookID, secret, actorID, actorRole, before); err != nil {
		return "", fmt.Errorf("service.RegenerateSecret: %w", err)
	}
	return secret, nil
}

func (s *WebhookService) Delete(ctx context.Context, exec db.Executor, webhookID, actorID, actorRole string) error {
	before, err := s.loadForMutation(ctx, exec, webhookID, actorID, actorRole)
	if err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, exec, webhookID, actorID, actorRole, before); err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	return nil
}

// ListDeliveries -- tab Log Pengiriman.
func (s *WebhookService) ListDeliveries(ctx context.Context, exec db.Executor, groupID, statusFilter, webhookIDFilter, actorID, actorRole string) ([]repository.WebhookDelivery, error) {
	if groupID == "" {
		return nil, fmt.Errorf("service.ListDeliveries: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return nil, err
	}
	list, err := s.repo.ListDeliveries(ctx, exec, groupID, statusFilter, webhookIDFilter)
	if err != nil {
		return nil, fmt.Errorf("service.ListDeliveries: %w", err)
	}
	return list, nil
}

// ListDeliveriesForWorkspace (S4W-14) -- sejajar ListDeliveries, tab Log
// Pengiriman "AW Webhook.dc.html".
func (s *WebhookService) ListDeliveriesForWorkspace(ctx context.Context, exec db.Executor, workspaceID, statusFilter, webhookIDFilter, actorID, actorRole string) ([]repository.WebhookDelivery, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.ListDeliveriesForWorkspace: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return nil, err
	}
	list, err := s.repo.ListDeliveriesForWorkspace(ctx, exec, workspaceID, statusFilter, webhookIDFilter)
	if err != nil {
		return nil, fmt.Errorf("service.ListDeliveriesForWorkspace: %w", err)
	}
	return list, nil
}

// Test -- kirim payload contoh SINKRON (desain: "respons 200 OK dalam Xms"
// ditampilkan langsung, bukan lewat polling). attempt_number selalu 1 --
// independen dari rantai retry job async. Dipakai KEDUA cakupan (grup dan
// workspace) -- loadForMutation yang membedakan otorisasinya.
func (s *WebhookService) Test(ctx context.Context, exec db.Executor, webhookID, actorID, actorRole string) (durationMs int, delivered bool, err error) {
	w, err := s.loadForMutation(ctx, exec, webhookID, actorID, actorRole)
	if err != nil {
		return 0, false, err
	}
	payload, err := json.Marshal(map[string]any{
		"event":        "webhook.test",
		"delivered_at": time.Now().UTC().Format(time.RFC3339),
		"data":         map[string]string{"message": "Ini payload tes dari PRODO."},
	})
	if err != nil {
		return 0, false, fmt.Errorf("service.Test: %w", err)
	}
	return s.sendAndRecord(ctx, exec, w.ID, "webhook.test", payload, 1)
}

// Dispatch -- dipanggil ProjectService setelah Create/Update/Delete berhasil
// COMMIT (lihat pemanggil) -- mencari webhook aktif grup pemilik orgID DAN
// webhook aktif workspace pemilik workspaceID/projectID yang berlangganan
// eventType, lalu antre job pengiriman ASYNC per webhook (tidak memblokir/
// menggagalkan mutasi project kalau pengiriman gagal). Webhook grup dan
// webhook workspace boleh SAMA-SAMA terpicu untuk satu event yang sama --
// dua cakupan independen, bukan saling menggantikan (S4W-14).
func (s *WebhookService) Dispatch(ctx context.Context, exec db.Executor, orgID, workspaceID, projectID, eventType string, data map[string]any) error {
	var hooks []repository.Webhook

	groupID, err := s.orgs.GetGroupID(ctx, exec, orgID)
	if err != nil {
		return fmt.Errorf("service.Dispatch: %w", err)
	}
	groupHooks, err := s.repo.ListActiveForEvent(ctx, exec, groupID, orgID, eventType)
	if err != nil {
		return fmt.Errorf("service.Dispatch: %w", err)
	}
	hooks = append(hooks, groupHooks...)

	if workspaceID != "" {
		workspaceHooks, err := s.repo.ListActiveForEventWorkspace(ctx, exec, workspaceID, projectID, eventType)
		if err != nil {
			return fmt.Errorf("service.Dispatch: %w", err)
		}
		hooks = append(hooks, workspaceHooks...)
	}

	if len(hooks) == 0 {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"event":        eventType,
		"delivered_at": time.Now().UTC().Format(time.RFC3339),
		"data":         data,
	})
	if err != nil {
		return fmt.Errorf("service.Dispatch: %w", err)
	}
	for i := range hooks {
		if err := s.enqueuer.Enqueue(ctx, hooks[i].ID, eventType, payload); err != nil {
			return fmt.Errorf("service.Dispatch: enqueue webhook %s: %w", hooks[i].ID, err)
		}
	}
	return nil
}

// DeliverAttempt -- dipanggil worker Asynq (internal/worker/webhook_delivery.go)
// untuk SATU percobaan pengiriman. attemptNumber dari asynq.GetRetryCount+1.
// Mengembalikan error (untuk memicu retry Asynq) kalau pengiriman gagal DAN
// attemptNumber belum mencapai batas -- caller (worker) yang memutuskan itu,
// method ini hanya melaporkan sukses/gagal apa adanya.
func (s *WebhookService) DeliverAttempt(ctx context.Context, exec db.Executor, webhookID, eventType string, payload []byte, attemptNumber int) (delivered bool, err error) {
	_, delivered, err = s.sendAndRecord(ctx, exec, webhookID, eventType, payload, attemptNumber)
	return delivered, err
}

// sendAndRecord -- satu titik kirim+tandatangani+catat, dipakai Test (sinkron)
// dan DeliverAttempt (job async). err bukan null berarti kegagalan JARINGAN
// (dial/timeout) -- kegagalan HTTP non-2xx TETAP delivered=false tapi err=nil
// (baris sudah tercatat, bukan error infrastruktur).
func (s *WebhookService) sendAndRecord(ctx context.Context, exec db.Executor, webhookID, eventType string, payload []byte, attemptNumber int) (durationMs int, delivered bool, err error) {
	secret, err := s.repo.DecryptSecret(ctx, exec, webhookID)
	if err != nil {
		return 0, false, fmt.Errorf("service.sendAndRecord: %w", err)
	}
	w, err := s.repo.Get(ctx, exec, webhookID)
	if err != nil {
		return 0, false, fmt.Errorf("service.sendAndRecord: %w", err)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.TargetURL, strings.NewReader(string(payload)))
	if err != nil {
		return 0, false, fmt.Errorf("service.sendAndRecord: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Prodo-Event", eventType)
	req.Header.Set("X-Prodo-Signature", signature)

	start := time.Now()
	resp, sendErr := s.client.Do(req)
	duration := time.Since(start)
	durationMs = int(duration.Milliseconds())

	var httpStatus *int
	var responseBody, errMessage *string
	status := "failed"
	if sendErr != nil {
		msg := sendErr.Error()
		errMessage = &msg
	} else {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		bodyStr := string(body)
		responseBody = &bodyStr
		code := resp.StatusCode
		httpStatus = &code
		if code >= 200 && code < 300 {
			status = "delivered"
			delivered = true
		}
	}

	if recErr := s.repo.CreateDelivery(ctx, exec, webhookID, eventType, payload, attemptNumber, status, httpStatus, responseBody, errMessage, durationMs); recErr != nil {
		return durationMs, delivered, fmt.Errorf("service.sendAndRecord: catat delivery: %w", recErr)
	}
	return durationMs, delivered, nil
}

// NotifyExhausted -- email+in-app ke seluruh admin pemilik (Group Admin grup
// ATAU Admin Workspace workspace, tergantung cakupan) setelah 3x retry habis
// (desain: "Group Admin menerima notifikasi in-app dan email"), dipanggil
// worker. In-app lewat INSERT langsung ke notifications, pola PERSIS
// RetentionNotifyHandler (tidak ada NotificationRepository terpisah di
// codebase ini).
func (s *WebhookService) NotifyExhausted(ctx context.Context, exec db.Executor, webhookID, eventType string) error {
	w, err := s.repo.Get(ctx, exec, webhookID)
	if err != nil {
		return fmt.Errorf("service.NotifyExhausted: %w", err)
	}
	var admins []repository.GroupAdminContact
	if w.WorkspaceID != nil {
		admins, err = s.repo.WorkspaceAdminContacts(ctx, exec, *w.WorkspaceID)
	} else {
		admins, err = s.repo.GroupAdminContacts(ctx, exec, *w.GroupID)
	}
	if err != nil {
		return fmt.Errorf("service.NotifyExhausted: %w", err)
	}
	title := fmt.Sprintf("Webhook %q gagal terkirim", w.Name)
	body := fmt.Sprintf("Seluruh 3 percobaan pengiriman event %s ke %s gagal (1, 5, 15 menit). Periksa endpoint atau nonaktifkan webhook ini.", eventType, w.TargetURL)
	for _, a := range admins {
		if _, execErr := exec.Exec(ctx, `
			INSERT INTO notifications (user_id, actor_id, type, entity_type, entity_id, title, body)
			VALUES ($1, NULL, 'webhook_delivery_failed', 'webhook', $2, $3, $4)
		`, a.ID, webhookID, title, body); execErr != nil {
			return fmt.Errorf("service.NotifyExhausted: insert notification: %w", execErr)
		}
		if s.emailer != nil {
			if emailErr := s.emailer.SendWebhookFailureEmail(ctx, a.Email, a.Name, w.Name, w.TargetURL, eventType); emailErr != nil && s.logger != nil {
				s.logger.Error("gagal kirim email kegagalan webhook", zap.String("webhook_id", webhookID), zap.String("email", a.Email), zap.Error(emailErr))
			}
		}
	}
	return nil
}
