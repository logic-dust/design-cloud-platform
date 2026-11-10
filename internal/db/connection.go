package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"design-cloud-engine/internal/model"

	"design-cloud-engine/internal/contract"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrQuotaExceeded   = errors.New("connection quota exceeded")
	ErrDisplayOccupied = errors.New("display is already occupied")
)

const vncClaimAttempts = 8

// errClaimConflict 是本次插入撞上唯一约束。事务会整体回滚，调用方换一个端口或显示号再开一轮事务。
var errClaimConflict = errors.New("vnc claim conflict")

type ConnectionRepo struct {
	store *Store
}

// List returns a page of connections. limit must be > 0; use ListAll for a full scan.
func (r *ConnectionRepo) List(ctx context.Context, limit, offset int) ([]model.Connection, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	var connections []model.Connection
	var total int64

	query := r.store.db.WithContext(ctx).Model(&model.Connection{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	query = query.Limit(limit).Offset(offset)
	if err := query.Order("id DESC").Find(&connections).Error; err != nil {
		return nil, 0, err
	}

	return connections, total, nil
}

// ListAll returns every connection row (no pagination).
func (r *ConnectionRepo) ListAll(ctx context.Context) ([]model.Connection, error) {
	var connections []model.Connection
	if err := r.store.db.WithContext(ctx).Order("id DESC").Find(&connections).Error; err != nil {
		return nil, err
	}
	return connections, nil
}

func (r *ConnectionRepo) ListByUser(ctx context.Context, username string) ([]model.Connection, error) {
	var connections []model.Connection
	err := r.store.db.WithContext(ctx).
		Where("username = ? AND status != ?", username, model.ConnectionTerminating).
		Order("last_accessed_at DESC NULLS LAST, id DESC").
		Find(&connections).Error
	if err != nil {
		return nil, err
	}
	return connections, nil
}

// ListAllByUser returns every connection row for a user, including terminating tombstones.
func (r *ConnectionRepo) ListAllByUser(ctx context.Context, username string) ([]model.Connection, error) {
	var connections []model.Connection
	err := r.store.db.WithContext(ctx).
		Where("username = ?", username).
		Order("last_accessed_at DESC NULLS LAST, id DESC").
		Find(&connections).Error
	if err != nil {
		return nil, err
	}
	return connections, nil
}

// ListTerminating returns all terminating tombstones (any protocol).
func (r *ConnectionRepo) ListTerminating(ctx context.Context) ([]model.Connection, error) {
	var connections []model.Connection
	err := r.store.db.WithContext(ctx).
		Where("status = ?", model.ConnectionTerminating).
		Order("id ASC").
		Find(&connections).Error
	return connections, err
}

func (r *ConnectionRepo) GetByID(ctx context.Context, id uint) (model.Connection, error) {
	var connection model.Connection
	err := r.store.db.WithContext(ctx).First(&connection, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Connection{}, fmt.Errorf("connection %d: %w", id, contract.ErrNotFound)
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
	err := r.store.db.WithContext(ctx).Where("id IN ?", ids).Find(&connections).Error
	if err != nil {
		return nil, err
	}
	return connections, nil
}

func (r *ConnectionRepo) CountByUsernameAndResource(ctx context.Context, username string, resourceID uint) (int, error) {
	var count int64
	err := r.store.db.WithContext(ctx).Model(&model.Connection{}).
		Where("username = ? AND resource_id = ?", username, resourceID).
		Count(&count).Error
	return int(count), err
}

func (r *ConnectionRepo) CountActiveByNode(ctx context.Context) (map[string]int, error) {
	type rowResult struct {
		NodeID string `gorm:"column:node_id"`
		Count  int    `gorm:"column:cnt"`
	}
	var rows []rowResult
	err := r.store.db.WithContext(ctx).Model(&model.Connection{}).
		Select("node_id, COUNT(*) as cnt").
		Where("status = ?", model.ConnectionRunning).
		Group("node_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string]int, len(rows))
	for _, r := range rows {
		result[r.NodeID] = r.Count
	}
	return result, nil
}

// ListVNCByNode returns connection rows for a node limited to the VNC protocol.
func (r *ConnectionRepo) ListVNCByNode(ctx context.Context, nodeID string) ([]model.Connection, error) {
	var list []model.Connection
	err := r.store.db.WithContext(ctx).
		Where("node_id = ? AND protocol = ?", nodeID, contract.VNCProtocol).
		Find(&list).Error
	return list, err
}

// ListXRDPByNode returns connection rows for a node limited to the XRDP protocol.
func (r *ConnectionRepo) ListXRDPByNode(ctx context.Context, nodeID string) ([]model.Connection, error) {
	var list []model.Connection
	err := r.store.db.WithContext(ctx).
		Where("node_id = ? AND protocol = ?", nodeID, contract.XRDPProtocol).
		Find(&list).Error
	return list, err
}

// ListRDPByNode returns RDP connection rows for a node.
func (r *ConnectionRepo) ListRDPByNode(ctx context.Context, nodeID string) ([]model.Connection, error) {
	var list []model.Connection
	err := r.store.db.WithContext(ctx).
		Where("node_id = ? AND protocol = ?", nodeID, contract.RDPProtocol).
		Find(&list).Error
	return list, err
}

// StoppedConnectionPruneBatch is the explicit batch size for stopped-row retention pruning.
const StoppedConnectionPruneBatch = 500

// PruneStoppedConnectionsBefore deletes stopped connection rows last updated before cutoff.
func (r *ConnectionRepo) PruneStoppedConnectionsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if err := validateLimit(limit); err != nil {
		return 0, err
	}
	var ids []uint
	if err := r.store.db.WithContext(ctx).Model(&model.Connection{}).
		Where("status = ? AND updated_at < ?", model.ConnectionStopped, before).
		Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := r.store.db.WithContext(ctx).Delete(&model.Connection{}, ids)
	return result.RowsAffected, result.Error
}

// ListRunningVNC returns running VNC connections, the only rows backed by a node-side process.
func (r *ConnectionRepo) ListRunningVNC(ctx context.Context) ([]model.Connection, error) {
	var list []model.Connection
	err := r.store.db.WithContext(ctx).Where("status = ? AND protocol = ?", model.ConnectionRunning, contract.VNCProtocol).Find(&list).Error
	return list, err
}

func (r *ConnectionRepo) ListByResource(ctx context.Context, resourceID uint) ([]model.Connection, error) {
	var list []model.Connection
	err := r.store.db.WithContext(ctx).Where("resource_id = ?", resourceID).Find(&list).Error
	return list, err
}

func (r *ConnectionRepo) ListByUsernameAndResource(ctx context.Context, username string, resourceID uint) ([]model.Connection, error) {
	var list []model.Connection
	err := r.store.db.WithContext(ctx).
		Where("username = ? AND resource_id = ?", username, resourceID).
		Order("last_accessed_at DESC, id DESC").
		Find(&list).Error
	return list, err
}

func (r *ConnectionRepo) CountByUsernameGroupByResource(ctx context.Context, username string) (map[uint]int, error) {
	type rowResult struct {
		ResourceID *uint `gorm:"column:resource_id"`
		Count      int   `gorm:"column:cnt"`
	}
	var rows []rowResult
	err := r.store.db.WithContext(ctx).Model(&model.Connection{}).
		Select("resource_id, COUNT(*) as cnt").
		Where("username = ? AND resource_id IS NOT NULL", username).
		Group("resource_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make(map[uint]int, len(rows))
	for _, row := range rows {
		if row.ResourceID != nil {
			result[*row.ResourceID] = row.Count
		}
	}
	return result, nil
}

func (r *ConnectionRepo) HasDisplayOnNode(ctx context.Context, nodeID string, display int, excludeID uint) (bool, error) {
	q := r.store.db.WithContext(ctx).Model(&model.Connection{}).
		Where("node_id = ? AND display = ?", nodeID, display)
	if excludeID > 0 {
		q = q.Where("id != ?", excludeID)
	}
	var count int64
	err := q.Count(&count).Error
	return count > 0, err
}

func (r *ConnectionRepo) Create(ctx context.Context, conn *model.Connection) error {
	return r.store.db.WithContext(ctx).Create(conn).Error
}

func (r *ConnectionRepo) CreateWithResourceQuota(ctx context.Context, conn *model.Connection) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if conn.ResourceID != nil {
			if err := lockResourceQuota(tx, conn); err != nil {
				return err
			}
		}

		if conn.Display != nil {
			var holders []model.Connection
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("node_id = ? AND display = ?", conn.NodeID, *conn.Display).
				Find(&holders).Error; err != nil {
				return err
			}
			if len(holders) > 0 {
				return contract.NewConflictError(
					fmt.Sprintf("display %d on node %s is already occupied", *conn.Display, conn.NodeID),
					ErrDisplayOccupied,
				)
			}
		}

		if err := tx.Create(conn).Error; err != nil {
			if IsUniqueConstraintError(err) {
				return contract.NewConflictError("connection already exists", err)
			}
			return err
		}
		return nil
	})
}

// ClaimVNC 在事务里锁规格、校验配额，并插入 fixed display 的 pending 行占住 (node_id, display)。
// 动态 display 须用 ClaimVNCPending，由 Agent 选号后 FinalizeVNCPending。
// 成功后 conn 带上 ID、Display、占位 Port 和 pending。调用方提交之后再拉进程，失败则 ReleaseClaim。
func (r *ConnectionRepo) ClaimVNC(ctx context.Context, conn *model.Connection, display int) error {
	if conn == nil || conn.ResourceID == nil {
		return fmt.Errorf("vnc claim requires a resource id")
	}
	if conn.NodeID == "" || conn.Username == "" {
		return fmt.Errorf("vnc claim requires node id and username")
	}

	skipPorts := map[uint]struct{}{}
	var lastConflict error
	for range vncClaimAttempts {
		var triedPort uint
		tried := false
		err := r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := lockResourceQuota(tx, conn); err != nil {
				return err
			}
			if err := assertFixedDisplayFree(tx, conn.NodeID, display); err != nil {
				return err
			}
			port, err := pickPlaceholderPort(tx, conn.NodeID, conn.Username, skipPorts)
			if err != nil {
				return err
			}
			triedPort = port
			tried = true

			row := *conn
			row.ID = 0
			disp := display
			row.Display = &disp
			row.Port = port
			row.PID = nil
			row.Status = model.ConnectionPending
			if err := tx.Create(&row).Error; err != nil {
				if IsUniqueConstraintError(err) {
					return errClaimConflict
				}
				return err
			}
			*conn = row
			return nil
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, errClaimConflict) {
			return err
		}
		lastConflict = err
		if tried {
			skipPorts[triedPort] = struct{}{}
		}
	}

	occupied, err := r.HasDisplayOnNode(ctx, conn.NodeID, display, 0)
	if err != nil {
		return err
	}
	if occupied {
		return contract.NewConflictError(
			fmt.Sprintf("display %d on node %s is already occupied", display, conn.NodeID),
			ErrDisplayOccupied,
		)
	}
	return contract.NewConflictError("connection already exists", lastConflict)
}

