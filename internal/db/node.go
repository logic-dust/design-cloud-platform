package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"design-cloud-engine/internal/model"

	"design-cloud-engine/internal/contract"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type NodeRepo struct {
	store *Store
}

func (r *NodeRepo) Create(ctx context.Context, node *model.Node) error {
	if err := r.store.db.WithContext(ctx).Create(node).Error; err != nil {
		return err
	}
	if node.Online || !node.LastHeartbeatAt.IsZero() || !node.LastConnectedAt.IsZero() || !node.LastDisconnectedAt.IsZero() {
		lastConnected := node.LastConnectedAt
		if node.Online && lastConnected.IsZero() {
			lastConnected = time.Now()
			node.LastConnectedAt = lastConnected
		}
		liveness := model.NodeLiveness{
			NodeID:             node.NodeID,
			Online:             node.Online,
			LastHeartbeatAt:    node.LastHeartbeatAt,
			LastConnectedAt:    lastConnected,
			LastDisconnectedAt: node.LastDisconnectedAt,
		}
		return r.store.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "node_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"online", "last_heartbeat_at", "last_connected_at", "last_disconnected_at",
			}),
		}).Create(&liveness).Error
	}
	return nil
}

type NodeAdminPatch struct {
	IsServer            *bool
	DialHost            *string
	Groups              []string
	GroupsSet           bool
	Description         *string
	InMaintenance       *bool
	ConnectionsDisabled *bool
	MaxConnections      *uint
}

func (r *NodeRepo) UpdateAdminFields(ctx context.Context, nodeID string, patch NodeAdminPatch) error {
	updates := map[string]any{}
	if patch.IsServer != nil {
		updates["is_server"] = *patch.IsServer
	}
	if patch.DialHost != nil {
		updates["dial_host"] = *patch.DialHost
	}
	if patch.GroupsSet {
		stored := patch.Groups
		if stored == nil {
			stored = []string{}
		}
		if err := r.store.db.WithContext(ctx).Model(&model.Node{}).Where("node_id = ?", nodeID).
			Select("Groups").
			Updates(&model.Node{Groups: stored}).Error; err != nil {
			return err
		}
	}
	if patch.Description != nil {
		updates["description"] = *patch.Description
	}
	if patch.InMaintenance != nil {
		updates["in_maintenance"] = *patch.InMaintenance
	}
	if patch.ConnectionsDisabled != nil {
		updates["connections_disabled"] = *patch.ConnectionsDisabled
	}
	if patch.MaxConnections != nil {
		updates["max_connections"] = *patch.MaxConnections
	}
	if len(updates) == 0 {
		return nil
	}
	return r.store.db.WithContext(ctx).Model(&model.Node{}).Where("node_id = ?", nodeID).Updates(updates).Error
}

type NodeCertPatch struct {
	CertFingerprint     string
	CertFingerprintPrev string
	CertSubject         string
	CertExpiresAt       time.Time
	CertAlertStage      *int16
	ClearCert           bool
}

func (r *NodeRepo) UpdateCertFields(ctx context.Context, nodeID string, patch NodeCertPatch) error {
	updates := map[string]any{}
	if patch.ClearCert {
		updates["cert_fingerprint"] = ""
		updates["cert_fingerprint_prev"] = ""
		updates["cert_subject"] = ""
		updates["cert_expires_at"] = time.Time{}
		updates["cert_alert_stage"] = int16(0)
	} else {
		if patch.CertFingerprint != "" {
			updates["cert_fingerprint"] = patch.CertFingerprint
		}
		if patch.CertFingerprintPrev != "" {
			updates["cert_fingerprint_prev"] = patch.CertFingerprintPrev
		}
		if patch.CertSubject != "" {
			updates["cert_subject"] = patch.CertSubject
		}
		if !patch.CertExpiresAt.IsZero() {
			updates["cert_expires_at"] = patch.CertExpiresAt
		}
		if patch.CertAlertStage != nil {
			updates["cert_alert_stage"] = *patch.CertAlertStage
		}
	}
	if len(updates) == 0 {
		return nil
	}
	return r.store.db.WithContext(ctx).Model(&model.Node{}).Where("node_id = ?", nodeID).Updates(updates).Error
}

