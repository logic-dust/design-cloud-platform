package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ConnectionRepo struct {
	store *Store
}

func (r *ConnectionRepo) List(ctx context.Context, limit, offset int) ([]model.Connection, int64, error) {
	var connections []model.Connection
	var total int64
	query := r.store.db.WithContext(ctx).Model(&model.Connection{}).
		Select("connections.*, nodes.client_host AS node_client_host").
		Joins("LEFT JOIN nodes ON nodes.node_id = connections.target_node_id")
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit > 0 {
		query = query.Limit(limit).Offset(offset)
	}
	if err := query.Order("connections.updated_at DESC, connections.id DESC").Find(&connections).Error; err != nil {
		return nil, 0, err
	}
	return connections, total, nil
}

func (r *ConnectionRepo) ListByUser(ctx context.Context, username string) ([]model.Connection, error) {
	var connections []model.Connection
	if err := r.store.db.WithContext(ctx).
		Select("connections.*, nodes.client_host AS node_client_host").
		Joins("LEFT JOIN nodes ON nodes.node_id = connections.target_node_id").
		Where("connections.username = ? OR connections.shared = ?", username, true).Order("connections.updated_at DESC, connections.id DESC").Find(&connections).Error; err != nil {
		return nil, err
	}
	return connections, nil
}

// ListReconcileByNode returns connections that need VNC session reconciliation for the given node.
// Reconciliation scope:
//   - Active or unknown-status connections only (Running, Unknown)
//   - Excludes manually created connections (source NOT IN (Manual))
//   - Excludes shared connections (shared=false) — shared RDP/SSH endpoints are
//     not VNC processes on the node and must not be mutated by discovery
//   - Restricted to VNC protocol (protocol = VNC)
//
// Fixed connections ARE included (unless shared): VNC discovery may update runtime fields
// (PID, port, status) but mergeRuntimeConnection preserves the source and owner identity.
func (r *ConnectionRepo) ListReconcileByNode(ctx context.Context, nodeID string) ([]model.Connection, error) {
	var connections []model.Connection
	err := r.store.db.WithContext(ctx).
		Select("connections.*, nodes.client_host AS node_client_host").
		Joins("LEFT JOIN nodes ON nodes.node_id = connections.target_node_id").
		Where("connections.target_node_id = ? AND connections.status IN (?, ?) AND connections.source != ? AND connections.shared = ? AND connections.protocol = ?",
			nodeID,
			model.ConnectionRunning,
			model.ConnectionUnknown,
			model.ConnectionSourceManual,
			false,
			contract.VNCProtocol,
		).
		Find(&connections).Error
	if err != nil {
		return nil, err
	}
	return connections, nil
}

func (r *ConnectionRepo) GetByID(ctx context.Context, id uint) (model.Connection, error) {
	var connection model.Connection
	if err := r.store.db.WithContext(ctx).
		Select("connections.*, nodes.client_host AS node_client_host").
		Joins("LEFT JOIN nodes ON nodes.node_id = connections.target_node_id").
		Where("connections.id = ?", id).First(&connection).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Connection{}, fmt.Errorf("connection %d: %w", id, common.ErrNotFound)
		}
		return model.Connection{}, err
	}
	return connection, nil
}

func (r *ConnectionRepo) GetByIDs(ctx context.Context, ids []uint) ([]model.Connection, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var connections []model.Connection
	if err := r.store.db.WithContext(ctx).
		Select("connections.*, nodes.client_host AS node_client_host").
		Joins("LEFT JOIN nodes ON nodes.node_id = connections.target_node_id").
		Where("connections.id IN ?", ids).Find(&connections).Error; err != nil {
		return nil, err
	}
	return connections, nil
}

func (r *ConnectionRepo) GetRuntimeMatch(ctx context.Context, targetNodeID, username string, port uint, protocol contract.Protocol, pid uint) (model.Connection, bool, error) {
	var conn model.Connection
	query := r.store.db.WithContext(ctx).
		Model(&model.Connection{}).
		Select("connections.*, nodes.client_host AS node_client_host").
		Joins("LEFT JOIN nodes ON nodes.node_id = connections.target_node_id").
		Where("connections.target_node_id = ? AND connections.username = ? AND connections.status IN (?, ?)", targetNodeID, username, model.ConnectionRunning, model.ConnectionUnknown)
	switch {
	case port > 0:
		query = query.Where("port = ?", port)
		if protocol != "" {
			query = query.Where("(protocol = ? OR protocol = '')", protocol)
		}
	case pid > 0:
		query = query.Where("pid = ?", pid)
	default:
		return model.Connection{}, false, nil
	}

	if err := query.First(&conn).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Connection{}, false, nil
		}
		return model.Connection{}, false, err
	}
	return conn, true, nil
}

// FindFixedMatch looks up a fixed connection by (username, nodeID) + port-or-display priority:
//  1. port > 0  → match by (username, targetNodeID, port)
//  2. port == 0 && display != "" → match by (username, targetNodeID, display, port=0)
//  3. port == 0 && display == "" → match by (username, targetNodeID, port=0, display=”) — node-level fixed
func (r *ConnectionRepo) FindFixedMatch(ctx context.Context, username, nodeID string, port uint, display string) (model.Connection, bool, error) {
	var conn model.Connection
	q := r.store.db.WithContext(ctx).
		Select("connections.*, nodes.client_host AS node_client_host").
		Joins("LEFT JOIN nodes ON nodes.node_id = connections.target_node_id").
		Where("connections.source = ? AND connections.username = ? AND connections.target_node_id = ?",
			model.ConnectionSourceFixed, username, nodeID)
	switch {
	case port > 0:
		q = q.Where("connections.port = ?", port)
	case display != "":
		q = q.Where("connections.port = 0 AND connections.display = ?", display)
	default:
		q = q.Where("connections.port = 0 AND connections.display = ''")
	}
	if err := q.First(&conn).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Connection{}, false, nil
		}
		return model.Connection{}, false, err
	}
	return conn, true, nil
}

