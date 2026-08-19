package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/push"
	"design-cloud-engine/internal/server/service/settings"
	"design-cloud-engine/internal/server/service/settings/schema"
)

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
	settingsSvc    *settings.Service
	settingsPusher contract.NodeSettingsPusher
}

// NewService creates a new node service
func NewService(
	nodeRepo *db.NodeRepo,
	logger *slog.Logger,
	cfg Config,
	auditSvc *audit.Service,
	publisher push.Publisher,
	collectTrigger contract.CollectTrigger,
	settingsSvc *settings.Service,
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
	}
}

// SetSettingsPusher sets the facade for pushing node settings updates to connected agents.
func (s *Service) SetSettingsPusher(p contract.NodeSettingsPusher) {
	s.settingsPusher = p
}

// TriggerCollect triggers an on-demand resource collection probe across the cluster.
func (s *Service) TriggerCollect(ctx context.Context) error {
	if s.collectTrigger == nil {
		return nil
	}
	return s.collectTrigger.TriggerCollect(ctx, contract.CollectTargetResource)
}

// CountByStatus returns the number of nodes grouped by status.
func (s *Service) CountByStatus(ctx context.Context) (map[model.NodeStatus]int64, error) {
	counts, err := s.nodeRepo.CountByStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("count nodes by status: %w", err)
	}
	return counts, nil
}

// LogCertRenew records a certificate renewal lifecycle event in the audit log
// (requested / issued / succeeded / failed / exhausted).
func (s *Service) LogCertRenew(ctx context.Context, nodeID, phase, message string) {
	s.auditSvc.LogSecurity(ctx, "NODE_CERT_RENEW", phase, slog.LevelInfo, map[string]any{
		"node_id": nodeID,
		"message": message,
	})
}

// GetResourceReport returns resource report for a specific node
func (s *Service) GetResourceReport(ctx context.Context, nodeID string) (model.NodeResourceReport, error) {
	report, err := s.nodeRepo.GetResourceReport(ctx, nodeID)
	if err != nil {
		return model.NodeResourceReport{}, fmt.Errorf("get resource report for node %q: %w", nodeID, err)
	}
	return report, nil
}

// Update updates mutable node fields (is_server, client_host, labels,
func (s *Service) Update(ctx context.Context, nodeID string, req UpdateNodeRequest) (*model.Node, error) {
	var err error
	nodeID, err = validateNodeID(nodeID)
	if err != nil {
		return nil, err
	}

	// Validate status if provided
	if req.Status != 0 {
		switch req.Status {
		case model.NodeStatusPending, model.NodeStatusOnline, model.NodeStatusOffline,
			model.NodeStatusMaintenance:
			// valid
		default:
			return nil, fmt.Errorf("%w: %d", ErrInvalidNodeStatus, req.Status)
		}
	}
	if req.ClientHost != nil {
		clientHost, err := normalizeClientHost(*req.ClientHost)
		if err != nil {
			return nil, err
		}
		req.ClientHost = &clientHost
	}

	// Fetch node
	node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return nil, ErrNodeNotFound
		}
		return nil, fmt.Errorf("get node %q for update: %w", nodeID, err)
	}

	// Apply only provided (non-nil/non-zero) fields
	if req.IsServer != nil {
		node.IsServer = *req.IsServer
	}
	if req.Status != 0 {
		node.Status = req.Status
	}
	if req.ClientHost != nil {
		node.ClientHost = *req.ClientHost
	}
	if req.Labels != nil {
		node.Labels = req.Labels
	}
	if req.AllowConnections != nil {
		node.AllowConnections = *req.AllowConnections
	}
	if req.MaxConnections != nil {
		node.MaxConnections = *req.MaxConnections
	}

	if err := s.nodeRepo.UpdateAllFields(ctx, &node); err != nil {
		return nil, fmt.Errorf("update node %q: %w", nodeID, err)
	}

	// 推送事件：通知系统管理员节点信息/状态变更
	s.publisher.PublishToRole(ctx, model.RoleSystemAdmin, push.Message{
		Name: push.EventNode,
		Payload: push.EventPayload{
			Action:   "refresh",
			TargetID: nodeID,
		},
	})

	// Clear token before returning
	node.Token = ""
	return &node, nil
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
		if errors.Is(err, common.ErrNotFound) {
			return nil, ErrNodeNotFound
		}
		return nil, fmt.Errorf("get node %q for agent config: %w", nodeID, err)
	}

	return &AgentConfig{
		ServerAddr: serverAddr,
		NodeID:     node.NodeID,
		Token:      node.Token,
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
	if nodeID == "" {
		return "", fmt.Errorf("%w: nodeID is required", ErrInvalidNodeID)
	}
	if strings.Contains(nodeID, "..") || strings.Contains(nodeID, "/") {
		return "", fmt.Errorf("%w: nodeID must not contain '..' or '/'", ErrInvalidNodeID)
	}
	return nodeID, nil
}