func (r *NodeRepo) SetEnrollmentToken(ctx context.Context, nodeID, hash string, expiresAt time.Time) error {
	return r.store.db.WithContext(ctx).Model(&model.Node{}).Where("node_id = ?", nodeID).Updates(map[string]any{
		"enrollment_token_hash":       hash,
		"enrollment_token_expires_at": expiresAt,
	}).Error
}

// ConsumeEnrollmentToken clears the hash when nodeID, hash, and expiry match (atomic).
func (r *NodeRepo) ConsumeEnrollmentToken(ctx context.Context, nodeID, hash string, now time.Time) (bool, error) {
	result := r.store.db.WithContext(ctx).Model(&model.Node{}).
		Where("node_id = ? AND enrollment_token_hash = ? AND enrollment_token_hash <> '' AND enrollment_token_expires_at > ?",
			nodeID, hash, now).
		Updates(map[string]any{
			"enrollment_token_hash":       "",
			"enrollment_token_expires_at": time.Time{},
		})
	return result.RowsAffected == 1, result.Error
}

// UpdateSettings updates the settings JSON column for a node.
func (r *NodeRepo) UpdateSettings(ctx context.Context, nodeID string, settings map[string]any) error {
	return r.UpdateSettingsInTx(ctx, nodeID, settings, nil)
}

// UpdateSettingsInTx updates Settings inside a transaction. afterTx runs in the same
// transaction after the row update (e.g. cache_versions BumpTx); nil skips wrapping
// when no post-update work is needed beyond the update itself.
func (r *NodeRepo) UpdateSettingsInTx(ctx context.Context, nodeID string, settings map[string]any, afterTx func(tx *gorm.DB) error) error {
	apply := func(tx *gorm.DB) error {
		if err := tx.Model(&model.Node{}).
			Where("node_id = ?", nodeID).
			Select("Settings").
			Updates(&model.Node{Settings: settings}).Error; err != nil {
			return err
		}
		if afterTx != nil {
			return afterTx(tx)
		}
		return nil
	}
	if afterTx == nil {
		return apply(r.store.db.WithContext(ctx))
	}
	return r.store.db.WithContext(ctx).Transaction(apply)
}

func (r *NodeRepo) hydrateLiveness(ctx context.Context, nodes []model.Node) error {
	if len(nodes) == 0 {
		return nil
	}
	ids := make([]string, len(nodes))
	for i := range nodes {
		ids[i] = nodes[i].NodeID
	}
	var rows []model.NodeLiveness
	if err := r.store.db.WithContext(ctx).Where("node_id IN ?", ids).Find(&rows).Error; err != nil {
		return err
	}
	byID := make(map[string]model.NodeLiveness, len(rows))
	for _, row := range rows {
		byID[row.NodeID] = row
	}
	for i := range nodes {
		if l, ok := byID[nodes[i].NodeID]; ok {
			nodes[i].Online = l.Online
			nodes[i].LastHeartbeatAt = l.LastHeartbeatAt
			nodes[i].LastConnectedAt = l.LastConnectedAt
			nodes[i].LastDisconnectedAt = l.LastDisconnectedAt
		}
	}
	return nil
}

func (r *NodeRepo) GetByNodeID(ctx context.Context, nodeID string) (model.Node, error) {
	var node model.Node
	if err := r.store.db.WithContext(ctx).Where("node_id = ?", nodeID).Take(&node).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Node{}, fmt.Errorf("node %q: %w", nodeID, contract.ErrNotFound)
		}
		return model.Node{}, err
	}
	one := []model.Node{node}
	if err := r.hydrateLiveness(ctx, one); err != nil {
		return model.Node{}, err
	}
	return one[0], nil
}

