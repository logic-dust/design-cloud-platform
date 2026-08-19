package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type NodeRepo struct {
	store *Store
}

func (r *NodeRepo) Create(ctx context.Context, node *model.Node) error {
	return r.store.db.WithContext(ctx).Create(node).Error
}

func (r *NodeRepo) UpdateAllFields(ctx context.Context, node *model.Node) error {
	return r.store.db.WithContext(ctx).Model(node).Omit("Settings").Select("*").Updates(node).Error
}

// UpdateSettings updates the node_settings JSON column for a node atomically.
// Uses Select("Settings").Updates to ensure GORM triggers the serializer:json interceptor.
func (r *NodeRepo) UpdateSettings(ctx context.Context, nodeID string, settings map[string]any) error {
	return r.store.db.WithContext(ctx).
		Model(&model.Node{}).
		Where("node_id = ?", nodeID).
		Select("Settings").
		Updates(&model.Node{Settings: settings}).Error
}

func (r *NodeRepo) GetByNodeID(ctx context.Context, nodeID string) (model.Node, error) {
	var node model.Node
	if err := r.store.db.WithContext(ctx).Where("node_id = ?", nodeID).First(&node).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Node{}, fmt.Errorf("node %q: %w", nodeID, common.ErrNotFound)
		}
		return model.Node{}, err
	}
	return node, nil
}

// ListByNodeIDs returns all nodes whose node_id is in the given slice in a single
// DB round-trip. The result order is not guaranteed to match the input order.
// An empty input returns a nil slice without hitting the database.
func (r *NodeRepo) ListByNodeIDs(ctx context.Context, nodeIDs []string) ([]model.Node, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var nodes []model.Node
	if err := r.store.db.WithContext(ctx).Where("node_id IN ?", nodeIDs).Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

func (r *NodeRepo) List(ctx context.Context) ([]model.Node, error) {
	var nodes []model.Node
	if err := r.store.db.WithContext(ctx).Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

// NodeFilter specifies search criteria for filtering nodes.
type NodeFilter struct {
	NodeIDs []string
	OS      []string
	Status  []model.NodeStatus
	Labels  map[string]string
}

// ListByFilter returns nodes matching the specified selector criteria.
// In-database filtering handles NodeIDs, OS, and Status; in-memory filtering handles Labels.
func (r *NodeRepo) ListByFilter(ctx context.Context, filter NodeFilter) ([]model.Node, error) {
	query := r.store.db.WithContext(ctx).Model(&model.Node{})
	if len(filter.NodeIDs) > 0 {
		query = query.Where("node_id IN ?", filter.NodeIDs)
	}
	if len(filter.OS) > 0 {
		query = query.Where("os IN ?", filter.OS)
	}
	if len(filter.Status) > 0 {
		query = query.Where("status IN ?", filter.Status)
	}

	var nodes []model.Node
	if err := query.Order("node_id ASC").Find(&nodes).Error; err != nil {
		return nil, err
	}

	if len(filter.Labels) == 0 {
		return nodes, nil
	}

	filtered := make([]model.Node, 0, len(nodes))
	for _, n := range nodes {
		match := true
		for k, v := range filter.Labels {
			if n.Labels == nil || n.Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			filtered = append(filtered, n)
		}
	}
	return filtered, nil
}

// CountByStatus returns the number of nodes grouped by status.
func (r *NodeRepo) CountByStatus(ctx context.Context) (map[model.NodeStatus]int64, error) {
	type statusCount struct {
		Status model.NodeStatus
		Count  int64
	}
	var rows []statusCount
	if err := r.store.db.WithContext(ctx).Model(&model.Node{}).
		Select("status, count(*) as count").
		Group("status").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[model.NodeStatus]int64, len(rows))
	for _, r := range rows {
		result[r.Status] = r.Count
	}
	return result, nil
}

func (r *NodeRepo) MarkOnline(ctx context.Context, nodeID, serverID string) error {
	return r.applyHeartbeat(ctx, nodeID, serverID, true)
}

// SetNodeOS records the operating system reported by the agent at registration
// (e.g. "windows" / "linux"), so the Portal can tell which actions a node supports.
func (r *NodeRepo) SetNodeOS(ctx context.Context, nodeID, os string) error {
	if nodeID == "" || os == "" {
		return nil
	}
	return r.store.db.WithContext(ctx).
		Model(&model.Node{}).
		Where("node_id = ?", nodeID).
		Update("os", os).Error
}

func (r *NodeRepo) TouchHeartbeat(ctx context.Context, nodeID, serverID string) error {
	return r.applyHeartbeat(ctx, nodeID, serverID, false)
}

// applyHeartbeat updates status, last_heartbeat_at and last_seen_server for a node.
// When setConnectedAt is true, also records the current time as connected_at (used on first connect).
// Does not override maintenance or disabled status.
func (r *NodeRepo) applyHeartbeat(ctx context.Context, nodeID, serverID string, setConnectedAt bool) error {
	if nodeID == "" {
		return nil
	}

	now := time.Now()
	updates := map[string]any{
		"status":            model.NodeStatusOnline,
		"last_heartbeat_at": now,
		"last_seen_server":  serverID,
	}
	if setConnectedAt {
		updates["connected_at"] = now
	}

	return r.store.db.WithContext(ctx).Model(&model.Node{}).Where(
		"node_id = ? AND status <> ?",
		nodeID, model.NodeStatusMaintenance,
	).Updates(updates).Error
}

func (r *NodeRepo) MarkDisconnected(ctx context.Context, nodeID, serverID string) error {
	if nodeID == "" {
		return nil
	}

	updates := map[string]any{
		"status":          model.NodeStatusOffline,
		"disconnected_at": time.Now(),
	}
	if serverID != "" {
		updates["last_seen_server"] = serverID
	}
	return r.store.db.WithContext(ctx).Model(&model.Node{}).Where(
		"node_id = ? AND status <> ?",
		nodeID, model.NodeStatusMaintenance,
	).Updates(updates).Error
}

func (r *NodeRepo) MarkStaleOffline(ctx context.Context, staleBefore time.Time) ([]string, error) {
	var staledNodes []string
	err := r.store.db.WithContext(ctx).Model(&model.Node{}).
		Where("status = ? AND last_heartbeat_at < ?", model.NodeStatusOnline, staleBefore).
		Pluck("node_id", &staledNodes).Error
	if err != nil {
		return nil, err
	}
	if len(staledNodes) == 0 {
		return nil, nil
	}

	result := r.store.db.WithContext(ctx).Model(&model.Node{}).
		Where("node_id IN ?", staledNodes).
		Update("status", model.NodeStatusOffline)

	if result.Error != nil {
		return nil, result.Error
	}

	return staledNodes, nil
}

func (r *NodeRepo) ResetOnlineToOffline(ctx context.Context) (int64, error) {
	result := r.store.db.WithContext(ctx).Model(&model.Node{}).
		Where("status = ?", model.NodeStatusOnline).
		Update("status", model.NodeStatusOffline)
	return result.RowsAffected, result.Error
}

// UpsertSelf creates or updates the calling server node's own record in the nodes table.
// Other server nodes register themselves when they start.
// fingerprint is the SHA256 of the node's own certificate, written so the local
// agent can pass gRPC identity validation without the OTP bootstrap flow.
func (r *NodeRepo) UpsertSelf(ctx context.Context, nodeID string, fingerprint string) error {
	if nodeID == "" {
		return nil
	}
	return r.store.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "node_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"is_server", "cert_fingerprint"}),
	}).Create(&model.Node{
		NodeID:          nodeID,
		IsServer:        true,
		CertFingerprint: fingerprint,
		Status:          model.NodeStatusOffline,
	}).Error
}

