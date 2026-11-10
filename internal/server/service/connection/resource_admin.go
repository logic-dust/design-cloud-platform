package connection

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

// ResourceDTO 包装用户视角的资源规格及其实例使用情况
type ResourceDTO struct {
	model.ConnectionResource
	UsedCount    int  `json:"usedCount"`
	ResolvedPort uint `json:"resolvedPort,omitempty"`
}

// resolvedResourcePort 返回实例对外拨号的端口。
// VNC 端口由 display 推导（5900+display），规格上的 port 不适用。
// XRDP 使用 RDP 监听端口（默认 3389）；display 仅表示 X 席位预留，不推导 TCP 端口。
func resolvedResourcePort(res model.ConnectionResource) uint {
	if res.Protocol == contract.VNCProtocol {
		return 0
	}
	if res.Port != nil && *res.Port > 0 {
		return *res.Port
	}
	return res.Protocol.DialDefaultPort()
}

// ListAccessibleResources 返回指定用户有权使用的连接规格列表，附带 usedCount
func (s *Service) ListAccessibleResources(ctx context.Context, username string) ([]ResourceDTO, error) {
	if s.connectionResourceRepo == nil {
		return nil, fmt.Errorf("connection resource repo uninitialized")
	}
	user, err := s.userSvc.GetDBUser(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("get user %s: %w", username, err)
	}

	resources, err := s.connectionResourceRepo.ListAccessibleForUser(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("list accessible resources: %w", err)
	}

	result := make([]ResourceDTO, 0, len(resources))
	var usedCounts map[uint]int
	if s.connectionRepo != nil {
		counts, err := s.connectionRepo.CountByUsernameGroupByResource(ctx, username)
		if err != nil {
			return nil, fmt.Errorf("count used connections: %w", err)
		}
		usedCounts = counts
	}

	for _, res := range resources {
		usedCount := 0
		if usedCounts != nil {
			usedCount = usedCounts[res.ID]
		}

		result = append(result, ResourceDTO{
			ConnectionResource: res,
			UsedCount:          usedCount,
			ResolvedPort:       resolvedResourcePort(res),
		})
	}

	return result, nil
}

// GetAccessibleResourceByID 返回指定用户有权使用的单条连接规格
func (s *Service) GetAccessibleResourceByID(ctx context.Context, username string, id uint) (ResourceDTO, error) {
	if s.connectionResourceRepo == nil {
		return ResourceDTO{}, fmt.Errorf("connection resource repo uninitialized")
	}
	user, err := s.userSvc.GetDBUser(ctx, username)
	if err != nil {
		return ResourceDTO{}, fmt.Errorf("get user %s: %w", username, err)
	}

	res, err := s.connectionResourceRepo.GetAccessibleByID(ctx, id, user.ID)
	if err != nil {
		return ResourceDTO{}, err
	}

	var usedCount int
	if s.connectionRepo != nil {
		usedCount, err = s.connectionRepo.CountByUsernameAndResource(ctx, username, res.ID)
		if err != nil {
			return ResourceDTO{}, fmt.Errorf("count used connections: %w", err)
		}
	}
	return ResourceDTO{
		ConnectionResource: res,
		UsedCount:          usedCount,
		ResolvedPort:       resolvedResourcePort(res),
	}, nil
}

// Admin Resource CRUD & user mappings delegation
func (s *Service) AdminListResources(ctx context.Context, limit, offset int, protocol, nameLike string) ([]model.ConnectionResource, int64, error) {
	if s.connectionResourceRepo == nil {
		return nil, 0, fmt.Errorf("connection resource repo uninitialized")
	}
	return s.connectionResourceRepo.List(ctx, limit, offset, protocol, nameLike)
}

func (s *Service) AdminGetResourceByID(ctx context.Context, id uint) (model.ConnectionResource, error) {
	if s.connectionResourceRepo == nil {
		return model.ConnectionResource{}, fmt.Errorf("connection resource repo uninitialized")
	}
	return s.connectionResourceRepo.GetByID(ctx, id)
}

func mapResourceStoreError(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, db.ErrResourceNameExists) {
		return contract.NewConflictError("connection resource name already exists", ErrResourceNameConflict)
	}
	return fmt.Errorf("%s connection resource: %w", op, err)
}

