package node

import (
	"bytes"
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/concurrency"
	"design-cloud-engine/internal/server/push"

	"gorm.io/gorm"
)

const defaultBatchSettingsPushWorkers = 16

// Config holds the narrow configuration the node service needs to derive
// cluster certificate paths. It intentionally does NOT carry the full
// *config.Config: the node service must remain decoupled from the config
// package to respect the architecture boundary rules.
type Config struct {
	NodeID  string
	CertDir string // <DataDir>/certs/cluster, resolved by the assembly layer
}

// Service provides node management logic
type Service struct {
	nodeRepo       *db.NodeRepo
	logger         *slog.Logger
	cfg            Config
	auditSvc       *audit.Service
	publisher      push.Publisher
	collectTrigger contract.CollectTrigger
	settingsSvc    SettingsCoordinator
	settingsPusher contract.NodeSettingsPusher
	connChecker    ConnectionChecker

	nodeCertLocks sync.Map // nodeID → *sync.Mutex; serializes cert file writes per node
}

// NewService creates a new node service.
// settingsPusher and connChecker are optional — nil means settings changes
// won't be pushed to agents and maintenance mode cannot check live connections,
// which is acceptable for unit tests.
func NewService(
	nodeRepo *db.NodeRepo,
	logger *slog.Logger,
	cfg Config,
	auditSvc *audit.Service,
	publisher push.Publisher,
	collectTrigger contract.CollectTrigger,
	settingsSvc SettingsCoordinator,
	settingsPusher contract.NodeSettingsPusher,
	connChecker ConnectionChecker,
) *Service {
	if publisher == nil {
		publisher = push.NoopPublisher{}
	}
	return &Service{
		nodeRepo:       nodeRepo,
		logger:         logger,
		cfg:            cfg,
		auditSvc:       auditSvc,
		publisher:      publisher,
		collectTrigger: collectTrigger,
		settingsSvc:    settingsSvc,
		settingsPusher: settingsPusher,
		connChecker:    connChecker,
	}
}

// TriggerCollect triggers an on-demand resource collection probe across the cluster.
func (s *Service) TriggerCollect(ctx context.Context) error {
	if s.collectTrigger == nil {
		return nil
	}
	return s.collectTrigger.TriggerCollect(ctx, contract.CollectTargetMetrics)
}

// CountRuntimeStatus returns enrolled node counts by runtime status.
func (s *Service) CountRuntimeStatus(ctx context.Context) (online, offline, maintenance int64, err error) {
	return s.nodeRepo.CountRuntimeByStatus(ctx)
}

const heartbeatFreshWindow = 2 * time.Minute

func (s *Service) resolveRuntimeStatusAfterMaintenance(ctx context.Context, node model.Node) model.NodeStatus {
	if !node.HasEnrolled() {
		return model.NodeStatusUnset
	}
	if s.connChecker != nil && s.connChecker.IsConnected(node.NodeID) {
		return model.NodeStatusOnline
	}
	if !node.LastHeartbeatAt.IsZero() && time.Since(node.LastHeartbeatAt) <= heartbeatFreshWindow {
		return model.NodeStatusOnline
	}
	return model.NodeStatusOffline
}

// LogCertRenew records a certificate renewal lifecycle event in the audit log
// (requested / issued / succeeded / failed / exhausted).
func (s *Service) LogCertRenew(ctx context.Context, nodeID, phase, message string) {
	s.auditSvc.LogSecurity(ctx, "NODE_CERT_RENEW", phase, slog.LevelInfo, map[string]any{
		"node_id": nodeID,
		"message": message,
	})
}

// GetRuntime returns the latest metrics snapshot for a specific node.
func (s *Service) GetRuntime(ctx context.Context, nodeID string) (model.NodeRuntime, error) {
	metrics, err := s.nodeRepo.GetRuntime(ctx, nodeID)
	if err != nil {
		return model.NodeRuntime{}, fmt.Errorf("get metrics for node %q: %w", nodeID, err)
	}
	return metrics, nil
}