// ClaimVNCPending 插入 pending 行占配额与占位 port，display 由 Agent 启动后回写。
func (r *ConnectionRepo) ClaimVNCPending(ctx context.Context, conn *model.Connection) error {
	if conn == nil || conn.ResourceID == nil {
		return fmt.Errorf("vnc claim requires a resource id")
	}
	if conn.NodeID == "" || conn.Username == "" {
		return fmt.Errorf("vnc claim requires node id and username")
	}

	skipPorts := map[uint]struct{}{}
	for range vncClaimAttempts {
		var triedPort uint
		tried := false
		err := r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := lockResourceQuota(tx, conn); err != nil {
				return err
			}
			port, err := pickPlaceholderPort(tx, conn.NodeID, conn.Username, skipPorts)
			if err != nil {
				return err
			}
			triedPort = port
			tried = true

			row := *conn
			row.ID = 0
			row.Display = nil
			row.Port = port
			row.PID = nil
			row.Status = model.ConnectionPending
			if err := tx.Create(&row).Error; err != nil {
				if IsUniqueConstraintError(err) {
					return errClaimConflict
				}
				return err
			}
			*conn = row
			return nil
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, errClaimConflict) {
			return err
		}
		if tried {
			skipPorts[triedPort] = struct{}{}
		}
	}
	return contract.NewConflictError("connection already exists", errClaimConflict)
}

