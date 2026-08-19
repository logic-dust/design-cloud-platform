package connection

import (
	"context"
	"fmt"
	"io/fs"
	"strconv"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/common/crypto"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

const AllUsers = "all"

// formatConnID formats a connection ID for event payloads.
func formatConnID(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

// ListAll returns paginated connections for admin listing (no cache).
func (s *Service) ListAll(ctx context.Context, limit, offset int) ([]model.Connection, int64, error) {
	return s.connectionRepo.List(ctx, limit, offset)
}

// ListByUser get connections of a user, served through the settings-driven TTL cache.
func (s *Service) ListByUser(ctx context.Context, user string) ([]model.Connection, error) {
	if user == AllUsers {
		conns, _, err := s.connectionRepo.List(ctx, 50, 0)
		if err != nil {
			return nil, fmt.Errorf("list all connections: %w", err)
		}
		return conns, nil
	}
	return s.connListCache.GetWithTTL(ctx, user, s.listCacheTTL(ctx), func(ctx context.Context, u string) ([]model.Connection, error) {
		conns, err := s.connectionRepo.ListByUser(ctx, u)
		if err != nil {
			return nil, fmt.Errorf("list connections for user %s: %w", u, err)
		}
		return conns, nil
	})
}

// listCacheTTL 返回连接列表缓存 TTL：跟随 connection 域 ConnectionListCacheTTL
// 设置（默认 60s，可配 15-1800s），每次调用动态读取以支持热更新。
//
// 读失败/未配置/未注入 settings 时返回 0，由 TTLCache 回退到构造时设定的代码
// 默认值——"TTL<=0 走缓存级默认"是 cache 自身的契约，此处不重复一份默认值。
func (s *Service) listCacheTTL(ctx context.Context) time.Duration {
	if s.settingsSvc == nil {
		return 0
	}
	settings, err := s.settingsSvc.GetConnectionSettings(ctx)
	if err != nil {
		return 0
	}
	return time.Duration(settings.ConnectionListCacheTTL) * time.Second
}

// invalidateConnListCache removes a user's cached connection list so the next
// read hits the DB. Called after create / delete / collect.
// Shared connections (username == "") appear in every user's list, so the
// entire cache is cleared instead of a single key.
func (s *Service) invalidateConnListCache(username string) {
	if username == "" {
		s.connListCache.Clear()
		return
	}
	s.connListCache.Invalidate(username)
}

// GetByID returns connection by ID
func (s *Service) GetByID(ctx context.Context, id uint) (model.Connection, error) {
	conn, err := s.connectionRepo.GetByID(ctx, id)
	if err != nil {
		return model.Connection{}, fmt.Errorf("get connection %d: %w", id, err)
	}
	return conn, nil
}

func (s *Service) GetByIDForUser(ctx context.Context, id uint, username string) (model.Connection, error) {
	conn, err := s.GetByID(ctx, id)
	if err != nil {
		return model.Connection{}, err
	}
	if !canAccessConnection(username, conn) {
		return model.Connection{}, fs.ErrPermission
	}
	return conn, nil
}

// CreateManual creates a user-owned connection record that is not tied to an app submission.
func (s *Service) CreateManual(ctx context.Context, username string, form CreateManualConnectionForm) (model.Connection, error) {
	form, err := form.normalize()
	if err != nil {
		return model.Connection{}, err
	}

	conn := model.Connection{
		Name:         "manual-" + crypto.GenRandomId(16),
		Alias:        form.Alias,
		Username:     username,
		Host:         form.Host,
		Port:         form.Port,
		TargetNodeID: form.TargetNodeID,
		Status:       model.ConnectionUnknown,
		Source:       model.ConnectionSourceManual,
		Protocol:     form.Protocol,
	}
	if conn.Username == "" {
		return model.Connection{}, fmt.Errorf("%w: username is required", fs.ErrInvalid)
	}

	quota, err := s.effectiveMaxConnections(ctx, username)
	if err != nil {
		return model.Connection{}, fmt.Errorf("check connection quota: %w", err)
	}

	if err := s.connectionRepo.CreateWithQuota(ctx, &conn, quota); err != nil {
		if common.IsUniqueConstraintError(err) {
			return model.Connection{}, fmt.Errorf("%w: host=%s port=%d", ErrConnectionAlreadyExists, conn.Host, conn.Port)
		}
		return model.Connection{}, fmt.Errorf("create manual connection: %w", err)
	}
	s.invalidateConnListCache(username)

	// 手动创建是同步链路：操作者从 response 拿到完整 conn，无需推送
	return conn, nil
}

// CreateFixed creates an admin-assigned fixed connection record.
func (s *Service) CreateFixed(ctx context.Context, form CreateFixedConnectionForm) (model.Connection, error) {
	form, err := form.normalize()
	if err != nil {
		return model.Connection{}, err
	}

	conn, err := s.createFixedFromForm(ctx, form)
	if err != nil {
		if common.IsUniqueConstraintError(err) {
			return model.Connection{}, fmt.Errorf("%w: host=%s port=%d", ErrConnectionAlreadyExists, form.Host, form.Port)
		}
		return model.Connection{}, err
	}

	s.invalidateConnListCache(form.Username)

	// 共享连接对所有用户可见：广播刷新；私有连接调用方同步拿到 conn，无需推送
	if form.Shared {
		s.notifyOwnerRefresh(ctx, "", conn.ID)
	}

	return conn, nil
}

// updateStatus patches a connection record's mutable fields based on the
// ConnectionInfo payload and sets its status. It is the shared primitive
// used by event handling, refresh, and sync flows.
func (s *Service) updateStatus(ctx context.Context, connInfo *contract.ConnectionInfo, targetNodeID string, newStatus model.ConnectionStatus) error {
	if connInfo.ConnectionID == 0 {
		return fmt.Errorf("connection ID is required")
	}
	connectionRecord, err := s.connectionRepo.GetByID(ctx, connInfo.ConnectionID)
	if err != nil {
		return fmt.Errorf("failed to fetch connection: %w", err)
	}

	fields := make(map[string]any)
	if connectionRecord.TargetNodeID == "" && targetNodeID != "" {
		fields["target_node_id"] = targetNodeID
	}
	if connInfo.Name != "" && connectionRecord.Alias == "" {
		fields["alias"] = connInfo.Name
	}
	if connInfo.Username != "" {
		fields["username"] = connInfo.Username
	}
	if connInfo.Host != "" {
		fields["host"] = connInfo.Host
	}
	if connInfo.Display != "" {
		fields["display"] = connInfo.Display
	}
	if connInfo.Port != 0 {
		fields["port"] = connInfo.Port
	}
	if connInfo.PID != 0 {
		fields["pid"] = connInfo.PID
	}
	if connInfo.Protocol != "" {
		fields["protocol"] = connInfo.Protocol
	}
	fields["status"] = newStatus
	if err := s.connectionRepo.UpdateFields(ctx, connectionRecord.ID, fields); err != nil {
		return fmt.Errorf("update connection %d fields: %w", connectionRecord.ID, err)
	}

	// 状态变化才推送（变化驱动）：agent 上报/系统回收等异步链路中，归属用户
	// 不在请求链路上，需要得知状态跳变；状态未变（重复上报）零开销。
	if connectionRecord.Status != newStatus {
		s.notifyOwnerRefresh(ctx, connectionRecord.Username, connectionRecord.ID)
	}
	return nil
}

func canAccessConnection(username string, conn model.Connection) bool {
	if username == AllUsers {
		return true
	}
	if conn.Shared {
		return true
	}
	return conn.Username == username
}
