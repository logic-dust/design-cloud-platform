package connection

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
)

// GenerateVNCOTP creates a TurboVNC protocol-level one-time password for a
// running VNC connection by dispatching to the target node's launcher.
// Returns the OTP string.
func (s *Service) GenerateVNCOTP(ctx context.Context, id uint, requestUser string) (string, error) {
	conn, err := s.GetByIDForUser(ctx, id, requestUser)
	if err != nil {
		return "", err
	}
	if err := validateVNCOTPEligibility(conn); err != nil {
		return "", err
	}
	targetHost := vncAccessHost(conn)
	if targetHost == "" {
		return "", fmt.Errorf("%w: connection target host is required", fs.ErrInvalid)
	}
	if s.settingsSvc == nil {
		return "", fmt.Errorf("settings service is uninitialized in connection service")
	}
	nodeSettings, err := s.settingsSvc.GetEffectiveAgentSettingsForNode(ctx, conn.TargetNodeID)
	if err != nil {
		return "", fmt.Errorf("resolve effective agent settings for node %q: %w", conn.TargetNodeID, err)
	}
	passwdPath := nodeSettings.VNC.ResolvedPasswdPath()
	if passwdPath == "" {
		return "", fmt.Errorf("%w: vnc passwd binary path is empty/unresolved for node %q", fs.ErrInvalid, conn.TargetNodeID)
	}
	if s.execSvc == nil {
		return "", fmt.Errorf("connection execution service is not configured")
	}

	executionID := fmt.Sprintf("vnc-otp-%d-%d", conn.ID, time.Now().UnixNano())
	req := execution.NewExecuteRequest(execution.ActionVNCGenerateOTP, executionID,
		execution.WithTargetUser(conn.Username),
		execution.WithTargetNode(conn.TargetNodeID),
		execution.WithPayload(payload.VNCGenerateOTP{
			ConnectionID:  conn.ID,
			Display:       conn.Display,
			VNCPasswdPath: passwdPath,
		}),
		execution.WithTimeout(10*time.Second),
	)

	stdout, err := execution.Run(ctx, s.execSvc, req)
	if err != nil {
		if s.logger != nil {
			return "", execution.HandleError(s.logger, err, "vnc.generate_otp")
		}
		return "", err
	}

	var result payload.VNCGenerateOTPResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		return "", fmt.Errorf("parse vnc otp result: %w", err)
	}
	if result.OTP == "" {
		return "", fmt.Errorf("parse vnc otp result: otp is empty")
	}

	now := time.Now()
	if err := s.connectionRepo.UpdateFields(ctx, conn.ID, map[string]any{"last_accessed_at": now}); err != nil {
		return "", fmt.Errorf("touch connection access time: %w", err)
	}

	return result.OTP, nil
}

func validateVNCOTPEligibility(conn model.Connection) error {
	if conn.Protocol != contract.VNCProtocol {
		return fmt.Errorf("%w: only VNC connections support TurboVNC OTP", fs.ErrInvalid)
	}
	if !conn.IsOTPEnabled() {
		return fmt.Errorf("%w: OTP authentication is disabled or not supported for this connection", fs.ErrInvalid)
	}
	return nil
}

func vncAccessHost(conn model.Connection) string {
	switch {
	case conn.NodeClientHost != "":
		return conn.NodeClientHost
	case conn.Host != "":
		return conn.Host
	default:
		return conn.TargetNodeID
	}
}