// PickPlaceholderPort chooses a unique placeholder port for node+user (e.g. after Stop clears listen port).
func (r *ConnectionRepo) PickPlaceholderPort(ctx context.Context, nodeID, username string) (uint, error) {
	var port uint
	err := r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := pickPlaceholderPort(tx, nodeID, username, nil)
		if err != nil {
			return err
		}
		port = p
		return nil
	})
	return port, err
}

// FinalizeVNCPending 把 pending 行改为 running 并写入 Agent 选定的 display、port、pid。
func (r *ConnectionRepo) FinalizeVNCPending(ctx context.Context, id uint, display int, port uint, pid *uint, controlledAt time.Time) error {
	if id == 0 {
		return fmt.Errorf("connection id is required")
	}
	if display < 0 {
		return fmt.Errorf("display is required")
	}
	if port == 0 {
		return fmt.Errorf("listen port is required")
	}
	res := r.store.db.WithContext(ctx).Model(&model.Connection{}).
		Where("id = ? AND status = ?", id, model.ConnectionPending).
		Updates(map[string]any{
			"status":        model.ConnectionRunning,
			"display":       display,
			"port":          port,
			"pid":           pid,
			"controlled_at": controlledAt,
		})
	if res.Error != nil {
		if IsUniqueConstraintError(res.Error) {
			return contract.NewConflictError("connection display or port already exists", res.Error)
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("connection %d: %w", id, contract.ErrNotFound)
	}
	return nil
}

// ReleaseClaim 只删除本次插入的 pending 占坑行。已经变成 running 的行不会被删。
func (r *ConnectionRepo) ReleaseClaim(ctx context.Context, id uint) error {
	if id == 0 {
		return nil
	}
	return r.store.db.WithContext(ctx).
		Where("status = ?", model.ConnectionPending).
		Delete(&model.Connection{}, id).Error
}

// FinalizeClaim 把占坑行改成 running，并写入实际监听端口和 pid。
func (r *ConnectionRepo) FinalizeClaim(ctx context.Context, id uint, port uint, pid *uint, controlledAt time.Time) error {
	if id == 0 {
		return fmt.Errorf("connection id is required")
	}
	if port == 0 {
		return fmt.Errorf("listen port is required")
	}
	res := r.store.db.WithContext(ctx).Model(&model.Connection{}).
		Where("id = ? AND status = ?", id, model.ConnectionPending).
		Updates(map[string]any{
			"status":        model.ConnectionRunning,
			"port":          port,
			"pid":           pid,
			"controlled_at": controlledAt,
		})
	if res.Error != nil {
		if IsUniqueConstraintError(res.Error) {
			return contract.NewConflictError("connection port already exists", res.Error)
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("connection %d: %w", id, contract.ErrNotFound)
	}
	return nil
}

// lockResourceQuota 先锁规格行再数已用行。连接行还是 0 条时，对 COUNT 做 FOR UPDATE 锁不住任何行。
func lockResourceQuota(tx *gorm.DB, conn *model.Connection) error {
	if conn.ResourceID == nil {
		return fmt.Errorf("connection resource id is required")
	}
	var res model.ConnectionResource
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&res, *conn.ResourceID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("connection resource %d: %w", *conn.ResourceID, contract.ErrNotFound)
		}
		return err
	}
	if res.MaxTotal > 0 {
		var totalCount int64
		if err := tx.Model(&model.Connection{}).
			Where("resource_id = ?", *conn.ResourceID).
			Count(&totalCount).Error; err != nil {
			return err
		}
		if totalCount >= int64(res.MaxTotal) {
			return contract.NewConflictError(
				fmt.Sprintf("resource %q has reached maximum total quota of %d", res.Name, res.MaxTotal),
				ErrQuotaExceeded,
			)
		}
	}
	var currentCount int64
	if err := tx.Model(&model.Connection{}).
		Where("username = ? AND resource_id = ?", conn.Username, *conn.ResourceID).
		Count(&currentCount).Error; err != nil {
		return err
	}
	if currentCount >= int64(res.MaxPerUser) {
		return contract.NewConflictError(
			fmt.Sprintf("user %s has reached maximum quota of %d for resource %d", conn.Username, res.MaxPerUser, *conn.ResourceID),
			ErrQuotaExceeded,
		)
	}
	return nil
}