func (r *NodeRepo) ListByNodeIDs(ctx context.Context, nodeIDs []string) ([]model.Node, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var nodes []model.Node
	if err := r.store.db.WithContext(ctx).Where("node_id IN ?", nodeIDs).Find(&nodes).Error; err != nil {
		return nil, err
	}
	if err := r.hydrateLiveness(ctx, nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (r *NodeRepo) List(ctx context.Context) ([]model.Node, error) {
	var nodes []model.Node
	if err := r.store.db.WithContext(ctx).Order("node_id ASC").Find(&nodes).Error; err != nil {
		return nil, err
	}
	if err := r.hydrateLiveness(ctx, nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

// NodeFilter specifies search criteria for filtering nodes.
type NodeFilter struct {
	NodeIDs []string
	OS      []string
	Arch    []string
	Status  []model.NodeStatus
	Groups  []string // AND: node must contain every group name
}

func (r *NodeRepo) nodeFilterQuery(ctx context.Context, filter NodeFilter) *gorm.DB {
	query := r.store.db.WithContext(ctx).Model(&model.Node{})
	if len(filter.NodeIDs) > 0 {
		query = query.Where("nodes.node_id IN ?", filter.NodeIDs)
	}
	if len(filter.OS) > 0 {
		query = query.Where("nodes.os IN ?", filter.OS)
	}
	if len(filter.Arch) > 0 {
		query = query.Where("nodes.arch IN ?", filter.Arch)
	}
	if len(filter.Status) > 0 {
		query = query.Joins("LEFT JOIN node_liveness l ON l.node_id = nodes.node_id")
		var clauses []string
		var args []any
		for _, st := range filter.Status {
			switch st {
			case model.NodeStatusMaintenance:
				clauses = append(clauses, "nodes.in_maintenance = ?")
				args = append(args, true)
			case model.NodeStatusOnline:
				clauses = append(clauses, "(nodes.in_maintenance = ? AND l.online = ?)")
				args = append(args, false, true)
			case model.NodeStatusOffline:
				clauses = append(clauses, "(nodes.in_maintenance = ? AND COALESCE(l.online, ?) = ? AND l.last_connected_at > ?)")
				args = append(args, false, false, false, time.Time{})
			}
		}
		if len(clauses) > 0 {
			joined := clauses[0]
			for _, c := range clauses[1:] {
				joined += " OR " + c
			}
			query = query.Where("("+joined+")", args...)
		} else {
			query = query.Where("1 = 0")
		}
	}
	return query
}

func (r *NodeRepo) ListByFilter(ctx context.Context, filter NodeFilter) ([]model.Node, error) {
	if len(filter.Groups) == 0 {
		var nodes []model.Node
		if err := r.nodeFilterQuery(ctx, filter).Order("nodes.node_id ASC").Find(&nodes).Error; err != nil {
			return nil, err
		}
		if err := r.hydrateLiveness(ctx, nodes); err != nil {
			return nil, err
		}
		return nodes, nil
	}

	ids, err := r.listNodeIDsMatchingGroups(ctx, filter)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []model.Node{}, nil
	}
	var nodes []model.Node
	if err := r.store.db.WithContext(ctx).
		Where("node_id IN ?", ids).
		Order("node_id ASC").
		Find(&nodes).Error; err != nil {
		return nil, err
	}
	if err := r.hydrateLiveness(ctx, nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

// listNodeIDsMatchingGroups applies SQL filters then AND-matches Groups in memory (no SQL dialect).
func (r *NodeRepo) listNodeIDsMatchingGroups(ctx context.Context, filter NodeFilter) ([]string, error) {
	type groupRow struct {
		NodeID string
		Groups []string `gorm:"serializer:json"`
	}
	var rows []groupRow
	if err := r.nodeFilterQuery(ctx, filter).
		Select("nodes.node_id", "nodes.groups").
		Order("nodes.node_id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if model.NodeHasGroups(row.Groups, filter.Groups) {
			ids = append(ids, row.NodeID)
		}
	}
	return ids, nil
}

// ListPage returns a page of nodes matching filter. SQL limit/offset is used when groups is empty;
// with groups, only node_id/groups are scanned for AND filtering in memory (no SQL dialect),
// then the page's full rows are loaded by id.
func (r *NodeRepo) ListPage(ctx context.Context, filter NodeFilter, limit, offset int) ([]model.Node, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	if len(filter.Groups) > 0 {
		ids, err := r.listNodeIDsMatchingGroups(ctx, filter)
		if err != nil {
			return nil, 0, err
		}
		pageIDs, total := PageSlice(ids, limit, offset)
		if len(pageIDs) == 0 {
			return []model.Node{}, int64(total), nil
		}
		var nodes []model.Node
		if err := r.store.db.WithContext(ctx).
			Where("node_id IN ?", pageIDs).
			Find(&nodes).Error; err != nil {
			return nil, 0, err
		}
		if err := r.hydrateLiveness(ctx, nodes); err != nil {
			return nil, 0, err
		}
		byID := make(map[string]model.Node, len(nodes))
		for _, n := range nodes {
			byID[n.NodeID] = n
		}
		page := make([]model.Node, 0, len(pageIDs))
		for _, id := range pageIDs {
			if n, ok := byID[id]; ok {
				page = append(page, n)
			}
		}
		return page, int64(total), nil
	}

	query := r.nodeFilterQuery(ctx, filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var nodes []model.Node
	if err := query.Order("nodes.node_id ASC").Limit(limit).Offset(offset).Find(&nodes).Error; err != nil {
		return nil, 0, err
	}
	if err := r.hydrateLiveness(ctx, nodes); err != nil {
		return nil, 0, err
	}
	return nodes, total, nil
}

// CountRuntimeByStatus counts only enrolled runtime statuses (online/offline/maintenance).
func (r *NodeRepo) CountRuntimeByStatus(ctx context.Context) (online, offline, maintenance int64, err error) {
	type row struct {
		InMaintenance bool
		Online        bool
		Count         int64
	}
	var rows []row
	err = r.store.db.WithContext(ctx).
		Table("node_liveness l").
		Joins("JOIN nodes n ON n.node_id = l.node_id").
		Select("n.in_maintenance, l.online, count(*) as count").
		Where("l.last_connected_at > ?", time.Time{}).
		Group("n.in_maintenance, l.online").
		Find(&rows).Error
	if err != nil {
		return 0, 0, 0, err
	}
	for _, r := range rows {
		switch {
		case r.InMaintenance:
			maintenance += r.Count
		case r.Online:
			online += r.Count
		default:
			offline += r.Count
		}
	}
	return online, offline, maintenance, nil
}

const heartbeatBatchSize = 500

func (r *NodeRepo) upsertLivenessOnline(ctx context.Context, nodeID string, now time.Time) error {
	row := model.NodeLiveness{
		NodeID:          nodeID,
		Online:          true,
		LastHeartbeatAt: now,
		LastConnectedAt: now,
	}
	return r.store.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "node_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"online":            true,
			"last_heartbeat_at": now,
			"last_connected_at": now,
		}),
	}).Create(&row).Error
}

func (r *NodeRepo) MarkOnline(ctx context.Context, nodeID string) error {
	if nodeID == "" {
		return nil
	}
	return r.upsertLivenessOnline(ctx, nodeID, time.Now())
}

// MarkOnlineRegister marks first agent connect online and persists platform fields in one update.
func (r *NodeRepo) MarkOnlineRegister(ctx context.Context, nodeID, osFamily, arch, osProduct, osVersion, kernel string) error {
	if nodeID == "" {
		return nil
	}
	platformUpdates := map[string]any{}
	if osFamily != "" {
		platformUpdates["os"] = osFamily
	}
	if arch != "" {
		platformUpdates["arch"] = arch
	}
	if osProduct != "" {
		platformUpdates["os_name"] = osProduct
	}
	if osVersion != "" {
		platformUpdates["os_version"] = osVersion
	}
	if kernel != "" {
		platformUpdates["kernel_version"] = kernel
	}
	if len(platformUpdates) > 0 {
		if err := r.store.db.WithContext(ctx).Model(&model.Node{}).
			Where("node_id = ?", nodeID).
			Updates(platformUpdates).Error; err != nil {
			return err
		}
	}
	return r.upsertLivenessOnline(ctx, nodeID, time.Now())
}

// TouchHeartbeats batch-updates last_heartbeat_at and online status in node_liveness
// in chunks of 500 without touching static nodes rows.
func (r *NodeRepo) TouchHeartbeats(ctx context.Context, nodeIDs []string, at time.Time) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	rows := make([]model.NodeLiveness, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		if id != "" {
			rows = append(rows, model.NodeLiveness{
				NodeID:          id,
				Online:          true,
				LastHeartbeatAt: at,
			})
		}
	}
	if len(rows) == 0 {
		return nil
	}

	for chunk := range slices.Chunk(rows, heartbeatBatchSize) {
		if err := r.store.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "node_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				// 断线优先：本批次携带的是采样时刻 at。若节点在 at 之后已断线，
				// 迟到的批次不能把 online 重新置回 true；否则节点会假在线直到下一次超时巡检。
				"online":            gorm.Expr("CASE WHEN node_liveness.last_disconnected_at > excluded.last_heartbeat_at THEN FALSE ELSE TRUE END"),
				"last_heartbeat_at": at,
			}),
		}).Create(&chunk).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *NodeRepo) MarkDisconnected(ctx context.Context, nodeID string) error {
	if nodeID == "" {
		return nil
	}
	now := time.Now()
	return r.store.db.WithContext(ctx).Model(&model.NodeLiveness{}).Where(
		"node_id = ? AND online = ?",
		nodeID, true,
	).Updates(map[string]any{
		"online":               false,
		"last_disconnected_at": now,
	}).Error
}