// Update updates mutable admin fields and writes NODE_UPDATE when admin fields change.
func (s *Service) Update(ctx context.Context, nodeID string, req UpdateNodeRequest) (*NodeAdminView, error) {
	var err error
	nodeID, err = validateNodeID(nodeID)
	if err != nil {
		return nil, err
	}
	if req.DialHost != nil {
		dialHost, err := normalizeDialHost(*req.DialHost)
		if err != nil {
			return nil, err
		}
		req.DialHost = &dialHost
	}
	if req.Description != nil {
		if err := validateDescription(*req.Description); err != nil {
			return nil, err
		}
		trimmed := strings.TrimSpace(*req.Description)
		req.Description = &trimmed
	}
	replaceGroups := req.Groups != nil
	if replaceGroups {
		for _, g := range req.Groups {
			if err := model.ValidateNodeGroupName(g); err != nil {
				return nil, contract.NewValidationError(err.Error())
			}
		}
		req.Groups = model.NormalizeNodeGroups(req.Groups)
	}

	node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return nil, ErrNodeNotFound
		}
		return nil, fmt.Errorf("get node %q for update: %w", nodeID, err)
	}

	patch := db.NodeAdminPatch{}
	changedFields := make([]string, 0, 8)

	if req.IsServer != nil {
		patch.IsServer = req.IsServer
		changedFields = append(changedFields, "isServer")
	}
	if req.DialHost != nil {
		patch.DialHost = req.DialHost
		changedFields = append(changedFields, "dialHost")
	}
	if replaceGroups {
		patch.Groups = req.Groups
		patch.GroupsSet = true
		changedFields = append(changedFields, "groups")
	}
	if req.Description != nil {
		patch.Description = req.Description
		changedFields = append(changedFields, "description")
	}
	if req.AllowConnections != nil {
		patch.AllowConnections = req.AllowConnections
		changedFields = append(changedFields, "allowConnections")
	}
	if req.MaxConnections != nil {
		patch.MaxConnections = req.MaxConnections
		changedFields = append(changedFields, "maxConnections")
	}
	if req.Maintenance != nil {
		changedFields = append(changedFields, "maintenance")
		if *req.Maintenance {
			st := model.NodeStatusMaintenance
			patch.Status = &st
		} else {
			next := s.resolveRuntimeStatusAfterMaintenance(ctx, node)
			patch.Status = &next
		}
	}

	if len(changedFields) == 0 {
		view := BuildNodeAdminView(node)
		return &view, nil
	}

	if err := s.nodeRepo.UpdateAdminFields(ctx, nodeID, patch); err != nil {
		return nil, fmt.Errorf("update node %q: %w", nodeID, err)
	}

	s.auditSvc.LogNode(ctx, "NODE_UPDATE", "success", nodeID, slog.LevelInfo, map[string]any{
		"fields": changedFields,
	})

	s.publisher.PublishToRole(ctx, model.RoleSystemAdmin, push.Message{
		Name: push.EventNode,
		Payload: push.EventPayload{
			Action:   "refresh",
			TargetID: nodeID,
		},
	})

	updated, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	view := BuildNodeAdminView(updated)
	return &view, nil
}

// AdminBatchUpdate sparsely updates existing nodes; does not create.
// Empty items or more than 100 return InvalidParamError for the whole request.
// Missing nodes and per-item validation errors go into failures; items with no
// mutable fields are skipped (not counted in updated, not failures).
func (s *Service) AdminBatchUpdate(ctx context.Context, items []BatchNodeUpdateItem) (*BatchNodeUpdateResponse, error) {
	if len(items) == 0 {
		return nil, contract.NewInvalidParamError("items is required")
	}
	if len(items) > maxNodeBatchItems {
		return nil, contract.NewInvalidParamError(fmt.Sprintf("items exceeds limit of %d", maxNodeBatchItems))
	}
	out := &BatchNodeUpdateResponse{}
	for i := range items {
		item := items[i]
		if !item.hasFields() {
			continue
		}
		if _, err := s.Update(ctx, item.NodeID, item.toUpdateRequest()); err != nil {
			out.Failures = append(out.Failures, BatchNodeUpdateFailure{
				Index:   i,
				NodeID:  item.NodeID,
				Message: err.Error(),
			})
			continue
		}
		out.Updated++
	}
	return out, nil
}