func assertFixedDisplayFree(tx *gorm.DB, nodeID string, display int) error {
	var holders []model.Connection
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("node_id = ? AND display = ?", nodeID, display).
		Find(&holders).Error; err != nil {
		return err
	}
	if len(holders) > 0 {
		return contract.NewConflictError(
			fmt.Sprintf("display %d on node %s is already occupied", display, nodeID),
			ErrDisplayOccupied,
		)
	}
	return nil
}

func pickPlaceholderPort(tx *gorm.DB, nodeID, username string, skip map[uint]struct{}) (uint, error) {
	var used []uint
	if err := tx.Model(&model.Connection{}).
		Where("node_id = ? AND username = ?", nodeID, username).
		Pluck("port", &used).Error; err != nil {
		return 0, err
	}
	taken := make(map[uint]struct{}, len(used)+len(skip))
	for _, port := range used {
		taken[port] = struct{}{}
	}
	for port := range skip {
		taken[port] = struct{}{}
	}
	for port := uint(1); port < 100000; port++ {
		if _, ok := taken[port]; ok {
			continue
		}
		return port, nil
	}
	return 0, fmt.Errorf("no placeholder port for %s on %s", username, nodeID)
}

func (r *ConnectionRepo) UpdateFields(ctx context.Context, id uint, fields map[string]any) error {
	return r.store.db.WithContext(ctx).Model(&model.Connection{}).Where("id = ?", id).Updates(fields).Error
}