func (r *NodeRepo) MarkStaleOffline(ctx context.Context, staleBefore time.Time) ([]string, error) {
	var staledNodes []string
	err := r.store.db.WithContext(ctx).Model(&model.NodeLiveness{}).
		Where("online = ? AND last_heartbeat_at < ?", true, staleBefore).
		Pluck("node_id", &staledNodes).Error
	if err != nil {
		return nil, err
	}
	if len(staledNodes) == 0 {
		return nil, nil
	}
	now := time.Now()
	result := r.store.db.WithContext(ctx).Model(&model.NodeLiveness{}).
		Where("node_id IN ? AND online = ? AND last_heartbeat_at < ?",
			staledNodes, true, staleBefore).
		Updates(map[string]any{
			"online":               false,
			"last_disconnected_at": now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	return staledNodes, nil
}

func (r *NodeRepo) ResetOnlineToOffline(ctx context.Context) (int64, error) {
	now := time.Now()
	result := r.store.db.WithContext(ctx).Model(&model.NodeLiveness{}).
		Where("online = ?", true).
		Updates(map[string]any{
			"online":               false,
			"last_disconnected_at": now,
		})
	return result.RowsAffected, result.Error
}

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
	}).Error
}

func (r *NodeRepo) ListCertExpiring(ctx context.Context, before time.Time) ([]model.Node, error) {
	var nodes []model.Node
	err := r.store.db.WithContext(ctx).
		Where("cert_expires_at < ? AND cert_expires_at > ?", before, time.Time{}).
		Find(&nodes).Error
	if err != nil {
		return nil, err
	}
	if err := r.hydrateLiveness(ctx, nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (r *NodeRepo) UpdateCertAlertStage(ctx context.Context, nodeID string, stage int16) error {
	return r.store.db.WithContext(ctx).Model(&model.Node{}).
		Where("node_id = ?", nodeID).
		Update("cert_alert_stage", stage).Error
}

func (r *NodeRepo) UpdateCertMetadata(ctx context.Context, nodeID, subject string, expiresAt time.Time) error {
	if expiresAt.IsZero() {
		return nil
	}
	return r.store.db.WithContext(ctx).Model(&model.Node{}).
		Where("node_id = ?", nodeID).
		Updates(map[string]any{"cert_subject": subject, "cert_expires_at": expiresAt}).Error
}

// CountActiveConnectionsByNodes counts running connections grouped by node_id for the given nodeIDs.
func (r *NodeRepo) CountActiveConnectionsByNodes(ctx context.Context, nodeIDs []string) (map[string]int, error) {
	return r.countActiveConnections(ctx, nodeIDs, true)
}

func (r *NodeRepo) countActiveConnections(ctx context.Context, nodeIDs []string, filterByNodeIDs bool) (map[string]int, error) {
	if r == nil || r.store == nil || r.store.db == nil {
		return nil, nil
	}
	if filterByNodeIDs && len(nodeIDs) == 0 {
		return map[string]int{}, nil
	}
	type rowResult struct {
		NodeID string `gorm:"column:node_id"`
		Count  int    `gorm:"column:cnt"`
	}
	query := r.store.db.WithContext(ctx).Model(&model.Connection{}).
		Select("node_id, COUNT(*) as cnt").
		Where("status = ?", model.ConnectionRunning)
	if filterByNodeIDs {
		query = query.Where("node_id IN ?", nodeIDs)
	}
	var rows []rowResult
	if err := query.Group("node_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]int, len(rows))
	for _, row := range rows {
		result[row.NodeID] = row.Count
	}
	return result, nil
}

func (r *NodeRepo) CountActiveConnectionsForNode(ctx context.Context, nodeID string) (int, error) {
	if r == nil || r.store == nil || r.store.db == nil {
		return 0, nil
	}
	var cnt int64
	err := r.store.db.WithContext(ctx).Model(&model.Connection{}).
		Where("node_id = ? AND status = ?", nodeID, model.ConnectionRunning).
		Count(&cnt).Error
	return int(cnt), err
}