// FindSharedStatic finds the shared static connection for a node and protocol.
func (r *ConnectionRepo) FindSharedStatic(ctx context.Context, targetNodeID string, protocol contract.Protocol) (model.Connection, bool, error) {
	var conn model.Connection
	err := r.store.db.WithContext(ctx).
		Select("connections.*, nodes.client_host AS node_client_host").
		Joins("LEFT JOIN nodes ON nodes.node_id = connections.target_node_id").
		Where("connections.target_node_id = ? AND connections.shared = ? AND connections.protocol = ?",
			targetNodeID, true, protocol).
		First(&conn).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Connection{}, false, nil
		}
		return model.Connection{}, false, err
	}
	return conn, true, nil
}

func (r *ConnectionRepo) Create(ctx context.Context, conn *model.Connection) error {
	return r.store.db.WithContext(ctx).Create(conn).Error
}

// CreateWithQuota inserts a connection while enforcing node admission, node capacity,
// and per-user non-shared connection limits inside a transaction with a fixed lock order (Node then User).
func (r *ConnectionRepo) CreateWithQuota(ctx context.Context, conn *model.Connection, quota int) error {
	needNodeCheck := conn.TargetNodeID != ""
	needUserCheck := quota > 0 && !conn.Shared && conn.Username != ""

	if !needNodeCheck && !needUserCheck {
		return r.Create(ctx, conn)
	}

	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Node check (Lock order: Node first)
		if needNodeCheck {
			var node model.Node
			if tx.Name() == "postgres" {
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("node_id = ?", conn.TargetNodeID).First(&node).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return common.NewValidationError(fmt.Sprintf("node %s not found", conn.TargetNodeID))
					}
					return fmt.Errorf("lock node: %w", err)
				}
			} else {
				if err := tx.Where("node_id = ?", conn.TargetNodeID).First(&node).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return common.NewValidationError(fmt.Sprintf("node %s not found", conn.TargetNodeID))
					}
					return fmt.Errorf("find node: %w", err)
				}
			}

			if !node.AllowConnections {
				return common.NewValidationError(fmt.Sprintf("node %s does not allow new connections", conn.TargetNodeID))
			}

			if node.MaxConnections > 0 && !conn.Shared {
				var count int64
				if err := tx.Model(&model.Connection{}).Where("target_node_id = ? AND shared = ?", conn.TargetNodeID, false).Count(&count).Error; err != nil {
					return fmt.Errorf("count node connections: %w", err)
				}
				if count >= int64(node.MaxConnections) {
					return common.NewQuotaExceededError(fmt.Sprintf("node %s has reached its connection capacity of %d", conn.TargetNodeID, node.MaxConnections))
				}
			}
		}

		// 2. User check (Lock order: User after Node)
		if needUserCheck {
			if tx.Name() == "postgres" {
				var u model.User
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("username = ?", conn.Username).First(&u).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						// Fallback to transaction-level advisory lock by username hash
						if lErr := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", conn.Username).Error; lErr != nil {
							return fmt.Errorf("advisory lock: %w", lErr)
						}
					} else {
						return fmt.Errorf("lock user: %w", err)
					}
				}
			}

			var count int64
			if err := tx.Model(&model.Connection{}).Where("username = ? AND shared = ?", conn.Username, false).Count(&count).Error; err != nil {
				return fmt.Errorf("count connections: %w", err)
			}

			if count >= int64(quota) {
				return common.NewQuotaExceededError(fmt.Sprintf("user %s has reached the limit of %d connections", conn.Username, quota))
			}
		}

		return tx.Create(conn).Error
	})
}

func (r *ConnectionRepo) UpdateAllFields(ctx context.Context, conn *model.Connection) error {
	return r.store.db.WithContext(ctx).Model(conn).Select("*").Updates(conn).Error
}

func (r *ConnectionRepo) UpdateFields(ctx context.Context, id uint, fields map[string]any) error {
	return r.store.db.WithContext(ctx).Model(&model.Connection{}).Where("id = ?", id).Updates(fields).Error
}

func (r *ConnectionRepo) DeleteByID(ctx context.Context, id uint) error {
	return r.store.db.WithContext(ctx).Delete(&model.Connection{}, id).Error
}

func (r *ConnectionRepo) DeleteByIDs(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return r.store.db.WithContext(ctx).Where("id IN ?", ids).Delete(&model.Connection{}).Error
}

// DeleteStaleManaged removes old non-running discovered connections.
// Manual and Fixed connections are preserved: Manual is user-defined static endpoints,
// Fixed is admin-assigned.
func (r *ConnectionRepo) DeleteStaleManaged(ctx context.Context, staleBefore time.Time) (int64, error) {
	result := r.store.db.WithContext(ctx).
		Where("status IN (?, ?) AND source = ? AND updated_at < ?",
			model.ConnectionStopped,
			model.ConnectionUnknown,
			model.ConnectionSourceDiscovered,
			staleBefore,
		).
		Delete(&model.Connection{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}
