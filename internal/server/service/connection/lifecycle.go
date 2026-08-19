package connection

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
)

// vncControlOperation is the payload operation for ActionVNCControl.
type vncControlOperation string

const (
	vncOpStart vncControlOperation = "start"
	vncOpStop  vncControlOperation = "stop"
)

// validateVNCControlEligibility checks that a connection can be started or
// stopped via the launcher: only admin-assigned Fixed VNC connections with a
// known target node and display are controllable.
func validateVNCControlEligibility(conn model.Connection) error {
	if conn.Source != model.ConnectionSourceFixed {
		return fmt.Errorf("%w: only fixed connections support start/stop", fs.ErrInvalid)
	}
	if conn.Protocol != contract.VNCProtocol {
		return fmt.Errorf("%w: only VNC connections support start/stop", fs.ErrInvalid)
	}
	if conn.TargetNodeID == "" {
		return fmt.Errorf("%w: connection target node is required", fs.ErrInvalid)
	}
	if conn.Display == "" {
		return fmt.Errorf("%w: connection display is required", fs.ErrInvalid)
	}
	return nil
}

// StartConnection starts the VNC server of a fixed connection on its assigned
// node, then optimistically marks the connection running.
func (s *Service) StartConnection(ctx context.Context, id uint, requestUser string) error {
	s.markControlled(id)
	return s.controlConnection(ctx, id, requestUser, vncOpStart, model.ConnectionRunning)
}

// StopConnection stops the VNC server of a fixed connection on its assigned
// node, then marks the connection stopped and terminates any active proxy session.
func (s *Service) StopConnection(ctx context.Context, id uint, requestUser string) error {
	s.markControlled(id)
	if s.tunnelCloser != nil {
		_ = s.tunnelCloser.CloseByConnectionID(id)
	}
	return s.controlConnection(ctx, id, requestUser, vncOpStop, model.ConnectionStopped)
}

// RestartConnection stops and then starts the VNC server of a fixed connection.
func (s *Service) RestartConnection(ctx context.Context, id uint, requestUser string) error {
	s.markControlled(id)
	if s.tunnelCloser != nil {
		_ = s.tunnelCloser.CloseByConnectionID(id)
	}
	conn, err := s.GetByIDForUser(ctx, id, requestUser)
	if err != nil {
		return err
	}
	if err := validateVNCControlEligibility(conn); err != nil {
		return err
	}
	if err := s.dispatchVNCControl(ctx, conn, vncOpStop); err != nil {
		return err
	}
	if err := s.dispatchVNCControl(ctx, conn, vncOpStart); err != nil {
		return err
	}
	return s.updateStatus(ctx, &contract.ConnectionInfo{ConnectionID: conn.ID}, conn.TargetNodeID, model.ConnectionRunning)
}

// controlConnection validates the connection and dispatches a single
// start/stop operation.
func (s *Service) controlConnection(ctx context.Context, id uint, requestUser string, op vncControlOperation, status model.ConnectionStatus) error {
	conn, err := s.GetByIDForUser(ctx, id, requestUser)
	if err != nil {
		return err
	}
	if err := validateVNCControlEligibility(conn); err != nil {
		return err
	}
	if err := s.dispatchVNCControl(ctx, conn, op); err != nil {
		return err
	}
	return s.updateStatus(ctx, &contract.ConnectionInfo{ConnectionID: conn.ID}, conn.TargetNodeID, status)
}

// dispatchVNCControl sends the start/stop action to the connection's node.
func (s *Service) dispatchVNCControl(ctx context.Context, conn model.Connection, op vncControlOperation) error {
	if s.execSvc == nil {
		return fmt.Errorf("connection execution service is not configured")
	}
	if s.settingsSvc == nil {
		return fmt.Errorf("settings service is uninitialized in connection service")
	}
	nodeSettings, err := s.settingsSvc.GetEffectiveAgentSettingsForNode(ctx, conn.TargetNodeID)
	if err != nil {
		return fmt.Errorf("resolve effective agent settings for node %q: %w", conn.TargetNodeID, err)
	}

	serverPath := nodeSettings.VNC.ResolvedServerPath()
	if serverPath == "" {
		return fmt.Errorf("%w: vnc server binary path is empty/unresolved for node %q", fs.ErrInvalid, conn.TargetNodeID)
	}

	serverArgs := nodeSettings.VNC.ServerArgs
	if startOpts := strings.TrimSpace(conn.GetStartOptions()); startOpts != "" {
		serverArgs = strings.Fields(startOpts)
	}

	actionName := execution.ActionVNCControl
	if op == vncOpStart && conn.NeedsElevation() {
		actionName = execution.ActionVNCCgroupControl
	}

	executionID := fmt.Sprintf("vnc-control-%d-%d", conn.ID, time.Now().UnixNano())
	req := execution.NewExecuteRequest(actionName, executionID,
		execution.WithTargetUser(conn.Username),
		execution.WithTargetNode(conn.TargetNodeID),
		execution.WithPayload(payload.VNCControl{
			ConnectionID:  conn.ID,
			Display:       conn.Display,
			Operation:     string(op),
			VNCServerPath: serverPath,
			StartOptions:  conn.GetStartOptions(),
			ServerArgs:    serverArgs,
		}),
		execution.WithTimeout(30*time.Second),
	)

	if _, err := execution.Run(ctx, s.execSvc, req); err != nil {
		if s.logger != nil {
			return execution.HandleError(s.logger, err, "vnc.control")
		}
		return err
	}
	return nil
}
