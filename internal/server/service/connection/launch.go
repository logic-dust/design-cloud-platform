package connection

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

// dynamicDisplayMin/Max 传给 Agent 动态选号范围；Server 不在 DB 里预选 display。
const (
	dynamicDisplayMin = 1
	dynamicDisplayMax = 99
)

// Launch 从规格创建并拉起新连接实例。
func (s *Service) Launch(ctx context.Context, username string, resourceID uint) (model.Connection, error) {
	if s.connectionResourceRepo == nil {
		return model.Connection{}, fmt.Errorf("connection resource repo uninitialized")
	}

	// 1. 获取门户用户以校验身份与 user_id
	user, err := s.userSvc.GetDBUser(ctx, username)
	if err != nil {
		return model.Connection{}, fmt.Errorf("get user %s: %w", username, err)
	}

	// 2. 鉴权：public=true 或存在 (user_id, resource_id) 映射
	res, err := s.connectionResourceRepo.GetAccessibleByID(ctx, resourceID, user.ID)
	if err != nil {
		return model.Connection{}, fmt.Errorf("resource %d not accessible: %w", resourceID, err)
	}

	// 3. 协议分支与选机。VNC 在事务里占配额（固定 display 占号，动态 display=NULL pending），再经 Agent 拉起。
	var conn model.Connection
	switch res.Protocol {
	case contract.VNCProtocol:
		launched, err := s.launchVNC(ctx, username, resourceID, res)
		if err != nil {
			return model.Connection{}, err
		}
		conn = launched

	case contract.RDPProtocol, contract.XRDPProtocol:
		// XRDP: dial registration like RDP (system xrdp listens on 3389).
		// Display on the resource reserves an X seat for avoid; seats appear after
		// RDP login and are reconciled/killed via connection-probe / XRDP_CONTROL.
		launched, err := s.launchDial(ctx, username, resourceID, res)
		if err != nil {
			return model.Connection{}, err
		}
		conn = launched

	case contract.SSHProtocol:
		// Not the SSH start path — product uses GET /api/connections/ssh?resourceId=.
		// 422 blocks POST /api/connections so old clients cannot create SSH rows.
		return model.Connection{}, contract.NewValidationError("SSH does not create connection instances; use GET /api/connections/ssh?resourceId=")

	default:
		return model.Connection{}, fmt.Errorf("unsupported protocol %q", res.Protocol.String())
	}

	s.notifyOwnerRefresh(ctx, username, conn.Username, conn.ID)
	s.attachDialHost(ctx, &conn)
	return conn, nil
}

func (s *Service) launchVNC(ctx context.Context, username string, resourceID uint, res model.ConnectionResource) (model.Connection, error) {
	if res.Display != nil {
		if len(res.Nodes) != 1 {
			return model.Connection{}, fmt.Errorf("fixed display requires exactly one node")
		}
		nodeID := res.Nodes[0]
		node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
		if err != nil {
			return model.Connection{}, fmt.Errorf("get target node %s: %w", nodeID, err)
		}
		if node.Status == model.NodeStatusOffline || node.Status == model.NodeStatusMaintenance || !node.AllowConnections || node.OS != "linux" {
			return model.Connection{}, ErrTargetNodeUnavailable
		}
		connName := model.ConnectionName(res.Name, node.NodeID)
		return s.claimAndLaunchVNC(ctx, username, resourceID, connName, node, res.Display, res.VNCConfig)
	}

	nodes, err := s.placer.RankedNodes(ctx, res, nil)
	if err != nil {
		return model.Connection{}, err
	}
	var lastStartErr error
	sawNoDisplay := false
	for _, node := range nodes {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return model.Connection{}, ctxErr
		}
		connName := model.ConnectionName(res.Name, node.NodeID)
		conn, err := s.claimAndLaunchVNC(ctx, username, resourceID, connName, node, nil, res.VNCConfig)
		if err == nil {
			return conn, nil
		}
		if errors.Is(err, db.ErrQuotaExceeded) {
			return model.Connection{}, err
		}
		if errors.Is(err, ErrDisplayExhausted) {
			sawNoDisplay = true
			continue
		}
		// 保留节点上下文，避免多节点重试后 last error 掩盖真实失败点。
		lastStartErr = fmt.Errorf("node %s: %w", node.NodeID, err)
	}
	if lastStartErr != nil {
		return model.Connection{}, lastStartErr
	}
	if sawNoDisplay {
		return model.Connection{}, ErrDisplayExhausted
	}
	// RankedNodes should have erred already; keep a specific sentinel if the loop was a no-op.
	return model.Connection{}, ErrNoHealthyNode
}

