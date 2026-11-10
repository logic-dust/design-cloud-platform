package connection

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
)

// GenerateVNCOTP creates a TurboVNC protocol-level one-time password for a
// running VNC connection by dispatching to the target node's PrivilegedExec.
// Returns the OTP string.
func (s *Service) GenerateVNCOTP(ctx context.Context, id uint, requestUser string) (string, error) {
	conn, err := s.GetByIDForUser(ctx, id, requestUser)
	if err != nil {
		return "", err
	}
	if err := validateVNCOTPEligibility(conn); err != nil {
		return "", err
	}
	targetHost := s.dialHostForNode(ctx, conn.NodeID)
	if targetHost == "" {
		return "", contract.NewInvalidParamError("connection target host is required")
	}
	if s.settingsSvc == nil {
		return "", fmt.Errorf("settings service is uninitialized in connection service")
	}
	nodeSettings, err := s.settingsSvc.GetEffectiveAgentSettingsForNode(ctx, conn.NodeID)
	if err != nil {
		return "", fmt.Errorf("resolve effective agent settings for node %q: %w", conn.NodeID, err)
	}
	passwdPath := nodeSettings.VNC.ResolvedPasswdPath()
	if passwdPath == "" {
		return "", contract.NewValidationError(fmt.Sprintf("vnc passwd binary path is empty/unresolved for node %q", conn.NodeID))
	}
	if s.execSvc == nil {
		return "", fmt.Errorf("connection execution service is not configured")
	}

	executionID := fmt.Sprintf("vnc-otp-%d-%d", conn.ID, time.Now().UnixNano())
	req := execution.NewExecuteRequest(execution.ActionVNCGenerateOTP, executionID,
		execution.WithTargetUser(conn.Username),
		execution.WithTargetNode(conn.NodeID),
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

	_ = s.Touch(ctx, conn.ID, conn.Username)

	return result.OTP, nil
}

func validateVNCOTPEligibility(conn model.Connection) error {
	if conn.Protocol != contract.VNCProtocol {
		return contract.NewValidationError("only VNC connections support TurboVNC OTP")
	}
	return nil
}