func (s *Service) ensureResourceNameAvailable(ctx context.Context, name string, excludeID uint) error {
	existing, err := s.connectionResourceRepo.GetByName(ctx, name)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return nil
		}
		return err
	}
	if excludeID != 0 && existing.ID == excludeID {
		return nil
	}
	return contract.NewConflictError("connection resource name already exists", ErrResourceNameConflict)
}

func (s *Service) AdminCreateResource(ctx context.Context, res *model.ConnectionResource) error {
	if s.connectionResourceRepo == nil {
		return fmt.Errorf("connection resource repo uninitialized")
	}
	// id 与时间戳由服务端维护，不接受客户端在请求体中指定。
	res.ID = 0
	res.CreatedAt = time.Time{}
	res.UpdatedAt = time.Time{}
	if err := s.rejectInvalidResource(ctx, res); err != nil {
		return err
	}
	if err := s.ensureResourceNameAvailable(ctx, res.Name, 0); err != nil {
		return err
	}
	return mapResourceStoreError("create", s.connectionResourceRepo.Create(ctx, res))
}

func (s *Service) AdminUpdateResource(ctx context.Context, res *model.ConnectionResource) error {
	if s.connectionResourceRepo == nil {
		return fmt.Errorf("connection resource repo uninitialized")
	}
	if _, err := s.connectionResourceRepo.GetByID(ctx, res.ID); err != nil {
		return err
	}
	if err := s.rejectInvalidResource(ctx, res); err != nil {
		return err
	}
	if err := s.ensureResourceNameAvailable(ctx, res.Name, res.ID); err != nil {
		return err
	}
	return mapResourceStoreError("update", s.connectionResourceRepo.Update(ctx, res))
}

const (
	maxResourceBatchItems = 100
	maxResourceNameLen    = 128 // 与列 varchar(128) 对齐
	maxResourceNodeIDLen  = 64  // Node.NodeID 为 varchar(64)
	maxResourcePort       = 65535
)