// GetAgentConfig returns the agent configuration for a node.
// Returns an error if the node is not found or if it does not have the agent role.
func (s *Service) GetAgentConfig(ctx context.Context, nodeID, serverAddr string) (*AgentConfig, error) {
	var err error
	nodeID, err = validateNodeID(nodeID)
	if err != nil {
		return nil, err
	}
	node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return nil, ErrNodeNotFound
		}
		return nil, fmt.Errorf("get node %q for agent config: %w", nodeID, err)
	}

	return &AgentConfig{
		ServerAddr: serverAddr,
		NodeID:     node.NodeID,
		CertPath:   s.clusterNodeCertPath(node.NodeID),
		KeyPath:    s.clusterNodeKeyPath(node.NodeID),
		CAPath:     s.clusterCACertPath(),
	}, nil
}

// clusterCACertPath returns the path to the cluster CA certificate (flat file
// under certs/cluster, shared by every node).
func (s *Service) clusterCACertPath() string {
	return filepath.Join(s.cfg.CertDir, "ca.crt")
}

// clusterCAKeyPath returns the path to the cluster CA key (nested private dir).
func (s *Service) clusterCAKeyPath() string {
	return filepath.Join(s.cfg.CertDir, "ca", "ca.key")
}

// clusterNodeCertPath returns the certificate path for a specific node:
// <CertDir>/<nodeID>/<nodeID>.crt.
func (s *Service) clusterNodeCertPath(nodeID string) string {
	return filepath.Join(s.cfg.CertDir, nodeID, fmt.Sprintf("%s.crt", nodeID))
}

// clusterNodeKeyPath returns the key path for a specific node:
// <CertDir>/<nodeID>/<nodeID>.key.
func (s *Service) clusterNodeKeyPath(nodeID string) string {
	return filepath.Join(s.cfg.CertDir, nodeID, fmt.Sprintf("%s.key", nodeID))
}

func validateNodeID(nodeID string) (string, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return "", fmt.Errorf("%w: nodeID is required", ErrInvalidNodeID)
	}
	if err := model.ValidateNodeID(nodeID); err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidNodeID, err.Error())
	}
	return nodeID, nil
}

// GetView returns admin-facing node metadata for the given nodeID.
func (s *Service) GetView(ctx context.Context, nodeID string) (*NodeAdminView, error) {
	node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return nil, ErrNodeNotFound
		}
		return nil, fmt.Errorf("get node %q for view: %w", nodeID, err)
	}
	view := BuildNodeAdminView(node)
	return &view, nil
}

// ListViews returns admin-facing node metadata for all nodes.
func (s *Service) ListViews(ctx context.Context) ([]NodeAdminView, error) {
	nodes, err := s.nodeRepo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	out := make([]NodeAdminView, len(nodes))
	for i, n := range nodes {
		out[i] = BuildNodeAdminView(n)
	}
	return out, nil
}

// ListViewsPage returns admin-facing node metadata with limit/offset pagination.
func (s *Service) ListViewsPage(ctx context.Context, groups []string, limit, offset int) ([]NodeAdminView, int64, error) {
	var filter db.NodeFilter
	if len(groups) > 0 {
		for _, g := range groups {
			if err := model.ValidateNodeGroupName(g); err != nil {
				return nil, 0, contract.NewValidationError(err.Error())
			}
		}
		filter.Groups = groups
	}
	nodes, total, err := s.nodeRepo.ListPage(ctx, filter, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list nodes: %w", err)
	}
	out := make([]NodeAdminView, len(nodes))
	for i, n := range nodes {
		out[i] = BuildNodeAdminView(n)
	}
	return out, total, nil
}

// GetNodeSettings retrieves the override and effective settings for a node.
func (s *Service) GetNodeSettings(ctx context.Context, nodeID string) (*schema.NodeSettingsView, error) {
	nodeID, err := validateNodeID(nodeID)
	if err != nil {
		return nil, err
	}
	node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return nil, ErrNodeNotFound
		}
		return nil, fmt.Errorf("get node %q for settings: %w", nodeID, err)
	}

	var override *schema.NodeSettings
	if len(node.Settings) > 0 {
		var decErr error
		override, decErr = schema.DecodeNodeSettingsFromMap(node.Settings)
		if decErr != nil {
			return nil, fmt.Errorf("decode settings for node %q: %w", nodeID, decErr)
		}
	}

	var effective schema.NodeEffectiveSettings
	if s.settingsSvc != nil {
		eff, effErr := s.settingsSvc.BuildNodeEffectiveSettings(ctx, nodeID, override, true)
		if effErr != nil {
			return nil, fmt.Errorf("build effective settings for node %q: %w", nodeID, effErr)
		}
		effective = eff
	}

	return &schema.NodeSettingsView{
		Override:  override,
		Effective: &effective,
	}, nil
}