// ListCertExpiring returns nodes whose certificate expires before the given time.
// Nodes without an issued certificate (zero value) are skipped.
func (r *NodeRepo) ListCertExpiring(ctx context.Context, before time.Time) ([]model.Node, error) {
	var nodes []model.Node
	err := r.store.db.WithContext(ctx).
		Where("cert_expires_at < ? AND cert_expires_at > ?", before, time.Time{}).
		Find(&nodes).Error
	return nodes, err
}

// UpdateCertAlertStage records the highest emitted expiry alert stage for a node
// (0=none, 90/30/7 days, 1=expired) so duplicate alerts are suppressed.
func (r *NodeRepo) UpdateCertAlertStage(ctx context.Context, nodeID string, stage int16) error {
	return r.store.db.WithContext(ctx).Model(&model.Node{}).
		Where("node_id = ?", nodeID).
		Update("cert_alert_stage", stage).Error
}

// UpdateCertMetadata refreshes only the bookkeeping metadata (subject/expiry) of a
// node certificate; fingerprints are never self-healed (D11 fail-closed rule).
func (r *NodeRepo) UpdateCertMetadata(ctx context.Context, nodeID, subject string, expiresAt time.Time) error {
	if expiresAt.IsZero() {
		return nil
	}
	return r.store.db.WithContext(ctx).Model(&model.Node{}).
		Where("node_id = ?", nodeID).
		Updates(map[string]any{"cert_subject": subject, "cert_expires_at": expiresAt}).Error
}