// claimAndLaunchVNC 先提交 pending 占坑行，动态 display 由 Agent 选号后回写。
func (s *Service) claimAndLaunchVNC(ctx context.Context, username string, resourceID uint, connName string, node model.Node, fixed *int, vncConfig json.RawMessage) (model.Connection, error) {
	now := time.Now()
	pending := &model.Connection{
		ResourceID:     &resourceID,
		Name:           connName,
		Username:       username,
		Protocol:       contract.VNCProtocol,
		NodeID:         node.NodeID,
		LastAccessedAt: &now,
	}

	if fixed != nil {
		if err := s.connectionRepo.ClaimVNC(ctx, pending, *fixed); err != nil {
			if errors.Is(err, db.ErrDisplayOccupied) {
				return model.Connection{}, ErrDisplayOccupied
			}
			return model.Connection{}, err
		}
	} else {
		if err := s.connectionRepo.ClaimVNCPending(ctx, pending); err != nil {
			return model.Connection{}, err
		}
	}

	var avoid []int
	var err error
	if fixed == nil {
		avoid, err = s.buildAvoidDisplays(ctx, node.NodeID, resourceID)
		if err != nil {
			s.releaseOwnClaim(ctx, pending.ID)
			return model.Connection{}, err
		}
	}

	chosenDisplay, port, pid, err := s.launchVNCOnNode(ctx, node, username, pending.ID, fixed, avoid, vncConfig)
	if err != nil {
		s.releaseOwnClaim(ctx, pending.ID)
		if errors.Is(err, ErrDisplayExhausted) {
			return model.Connection{}, err
		}
		if errors.Is(err, ErrDisplayConflict) {
			return model.Connection{}, err
		}
		return model.Connection{}, fmt.Errorf("launch vnc server failed: %w", err)
	}

	var pidPtr *uint
	if pid > 0 {
		pidPtr = &pid
	}

	if fixed != nil {
		if err := s.connectionRepo.FinalizeClaim(ctx, pending.ID, port, pidPtr, time.Now()); err != nil {
			s.rollbackLaunchedVNC(ctx, node.NodeID, username, pending.ID, chosenDisplay)
			s.releaseOwnClaim(ctx, pending.ID)
			if db.IsUniqueConstraintError(err) {
				return model.Connection{}, ErrDisplayOccupied
			}
			return model.Connection{}, err
		}
	} else {
		if err := s.connectionRepo.FinalizeVNCPending(ctx, pending.ID, chosenDisplay, port, pidPtr, time.Now()); err != nil {
			s.rollbackLaunchedVNC(ctx, node.NodeID, username, pending.ID, chosenDisplay)
			s.releaseOwnClaim(ctx, pending.ID)
			if db.IsUniqueConstraintError(err) {
				return model.Connection{}, ErrDisplayConflict
			}
			return model.Connection{}, err
		}
	}

	pending.Display = &chosenDisplay
	pending.Port = port
	pending.PID = pidPtr
	pending.Status = model.ConnectionRunning
	return *pending, nil
}

func (s *Service) releaseOwnClaim(ctx context.Context, id uint) {
	if err := s.connectionRepo.ReleaseClaim(ctx, id); err != nil && s.logger != nil {
		s.logger.Warn("release vnc claim failed", "connectionID", id, "error", err)
	}
}

// buildAvoidDisplays returns fixed displays reserved by other VNC resources on the node.
// Dynamic selection is authoritative on the agent (scanX); connection rows are not consulted.
func (s *Service) buildAvoidDisplays(ctx context.Context, nodeID string, resourceID uint) ([]int, error) {
	if s.connectionResourceRepo == nil {
		return nil, nil
	}
	return s.connectionResourceRepo.ListAvoidDisplaysForLaunch(ctx, nodeID, resourceID)
}