// UpdateNodeSettings validates and updates the override settings for a node using partial patch semantics,
// and pushes the update to the node if online.
func (s *Service) UpdateNodeSettings(ctx context.Context, nodeID string, patchRaw []byte) (*schema.NodeSettingsView, error) {
	nodeID, err := validateNodeID(nodeID)
	if err != nil {
		return nil, err
	}
	node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return nil, ErrNodeNotFound
		}
		return nil, fmt.Errorf("get node %q before update settings: %w", nodeID, err)
	}

	var existingJSON []byte
	if len(node.Settings) > 0 {
		var mErr error
		existingJSON, mErr = json.Marshal(node.Settings)
		if mErr != nil {
			return nil, fmt.Errorf("marshal existing settings for node %q: %w", nodeID, mErr)
		}
	}

	mergedJSON, err := schema.MergeNodeSettingsPatch(existingJSON, patchRaw)
	if err != nil {
		return nil, err
	}

	var settingsMap map[string]any
	if err := json.Unmarshal(mergedJSON, &settingsMap); err != nil {
		return nil, fmt.Errorf("unmarshal merged node settings: %w", err)
	}

	if err := s.nodeRepo.UpdateSettingsInTx(ctx, nodeID, settingsMap, func(tx *gorm.DB) error {
		if s.settingsSvc == nil {
			return nil
		}
		return s.settingsSvc.BumpNodeOverridesTopicTx(ctx, tx)
	}); err != nil {
		return nil, fmt.Errorf("update settings for node %q: %w", nodeID, err)
	}

	if s.settingsSvc != nil {
		s.settingsSvc.InvalidateNodeOverride(nodeID)
	}

	if s.settingsPusher != nil {
		concurrency.SafeGo(s.logger, "push-node-settings", func() {
			pCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if pushErr := s.settingsPusher.PushNodeSettingsUpdate(pCtx, nodeID); pushErr != nil {
				s.logger.Warn("Failed to push updated settings to node", "nodeID", nodeID, "error", pushErr)
			}
		})
	} else {
		s.logger.Debug("No settings pusher configured, skipping push", "nodeID", nodeID)
	}

	return s.GetNodeSettings(ctx, nodeID)
}