// GetView returns admin-facing node metadata for the given nodeID.
func (s *Service) GetView(ctx context.Context, nodeID string) (*model.Node, error) {
	node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return nil, ErrNodeNotFound
		}
		return nil, fmt.Errorf("get node %q for view: %w", nodeID, err)
	}
	return &node, nil
}

// ListViews returns admin-facing node metadata for all nodes.
func (s *Service) ListViews(ctx context.Context) ([]model.Node, error) {
	nodes, err := s.nodeRepo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	return nodes, nil
}

// GetNodeSettings retrieves the override and effective settings for a node.
func (s *Service) GetNodeSettings(ctx context.Context, nodeID string) (*schema.NodeSettingsView, error) {
	nodeID, err := validateNodeID(nodeID)
	if err != nil {
		return nil, err
	}
	node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
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
		effectiveAgent, effErr := s.settingsSvc.GetEffectiveAgentSettingsForNode(ctx, nodeID)
		if effErr != nil {
			return nil, fmt.Errorf("get effective agent settings for node %q: %w", nodeID, effErr)
		}
		effectiveAlert, effErr := s.settingsSvc.GetEffectiveAlertSettingsForNode(ctx, nodeID)
		if effErr != nil {
			return nil, fmt.Errorf("get effective alert settings for node %q: %w", nodeID, effErr)
		}
		effectiveLicense, effErr := s.settingsSvc.GetEffectiveLicenseSettingsForNode(ctx, nodeID)
		if effErr != nil {
			return nil, fmt.Errorf("get effective license settings for node %q: %w", nodeID, effErr)
		}
		effective = schema.NodeEffectiveSettings{
			Agent:   effectiveAgent,
			Alert:   effectiveAlert,
			License: effectiveLicense,
		}
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
		if errors.Is(err, common.ErrNotFound) {
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

	if err := s.nodeRepo.UpdateSettings(ctx, nodeID, settingsMap); err != nil {
		return nil, fmt.Errorf("update settings for node %q: %w", nodeID, err)
	}

	if s.settingsSvc != nil {
		s.settingsSvc.InvalidateNodeOverride(nodeID)
	}

	if s.settingsPusher != nil {
		common.SafeGo(s.logger, "push-node-settings", func() {
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
		return nil, common.NewValidationError("selector cannot be empty")
	}

	// 2. Validate selector status if specified
	var parsedStatuses []model.NodeStatus
	for _, st := range req.Selector.Status {
		parsed, err := model.ParseNodeStatus(st)
		if err != nil {
			return nil, common.NewValidationError(fmt.Sprintf("invalid status %q in selector", st))
		}
		parsedStatuses = append(parsedStatuses, parsed)
	}

	// 3. Patch cannot be empty
	trimmedPatch := bytes.TrimSpace(req.Patch)
	if len(trimmedPatch) == 0 {
		return nil, common.NewValidationError("patch cannot be empty")
	}

	// 4. Strict check that patch conforms to NodeSettings whitelist (rejects storage, unknown fields, malformed JSON)
	if _, err := schema.DecodeNodeSettingsPayload(trimmedPatch); err != nil {
		return nil, err
	}

	// 5. Query candidate nodes
	filter := db.NodeFilter{
		NodeIDs: req.Selector.NodeIDs,
		OS:      req.Selector.OS,
		Status:  parsedStatuses,
		Labels:  req.Selector.Labels,
	}
	nodes, err := s.nodeRepo.ListByFilter(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list nodes by filter: %w", err)
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

		// Canonical comparison to detect if settings actually changed
		var existingMap, mergedMap map[string]any
		if len(existingJSON) > 0 {
			_ = json.Unmarshal(existingJSON, &existingMap)
		}
		_ = json.Unmarshal(mergedJSON, &mergedMap)

		existingNorm, _ := json.Marshal(existingMap)
		mergedNorm, _ := json.Marshal(mergedMap)

		if bytes.Equal(existingNorm, mergedNorm) {
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

	// Invalidate cache and push to online nodes
	if !req.DryRun {
		for _, nodeID := range updatedNodeIDs {
			if s.settingsSvc != nil {
				s.settingsSvc.InvalidateNodeOverride(nodeID)
			}
			if s.settingsPusher != nil {
				targetNodeID := nodeID
				common.SafeGo(s.logger, "push-batch-node-settings", func() {
					pCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					if pushErr := s.settingsPusher.PushNodeSettingsUpdate(pCtx, targetNodeID); pushErr != nil {
						s.logger.Warn("Failed to push batch settings to node", "nodeID", targetNodeID, "error", pushErr)
					}
				})
			}
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
