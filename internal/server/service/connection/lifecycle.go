package connection

import (
	"context"
	"fmt"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

// StartConnection 启动已停止的连接（同一 connection id 墓碑卡片，底层按 resource 重新选机）。
// - VNC：stopped→pending→running，动态号重选节点；固定 display 规格仍用其唯一节点。
// - RDP / XRDP：按 resource 重新 SelectNode，写回本行后 status=running（不原 node 符活）。
// - SSH：无实例路径；若仍有历史 SSH 行则 422，提示改走 GET /api/connections/ssh?resourceId=。
func (s *Service) StartConnection(ctx context.Context, id uint, requestUser string) error {
	conn, err := s.GetByIDForUser(ctx, id, requestUser)
	if err != nil {
		return err
	}

	if conn.Protocol == contract.SSHProtocol {
		return contract.NewValidationError("SSH does not use connection start; use GET /api/connections/ssh?resourceId=")
	}
	if conn.Status == model.ConnectionPending {
		return contract.NewConflictError(ErrConnectionStillStarting.Error(), ErrConnectionStillStarting)
	}
	if conn.Status == model.ConnectionTerminating {
		return contract.NewConflictError("connection is being deleted")
	}
	if conn.Status == model.ConnectionUnknown {
		return contract.NewConflictError(ErrConnectionStateUnknown.Error(), ErrConnectionStateUnknown)
	}
	if conn.Status == model.ConnectionRunning {
		return nil
	}

	switch conn.Protocol {
	case contract.VNCProtocol:
		return s.startVNCViaResource(ctx, conn, requestUser)
	case contract.RDPProtocol, contract.XRDPProtocol:
		return s.startDialViaResource(ctx, conn, requestUser)
	default:
		return contract.NewValidationError("unsupported protocol for start")
	}
}

func (s *Service) revertStartToStopped(ctx context.Context, conn model.Connection) {
	if err := s.markStoppedTombstone(ctx, conn); err != nil && s.logger != nil {
		s.logger.Warn("revert start to stopped failed", "connectionID", conn.ID, "error", err)
	}
}

// StopConnection ends the remote user session/seat for a connection instance, then
// leaves a stopped tombstone (resourceId kept, quota retained).
// SSH product path has no instance Stop (close the WebSocket). A leftover SSH row
// in DB may still hit SESSION_CONTROL here for admin cleanup only.
func (s *Service) StopConnection(ctx context.Context, id uint, requestUser string) error {
	conn, err := s.GetByIDForUser(ctx, id, requestUser)
	if err != nil {
		return err
	}

	switch conn.Protocol {
	case contract.VNCProtocol, contract.XRDPProtocol, contract.RDPProtocol, contract.SSHProtocol:
	default:
		return contract.NewValidationError("unsupported protocol for stop")
	}

	if conn.Status == model.ConnectionStopped {
		return nil
	}
	if conn.Status == model.ConnectionPending {
		return contract.NewConflictError(ErrConnectionStillStarting.Error(), ErrConnectionStillStarting)
	}

	if s.proxyConnectionCloser != nil {
		_ = s.proxyConnectionCloser.CloseAllByConnectionID(id)
	}

	attempted, killErr := s.teardownRemoteSession(ctx, conn)
	if killErr != nil {
		if s.logger != nil {
			s.logger.Warn("Failed to stop remote session",
				"connectionID", conn.ID, "protocol", conn.Protocol.String(),
				"nodeID", conn.NodeID, "error", killErr)
		}
		if attempted {
			if markErr := s.connectionRepo.MarkTerminatingKeepResource(ctx, conn.ID); markErr != nil {
				return fmt.Errorf("mark terminating after stop kill failure: %w", markErr)
			}
			s.notifyOwnerRefresh(ctx, requestUser, conn.Username, conn.ID)
			// Surface failure so Restart does not chain into Start on a hidden tombstone.
			return fmt.Errorf("stop remote session failed; marked terminating: %w", killErr)
		}
		return fmt.Errorf("stop remote session: %w", killErr)
	}

	if err := s.markStoppedTombstone(ctx, conn); err != nil {
		return fmt.Errorf("update connection stopped: %w", err)
	}

	s.notifyOwnerRefresh(ctx, requestUser, conn.Username, conn.ID)
	return nil
}

// RestartConnection = Stop then Start (same connection id).
func (s *Service) RestartConnection(ctx context.Context, id uint, requestUser string) error {
	if err := s.StopConnection(ctx, id, requestUser); err != nil {
		return err
	}
	return s.StartConnection(ctx, id, requestUser)
}