// BatchUpdateNodeSettings updates override settings across multiple nodes matching a selector.
func (s *Service) BatchUpdateNodeSettings(ctx context.Context, req BatchUpdateNodeSettingsRequest) (*BatchNodeSettingsResponse, error) {
	// 1. Selector cannot be empty
	if req.Selector == nil || req.Selector.IsEmpty() {
		return nil, contract.NewValidationError("selector cannot be empty")
	}

	// 5. Resolve candidate nodes (group filter before pagination elsewhere)
	nodeIDs, err := s.ResolveSelectorNodeIDs(ctx, req.Selector)
	if err != nil {
		return nil, err
	}
	var nodes []model.Node
	if len(nodeIDs) > 0 {
		nodes, err = s.nodeRepo.ListByNodeIDs(ctx, nodeIDs)
		if err != nil {
			return nil, fmt.Errorf("list nodes by ids: %w", err)
		}
	}

	trimmedPatch := bytes.TrimSpace(req.Patch)
	if len(trimmedPatch) == 0 {
		return nil, contract.NewValidationError("patch cannot be empty")
	}

	if _, err := schema.DecodeNodeSettingsPayload(trimmedPatch); err != nil {
		return nil, err
	}

	resp := &BatchNodeSettingsResponse{
		Matched:  len(nodes),
		DryRun:   req.DryRun,
		Failures: make([]BatchNodeFailure, 0),
	}

	if len(nodes) == 0 {
		if s.auditSvc != nil {
			s.auditSvc.LogSystem(ctx, "NODE_SETTINGS_BATCH", slog.LevelInfo, map[string]any{
				"selector": req.Selector,
				"dryRun":   req.DryRun,
				"matched":  0,
				"updated":  0,
			})
		}
		return resp, nil
	}

	var updatedNodeIDs []string

	for _, n := range nodes {
		var existingJSON []byte
		if len(n.Settings) > 0 {
			var mErr error
			existingJSON, mErr = json.Marshal(n.Settings)
			if mErr != nil {
				resp.Failures = append(resp.Failures, BatchNodeFailure{
					NodeID:  n.NodeID,
					Message: fmt.Sprintf("marshal existing settings: %v", mErr),
				})
				continue
			}
		}

		mergedJSON, err := schema.MergeNodeSettingsPatch(existingJSON, trimmedPatch)
		if err != nil {
			resp.Failures = append(resp.Failures, BatchNodeFailure{
				NodeID:  n.NodeID,
				Message: err.Error(),
			})
			continue
		}

		var mergedMap map[string]any
		if err := json.Unmarshal(mergedJSON, &mergedMap); err != nil {
			resp.Failures = append(resp.Failures, BatchNodeFailure{
				NodeID:  n.NodeID,
				Message: fmt.Sprintf("unmarshal merged settings: %v", err),
			})
			continue
		}
		if settingsMapsEqual(n.Settings, mergedMap) {
			resp.Unchanged++
			continue
		}

		resp.Updated++

		if !req.DryRun {
			if err := s.nodeRepo.UpdateSettings(ctx, n.NodeID, mergedMap); err != nil {
				resp.Failures = append(resp.Failures, BatchNodeFailure{
					NodeID:  n.NodeID,
					Message: fmt.Sprintf("db update settings: %v", err),
				})
				resp.Updated--
				continue
			}
			updatedNodeIDs = append(updatedNodeIDs, n.NodeID)
		}
	}

	// Invalidate cache and push to online nodes (bounded worker pool for pushes).
	// Invalidate local cache, bump node_overrides once, then push. Bump failure must not
	// undo successful DB writes or skip Agent push (TTL still bounds cross-Portal lag).
	if !req.DryRun {
		if s.settingsSvc != nil && len(updatedNodeIDs) > 0 {
			for _, nodeID := range updatedNodeIDs {
				s.settingsSvc.InvalidateNodeOverride(nodeID)
			}
			if err := s.settingsSvc.BumpNodeOverridesTopic(ctx); err != nil {
				s.logger.Warn("Failed to bump node_overrides after batch settings update",
					"error", err, "updated", len(updatedNodeIDs))
			}
		}
		if len(updatedNodeIDs) > 0 {
			s.pushBatchNodeSettings(updatedNodeIDs)
		}
	}

	// Audit logging
	if s.auditSvc != nil {
		s.auditSvc.LogSystem(ctx, "NODE_SETTINGS_BATCH", slog.LevelInfo, map[string]any{
			"selector":  req.Selector,
			"dryRun":    req.DryRun,
			"matched":   resp.Matched,
			"updated":   resp.Updated,
			"unchanged": resp.Unchanged,
			"failures":  len(resp.Failures),
			"patch":     string(trimmedPatch),
		})
	}

	return resp, nil
}

// settingsMapsEqual compares node settings maps; nil and empty both count as empty.
func settingsMapsEqual(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func (s *Service) pushBatchNodeSettings(nodeIDs []string) {
	if s.settingsPusher == nil || len(nodeIDs) == 0 {
		return
	}
	jobs := make(chan string, len(nodeIDs))
	for _, id := range nodeIDs {
		jobs <- id
	}
	close(jobs)

	workers := min(defaultBatchSettingsPushWorkers, len(nodeIDs))
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			defer concurrency.SafeRecover(s.logger, "push-batch-node-settings-pool")()
			for nodeID := range jobs {
				pCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				pushErr := s.settingsPusher.PushNodeSettingsUpdate(pCtx, nodeID)
				cancel()
				if pushErr != nil {
					s.logger.Warn("Failed to push batch settings to node", "nodeID", nodeID, "error", pushErr)
				}
			}
		})
	}
	wg.Wait()
}