// MarkPendingFromStopped CAS-transitions a stopped row to pending for in-place Start.
func (r *ConnectionRepo) MarkPendingFromStopped(ctx context.Context, id uint, controlledAt time.Time) error {
	res := r.store.db.WithContext(ctx).Model(&model.Connection{}).
		Where("id = ? AND status = ?", id, model.ConnectionStopped).
		Updates(map[string]any{
			"status":        model.ConnectionPending,
			"controlled_at": controlledAt,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return contract.NewConflictError("connection is not stopped")
	}
	return nil
}

// MarkRunningFromPending CAS-writes launch results onto a pending row (Start path).
func (r *ConnectionRepo) MarkRunningFromPending(ctx context.Context, id uint, display int, port uint, pid *uint, controlledAt time.Time) error {
	res := r.store.db.WithContext(ctx).Model(&model.Connection{}).
		Where("id = ? AND status = ?", id, model.ConnectionPending).
		Updates(map[string]any{
			"status":           model.ConnectionRunning,
			"display":          display,
			"port":             port,
			"pid":              pid,
			"controlled_at":    controlledAt,
			"last_accessed_at": controlledAt,
		})
	if res.Error != nil {
		if IsUniqueConstraintError(res.Error) {
			return contract.NewConflictError("connection display or port already exists", res.Error)
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("connection %d: %w", id, contract.ErrNotFound)
	}
	return nil
}

// MarkStoppedClearDisplay sets stopped and clears display/pid with a placeholder port.
func (r *ConnectionRepo) MarkStoppedClearDisplay(ctx context.Context, id uint, placeholderPort uint) error {
	return r.UpdateFields(ctx, id, map[string]any{
		"status":  model.ConnectionStopped,
		"display": nil,
		"pid":     nil,
		"port":    placeholderPort,
	})
}

// MarkTerminatingClearResource marks a delete tombstone and drops resource association.
func (r *ConnectionRepo) MarkTerminatingClearResource(ctx context.Context, id uint) error {
	return r.store.db.WithContext(ctx).Model(&model.Connection{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      model.ConnectionTerminating,
			"resource_id": nil,
		}).Error
}

// MarkTerminatingKeepResource marks stop/reap tombstone while retaining display/pid/resource.
func (r *ConnectionRepo) MarkTerminatingKeepResource(ctx context.Context, id uint) error {
	return r.UpdateFields(ctx, id, map[string]any{
		"status": model.ConnectionTerminating,
	})
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

const connectionDialHostSelect = "connections.*, COALESCE(NULLIF(nodes.dial_host, ''), connections.node_id) AS dial_host_resolved"

type connectionDialRow struct {
	model.Connection
	DialHostResolved string `gorm:"column:dial_host_resolved"`
}

func joinConnectionDialHost(db *gorm.DB) *gorm.DB {
	return db.
		Select(connectionDialHostSelect).
		Joins("LEFT JOIN nodes ON nodes.node_id = connections.node_id")
}

func unpackConnectionDialRows(rows []connectionDialRow) []model.Connection {
	out := make([]model.Connection, len(rows))
	for i, row := range rows {
		out[i] = row.Connection
		out[i].Host = row.DialHostResolved
	}
	return out
}

// ListByUserWithDialHost returns user connections with API host resolved via nodes JOIN.
func (r *ConnectionRepo) ListByUserWithDialHost(ctx context.Context, username string) ([]model.Connection, error) {
	var rows []connectionDialRow
	err := joinConnectionDialHost(r.store.db.WithContext(ctx).Model(&model.Connection{})).
		Where("connections.username = ? AND connections.status != ?", username, model.ConnectionTerminating).
		Order("connections.last_accessed_at DESC NULLS LAST, connections.id DESC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return unpackConnectionDialRows(rows), nil
}

// GetByIDWithDialHost loads one connection and resolves dial host from nodes.
func (r *ConnectionRepo) GetByIDWithDialHost(ctx context.Context, id uint) (model.Connection, error) {
	var rows []connectionDialRow
	err := joinConnectionDialHost(r.store.db.WithContext(ctx).Model(&model.Connection{})).
		Where("connections.id = ?", id).
		Limit(1).
		Find(&rows).Error
	if err != nil {
		return model.Connection{}, err
	}
	if len(rows) == 0 {
		return model.Connection{}, fmt.Errorf("connection %d: %w", id, contract.ErrNotFound)
	}
	out := rows[0].Connection
	out.Host = rows[0].DialHostResolved
	return out, nil
}