// BatchResourceUpsertFailure is one failed item in AdminBatchUpsertResources.
type BatchResourceUpsertFailure struct {
	Index   int    `json:"index"`
	ID      uint   `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
}

// BatchResourceUpsertResponse is the partial-success result of AdminBatchUpsertResources.
type BatchResourceUpsertResponse struct {
	Created  int                          `json:"created"`
	Updated  int                          `json:"updated"`
	Failures []BatchResourceUpsertFailure `json:"failures,omitempty"`
}

// AdminBatchUpsertResources creates or updates connection resources; continues after item failures.
//
// Identity is name-scoped for catalog sync:
//   - id == 0: upsert by name (existing name → update that row; missing → create).
//   - id > 0: update by primary key (rename still subject to name uniqueness).
//
// Soft “looks alike” duplicate detection (same users/hosts/display across different names)
// is intentionally not enforced here; clients may warn only.
func (s *Service) AdminBatchUpsertResources(ctx context.Context, items []model.ConnectionResource) (*BatchResourceUpsertResponse, error) {
	if s.connectionResourceRepo == nil {
		return nil, fmt.Errorf("connection resource repo uninitialized")
	}
	if len(items) == 0 {
		return nil, contract.NewInvalidParamError("items is required")
	}
	if len(items) > maxResourceBatchItems {
		return nil, contract.NewInvalidParamError(fmt.Sprintf("items exceeds limit of %d", maxResourceBatchItems))
	}
	out := &BatchResourceUpsertResponse{}
	for i := range items {
		item := items[i]
		fail := func(msg string) {
			out.Failures = append(out.Failures, BatchResourceUpsertFailure{
				Index:   i,
				ID:      item.ID,
				Name:    item.Name,
				Message: msg,
			})
		}
		if item.ID == 0 {
			existing, err := s.connectionResourceRepo.GetByName(ctx, item.Name)
			if err != nil && !errors.Is(err, contract.ErrNotFound) {
				fail(err.Error())
				continue
			}
			if err == nil {
				item.ID = existing.ID
				if err := s.AdminUpdateResource(ctx, &item); err != nil {
					fail(err.Error())
					continue
				}
				items[i] = item
				out.Updated++
				continue
			}
			if err := s.AdminCreateResource(ctx, &item); err != nil {
				fail(err.Error())
				continue
			}
			items[i] = item
			out.Created++
			continue
		}
		if err := s.AdminUpdateResource(ctx, &item); err != nil {
			fail(err.Error())
			continue
		}
		items[i] = item
		out.Updated++
	}
	return out, nil
}

func (s *Service) rejectInvalidResource(ctx context.Context, res *model.ConnectionResource) error {
	if s.settingsSvc == nil {
		return fmt.Errorf("connection settings service is not configured")
	}
	cap, err := s.settingsSvc.MaxPerUserCap(ctx)
	if err != nil {
		return err
	}
	if res != nil {
		// 规范化后再校验，避免仅含空白字符的输入绕过必填判断。
		res.Name = strings.TrimSpace(res.Name)
		for i := range res.Nodes {
			res.Nodes[i] = strings.TrimSpace(res.Nodes[i])
		}
		for i := range res.NodeGroups {
			res.NodeGroups[i] = strings.TrimSpace(res.NodeGroups[i])
		}
	}
	if err := validateResource(res, cap); err != nil {
		return err
	}
	return s.validateResourceCandidates(ctx, res)
}

// validateResourceCandidates 校验候选节点确实存在。
// 节点组没有独立实体，只校验命名语法，允许先建规格、后登记节点（预配置场景）。
func (s *Service) validateResourceCandidates(ctx context.Context, res *model.ConnectionResource) error {
	if s.nodeRepo == nil || res == nil {
		return nil
	}
	for _, nodeID := range res.Nodes {
		if _, err := s.nodeRepo.GetByNodeID(ctx, nodeID); err != nil {
			if errors.Is(err, contract.ErrNotFound) {
				return contract.NewInvalidParamError(fmt.Sprintf("node %q does not exist", nodeID))
			}
			return err
		}
	}
	return nil
}

func validateResource(res *model.ConnectionResource, maxPerUserCap int) error {
	if res == nil {
		return contract.NewInvalidParamError("connection resource is required")
	}
	name := strings.TrimSpace(res.Name)
	if name == "" {
		return contract.NewInvalidParamError("name is required")
	}
	if utf8.RuneCountInString(name) > maxResourceNameLen {
		return contract.NewInvalidParamError(fmt.Sprintf("name cannot exceed %d characters", maxResourceNameLen))
	}
	if strings.ContainsAny(name, "\x00\r\n\t") {
		return contract.NewInvalidParamError("name contains invalid control characters")
	}
	if err := model.ValidateConnectionResourceName(name); err != nil {
		return contract.NewInvalidParamError(err.Error())
	}
	if !res.Protocol.Valid() {
		return contract.NewInvalidParamError("protocol is required")
	}
	if len(res.Nodes) == 0 && len(res.NodeGroups) == 0 {
		return contract.NewInvalidParamError("candidate nodes or node groups cannot both be empty")
	}
	if err := validateResourceNodeIDs(res.Nodes); err != nil {
		return err
	}
	if err := validateResourceNodeGroups(res.NodeGroups); err != nil {
		return err
	}
	if res.MaxPerUser < 1 || res.MaxPerUser > maxPerUserCap {
		return contract.NewInvalidParamError(fmt.Sprintf("maxPerUser must be between 1 and %d", maxPerUserCap))
	}
	if res.MaxTotal < 0 {
		return contract.NewInvalidParamError("maxTotal cannot be negative")
	}
	if res.MaxTotal > 0 && res.MaxTotal < res.MaxPerUser {
		return contract.NewInvalidParamError(fmt.Sprintf("maxTotal (%d) cannot be less than maxPerUser (%d)", res.MaxTotal, res.MaxPerUser))
	}
	if res.Display != nil {
		if !res.Protocol.UsesXDisplay() {
			return contract.NewInvalidParamError("display is only allowed for VNC or XRDP protocol")
		}
		if *res.Display < dynamicDisplayMin || *res.Display > dynamicDisplayMax {
			return contract.NewInvalidParamError(fmt.Sprintf("display must be between %d and %d", dynamicDisplayMin, dynamicDisplayMax))
		}
		if len(res.Nodes) != 1 || len(res.NodeGroups) > 0 {
			return contract.NewInvalidParamError("fixed display is only allowed when exactly one node is configured and nodeGroups is empty")
		}
	}
	if res.Port != nil {
		if res.Protocol == contract.VNCProtocol {
			return contract.NewInvalidParamError("port is not supported for VNC; VNC port is derived from display")
		}
		if *res.Port == 0 || *res.Port > maxResourcePort {
			return contract.NewInvalidParamError(fmt.Sprintf("port must be between 1 and %d", maxResourcePort))
		}
	}
	if res.Protocol == contract.VNCProtocol && len(res.VNCConfig) > 0 {
		// vncConfig 必须是对象；否则会在启动时解析失败。
		var vncCfg struct {
			StartOptions string `json:"startOptions"`
			Resolution   string `json:"resolution"`
			UseCgroup    *bool  `json:"useCgroup"`
		}
		if err := json.Unmarshal(res.VNCConfig, &vncCfg); err != nil {
			return contract.NewInvalidParamError("vncConfig must be a JSON object with resolution/startOptions/useCgroup")
		}
	}
	return nil
}

func validateResourceNodeIDs(nodes []string) error {
	seen := make(map[string]struct{}, len(nodes))
	for _, nodeID := range nodes {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" {
			return contract.NewInvalidParamError("nodes cannot contain empty entries")
		}
		if utf8.RuneCountInString(nodeID) > maxResourceNodeIDLen {
			return contract.NewInvalidParamError(fmt.Sprintf("node id %q cannot exceed %d characters", nodeID, maxResourceNodeIDLen))
		}
		if _, ok := seen[nodeID]; ok {
			return contract.NewInvalidParamError(fmt.Sprintf("duplicate node id %q", nodeID))
		}
		if err := model.ValidateNodeID(nodeID); err != nil {
			return contract.NewInvalidParamError(err.Error())
		}
		seen[nodeID] = struct{}{}
	}
	return nil
}

func validateResourceNodeGroups(groups []string) error {
	seen := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			return contract.NewInvalidParamError("nodeGroups cannot contain empty entries")
		}
		if err := model.ValidateNodeGroupName(group); err != nil {
			return contract.NewInvalidParamError(err.Error())
		}
		if _, ok := seen[group]; ok {
			return contract.NewInvalidParamError(fmt.Sprintf("duplicate node group %q", group))
		}
		seen[group] = struct{}{}
	}
	return nil
}

func (s *Service) AdminDeleteResource(ctx context.Context, id uint) error {
	if s.connectionResourceRepo == nil {
		return fmt.Errorf("connection resource repo uninitialized")
	}
	err := s.connectionResourceRepo.Delete(ctx, id)
	if err != nil && errors.Is(err, db.ErrResourceInUse) {
		return contract.NewConflictError(ErrResourceInUse.Error(), ErrResourceInUse)
	}
	return err
}

func (s *Service) AdminListResourceUsers(ctx context.Context, resourceID uint) ([]model.User, error) {
	if s.connectionResourceRepo == nil {
		return nil, fmt.Errorf("connection resource repo uninitialized")
	}
	res, err := s.connectionResourceRepo.GetByID(ctx, resourceID)
	if err != nil {
		return nil, err
	}
	if res.Public {
		return []model.User{}, nil
	}
	return s.connectionResourceRepo.ListUsers(ctx, resourceID)
}

func (s *Service) AdminListResourceUsersPage(ctx context.Context, resourceID uint, limit, offset int) ([]model.User, int64, error) {
	if s.connectionResourceRepo == nil {
		return nil, 0, fmt.Errorf("connection resource repo uninitialized")
	}
	res, err := s.connectionResourceRepo.GetByID(ctx, resourceID)
	if err != nil {
		return nil, 0, err
	}
	if res.Public {
		return []model.User{}, 0, nil
	}
	return s.connectionResourceRepo.ListUsersPage(ctx, resourceID, limit, offset)
}

func (s *Service) AdminReplaceResourceUsers(ctx context.Context, resourceID uint, userIDs []uint) error {
	if s.connectionResourceRepo == nil {
		return fmt.Errorf("connection resource repo uninitialized")
	}
	res, err := s.connectionResourceRepo.GetByID(ctx, resourceID)
	if err != nil {
		return err
	}
	if err := ensureMutableResourceMappings(res); err != nil {
		return err
	}
	normalized, err := s.normalizeResourceUserIDs(ctx, userIDs, true)
	if err != nil {
		return err
	}
	current, err := s.connectionResourceRepo.ListUsers(ctx, resourceID)
	if err != nil {
		return err
	}
	keep := make(map[uint]struct{}, len(normalized))
	for _, id := range normalized {
		keep[id] = struct{}{}
	}
	var removed []string
	for _, user := range current {
		if _, ok := keep[user.ID]; !ok {
			removed = append(removed, user.Username)
		}
	}
	if err := s.connectionResourceRepo.ReplaceUsers(ctx, resourceID, normalized); err != nil {
		return err
	}
	s.closeProxyConnectionsForResourceUsers(ctx, resourceID, removed)
	return nil
}

func (s *Service) AdminPatchResourceUsers(ctx context.Context, resourceID uint, addIDs, removeIDs []uint) error {
	if s.connectionResourceRepo == nil {
		return fmt.Errorf("connection resource repo uninitialized")
	}
	res, err := s.connectionResourceRepo.GetByID(ctx, resourceID)
	if err != nil {
		return err
	}
	if err := ensureMutableResourceMappings(res); err != nil {
		return err
	}
	normalizedAdd, err := s.normalizeResourceUserIDs(ctx, addIDs, true)
	if err != nil {
		return err
	}
	// 移除不校验用户存在性，允许清理指向已删除用户的悬挂映射。
	normalizedRemove, err := s.normalizeResourceUserIDs(ctx, removeIDs, false)
	if err != nil {
		return err
	}
	removeSet := make(map[uint]struct{}, len(normalizedRemove))
	for _, id := range normalizedRemove {
		removeSet[id] = struct{}{}
	}
	for _, id := range normalizedAdd {
		if _, ok := removeSet[id]; ok {
			return contract.NewInvalidParamError(fmt.Sprintf("user %d cannot be both added and removed", id))
		}
	}

	current, err := s.connectionResourceRepo.ListUsers(ctx, resourceID)
	if err != nil {
		return err
	}
	var removed []string
	for _, user := range current {
		if _, ok := removeSet[user.ID]; ok {
			removed = append(removed, user.Username)
		}
	}
	if err := s.connectionResourceRepo.PatchUsers(ctx, resourceID, normalizedAdd, normalizedRemove); err != nil {
		return err
	}
	s.closeProxyConnectionsForResourceUsers(ctx, resourceID, removed)
	return nil
}

func ensureMutableResourceMappings(res model.ConnectionResource) error {
	if res.Public {
		return contract.NewInvalidParamError("public resource does not accept user mappings")
	}
	return nil
}

// normalizeResourceUserIDs 去重；requireExisting 为 true 时同时校验用户存在，避免悬挂映射。
func (s *Service) normalizeResourceUserIDs(ctx context.Context, userIDs []uint, requireExisting bool) ([]uint, error) {
	out := make([]uint, 0, len(userIDs))
	seen := make(map[uint]struct{}, len(userIDs))
	for _, id := range userIDs {
		if id == 0 {
			return nil, contract.NewInvalidParamError("user id must be positive")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		if requireExisting && s.userSvc != nil {
			if _, err := s.userSvc.GetDBUserByID(ctx, id); err != nil {
				if errors.Is(err, contract.ErrNotFound) {
					return nil, contract.NewInvalidParamError(fmt.Sprintf("user %d does not exist", id))
				}
				return nil, err
			}
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func (s *Service) closeProxyConnectionsForResourceUsers(ctx context.Context, resourceID uint, usernames []string) {
	if s.proxyConnectionCloser == nil || len(usernames) == 0 || s.connectionRepo == nil {
		return
	}
	want := make(map[string]struct{}, len(usernames))
	for _, username := range usernames {
		want[username] = struct{}{}
	}
	conns, err := s.connectionRepo.ListByResource(ctx, resourceID)
	if err != nil {
		return
	}
	for _, conn := range conns {
		if _, ok := want[conn.Username]; !ok {
			continue
		}
		_ = s.proxyConnectionCloser.CloseAllByConnectionID(conn.ID)
	}
}
