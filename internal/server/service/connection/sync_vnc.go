package connection

import (
	"cmp"
	"context"
	"fmt"
	"strconv"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

func (s *Service) SyncVNCConnections(ctx context.Context, nodeID string, reportedConns []contract.ConnectionInfo) error {
	if nodeID == "" {
		return fmt.Errorf("node ID is required")
	}

	connections, err := s.connectionRepo.ListReconcileByNode(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("list reconciliation candidates for node %s: %w", nodeID, err)
	}

	byID := make(map[uint]model.Connection, len(connections))
	byUserPort := make(map[string]model.Connection, len(connections))
	byPID := make(map[string]model.Connection, len(connections))
	for _, conn := range connections {
		byID[conn.ID] = conn
		if key := runtimeUserPortKey(conn.Username, conn.Port); key != "" {
			byUserPort[key] = conn
		}
		if conn.PID > 0 {
			byPID[strconv.FormatUint(uint64(conn.PID), 10)] = conn
		}
	}

	matched := make(map[uint]struct{}, len(reportedConns))
	for i := range reportedConns {
		info := &reportedConns[i]
		info.Protocol = cmp.Or(info.Protocol, contract.VNCProtocol)
		if info.Username == "" && info.Port == 0 && info.PID == 0 {
			continue
		}

		conn, ok := matchRuntimeConnection(info, byID, byUserPort, byPID)
		if !ok {
			created, err := s.upsertRuntimeConnection(ctx, nodeID, info)
			if err != nil {
				return err
			}
			info.ConnectionID = created.ID
			if err := s.updateStatus(ctx, info, nodeID, model.ConnectionRunning); err != nil {
				return err
			}
			// updateStatus 内部已做状态变化推送（Unknown→Running），此处不再重复
			// 记录 matched，防止 upsert 找到已有记录后被误标为 stopped
			matched[created.ID] = struct{}{}
			continue
		}

		info.ConnectionID = conn.ID
		if err := s.updateStatus(ctx, info, nodeID, model.ConnectionRunning); err != nil {
			return err
		}
		matched[conn.ID] = struct{}{}
	}

	for _, conn := range connections {
		if _, ok := matched[conn.ID]; ok {
			continue
		}
		// Avoid premature status flip if the connection was just started within grace period (15s)
		if conn.Source == model.ConnectionSourceFixed && conn.Status == model.ConnectionRunning && s.isWithinGracePeriod(conn.ID, 15*time.Second) {
			continue
		}
		if err := s.updateStatus(ctx, &contract.ConnectionInfo{ConnectionID: conn.ID}, nodeID, model.ConnectionStopped); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) upsertRuntimeConnection(ctx context.Context, nodeID string, connInfo *contract.ConnectionInfo) (model.Connection, error) {
	if connInfo == nil {
		return model.Connection{}, ErrInvalidConnectionInfo
	}

	protocol := cmp.Or(connInfo.Protocol, contract.VNCProtocol)
	defaultName := FormatDefaultConnectionName(protocol, connInfo.Username, nodeID, connInfo.Display)
	name := cmp.Or(connInfo.Name, defaultName)

	conn := model.Connection{
		Name:         name,
		Username:     connInfo.Username,
		TargetNodeID: nodeID,
		Host:         connInfo.Host,
		Port:         connInfo.Port,
		Display:      connInfo.Display,
		PID:          connInfo.PID,
		Status:       model.ConnectionUnknown,
		Source:       model.ConnectionSourceDiscovered,
		Protocol:     protocol,
		Alias:        connInfo.Name,
	}

	// 在 Service 层进行匹配和融合
	existing, ok, err := s.connectionRepo.GetRuntimeMatch(ctx, conn.TargetNodeID, conn.Username, conn.Port, conn.Protocol, conn.PID)
	if err != nil {
		return model.Connection{}, fmt.Errorf("get runtime match for connection: %w", err)
	}

	if ok {
		mergeRuntimeConnection(&existing, &conn)
		if err := s.connectionRepo.UpdateAllFields(ctx, &existing); err != nil {
			return model.Connection{}, fmt.Errorf("update existing runtime connection %d: %w", existing.ID, err)
		}
		return existing, nil
	}

	// 尝试创建
	quota, _ := s.effectiveMaxConnections(ctx, conn.Username)
	if err := s.connectionRepo.CreateWithQuota(ctx, &conn, quota); err != nil {
		// 处理并发创建的竞争情况
		if common.IsUniqueConstraintError(err) {
			existing, ok, findErr := s.connectionRepo.GetRuntimeMatch(ctx, conn.TargetNodeID, conn.Username, conn.Port, conn.Protocol, conn.PID)
			if findErr != nil {
				return model.Connection{}, fmt.Errorf("get runtime match after unique conflict: %w", findErr)
			}
			if !ok {
				return model.Connection{}, fmt.Errorf("create runtime connection conflict but no match found: %w", err)
			}
			mergeRuntimeConnection(&existing, &conn)
			if err := s.connectionRepo.UpdateAllFields(ctx, &existing); err != nil {
				return model.Connection{}, fmt.Errorf("update runtime connection %d after conflict: %w", existing.ID, err)
			}
			return existing, nil
		}
		return model.Connection{}, fmt.Errorf("create runtime connection: %w", err)
	}

	return conn, nil
}

func mergeRuntimeConnection(dst, src *model.Connection) {
	if dst == nil || src == nil {
		return
	}
	dst.TargetNodeID = cmp.Or(dst.TargetNodeID, src.TargetNodeID)
	if src.Username != "" {
		dst.Username = src.Username
	}
	if src.Host != "" {
		dst.Host = src.Host
	}
	if src.Display != "" {
		dst.Display = src.Display
	}
	if src.Port != 0 {
		dst.Port = src.Port
	}
	if src.PID != 0 {
		dst.PID = src.PID
	}
	if src.Status != 0 {
		dst.Status = src.Status
	}
	if src.Protocol != "" {
		dst.Protocol = src.Protocol
	}
	if src.Source != 0 && dst.SourceType() == model.ConnectionSourceDiscovered {
		dst.Source = src.Source
	}
	if src.Alias != "" && (dst.Alias == "" || dst.SourceType() == model.ConnectionSourceDiscovered) {
		dst.Alias = src.Alias
	}
}

// matchRuntimeSession 按优先级匹配活跃连接：
// 1. ConnectionID（若上报包含指定 ID）
// 2. Username+Port（节点固定时稳定且无跨用户误匹配）
// 3. PID（兜底，适用于 port=0 的托管连接）
func matchRuntimeConnection(info *contract.ConnectionInfo, byID map[uint]model.Connection, byUserPort map[string]model.Connection, byPID map[string]model.Connection) (model.Connection, bool) {
	if info == nil {
		return model.Connection{}, false
	}
	if info.ConnectionID != 0 {
		if conn, ok := byID[info.ConnectionID]; ok {
			return conn, true
		}
	}
	if key := runtimeUserPortKey(info.Username, info.Port); key != "" {
		if conn, ok := byUserPort[key]; ok {
			return conn, true
		}
	}
	if info.PID != 0 {
		if conn, ok := byPID[strconv.FormatUint(uint64(info.PID), 10)]; ok {
			return conn, true
		}
	}
	return model.Connection{}, false
}

func runtimeUserPortKey(username string, port uint) string {
	if username == "" || port == 0 {
		return ""
	}
	return username + "\x00" + strconv.FormatUint(uint64(port), 10)
}
