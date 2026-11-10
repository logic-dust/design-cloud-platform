package connection

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
	"design-cloud-engine/internal/model"
)

// teardownRemoteSession ends the remote seat/login for this connection instance.
// Shared by Stop and Delete. Returns (attempted, err): attempted is true when a
// remote kill was dispatched (or needed). err != nil means kill failed.
func (s *Service) teardownRemoteSession(ctx context.Context, conn model.Connection) (attempted bool, err error) {
	switch conn.Protocol {
	case contract.VNCProtocol, contract.XRDPProtocol:
		if conn.NodeID == "" || conn.Display == nil {
			return false, nil
		}
		return true, s.dispatchSeatKill(ctx, conn)
	case contract.SSHProtocol, contract.RDPProtocol:
		if conn.NodeID == "" {
			return false, nil
		}
		return true, s.dispatchSessionStop(ctx, conn)
	default:
		return false, nil
	}
}

func (s *Service) dispatchSeatKill(ctx context.Context, c model.Connection) error {
	// Caller (teardownRemoteSession) already requires Display != nil.
	var pid uint
	if c.PID != nil {
		pid = *c.PID
	}
	switch c.Protocol {
	case contract.VNCProtocol:
		return s.dispatchKillVNC(ctx, c.NodeID, c.Username, c.ID, *c.Display)
	case contract.XRDPProtocol:
		return s.dispatchKillXRDP(ctx, c.NodeID, c.Username, c.ID, *c.Display, pid)
	default:
		return nil
	}
}

func (s *Service) dispatchKillVNC(ctx context.Context, nodeID, username string, connectionID uint, display int) error {
	if s.settingsSvc == nil || s.execSvc == nil {
		return fmt.Errorf("vnc kill unavailable: executor or settings not configured")
	}
	nodeSettings, err := s.settingsSvc.GetEffectiveAgentSettingsForNode(ctx, nodeID)
	if err != nil {
		return err
	}
	serverPath := nodeSettings.VNC.ResolvedServerPath()

	req := execution.NewExecuteRequest(execution.ActionVNCControl, fmt.Sprintf("vnc-kill-%d", time.Now().UnixNano()),
		execution.WithTargetUser(username),
		execution.WithTargetNode(nodeID),
		execution.WithPayload(payload.VNCControl{
			ConnectionID:  connectionID,
			Display:       &display,
			Operation:     "stop",
			VNCServerPath: serverPath,
			PasswdMode:    nodeSettings.VNC.PasswdMode,
		}),
		execution.WithTimeout(15*time.Second),
	)
	_, err = execution.Run(ctx, s.execSvc, req)
	return err
}

func (s *Service) dispatchKillXRDP(ctx context.Context, nodeID, username string, connectionID uint, display int, pid uint) error {
	if s.execSvc == nil {
		return fmt.Errorf("xrdp kill unavailable: executor not configured")
	}
	req := execution.NewExecuteRequest(execution.ActionXRDPControl, fmt.Sprintf("xrdp-kill-%d", time.Now().UnixNano()),
		execution.WithTargetUser(username),
		execution.WithTargetNode(nodeID),
		execution.WithPayload(payload.XRDPControl{
			ConnectionID: connectionID,
			Display:      &display,
			Operation:    "stop",
			PID:          pid,
		}),
		execution.WithTimeout(15*time.Second),
	)
	_, err := execution.Run(ctx, s.execSvc, req)
	return err
}

func (s *Service) dispatchSessionStop(ctx context.Context, conn model.Connection) error {
	if s.execSvc == nil {
		return fmt.Errorf("session stop unavailable: executor not configured")
	}
	req := execution.NewExecuteRequest(execution.ActionSessionControl, fmt.Sprintf("session-stop-%d", time.Now().UnixNano()),
		execution.WithTargetUser(conn.Username),
		execution.WithTargetNode(conn.NodeID),
		execution.WithPayload(payload.SessionControl{
			ConnectionID: conn.ID,
			Protocol:     conn.Protocol.String(),
			Operation:    "stop",
			Username:     conn.Username,
		}),
		execution.WithTimeout(30*time.Second),
	)
	_, err := execution.Run(ctx, s.execSvc, req)
	return err
}

func (s *Service) markStoppedTombstone(ctx context.Context, conn model.Connection) error {
	port := conn.Port
	switch conn.Protocol {
	case contract.VNCProtocol:
		placeholderPort, err := s.connectionRepo.PickPlaceholderPort(ctx, conn.NodeID, conn.Username)
		if err != nil {
			return fmt.Errorf("pick placeholder port: %w", err)
		}
		port = placeholderPort
	case contract.XRDPProtocol:
		port = cmp.Or(port, 3389)
	case contract.RDPProtocol:
		port = cmp.Or(port, 3389)
	case contract.SSHProtocol:
		port = cmp.Or(port, 22)
	}
	return s.connectionRepo.MarkStoppedClearDisplay(ctx, conn.ID, port)
}
