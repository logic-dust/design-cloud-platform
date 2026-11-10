package connection

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
	"design-cloud-engine/internal/model"
)

func classifyVNCExecuteError(err error) error {
	if err == nil {
		return nil
	}
	var msg string
	if ae, ok := errors.AsType[*execution.ActionError](err); ok {
		msg = ae.Message
	} else {
		msg = err.Error()
	}
	if strings.Contains(msg, "display_exhausted") {
		return ErrDisplayExhausted
	}
	if strings.Contains(msg, "display_conflict") {
		return ErrDisplayConflict
	}
	return err
}

func (s *Service) launchVNCOnNode(
	ctx context.Context,
	node model.Node,
	username string,
	connectionID uint,
	fixedDisplay *int,
	avoidDisplays []int,
	vncConfig json.RawMessage,
) (chosenDisplay int, port uint, pid uint, err error) {
	if s.execSvc == nil {
		return 0, 0, 0, fmt.Errorf("executor service not configured")
	}

	nodeSettings, err := s.settingsSvc.GetEffectiveAgentSettingsForNode(ctx, node.NodeID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("resolve agent settings for node %s: %w", node.NodeID, err)
	}

	serverPath := nodeSettings.VNC.ResolvedServerPath()
	if serverPath == "" {
		return 0, 0, 0, fmt.Errorf("vnc server binary path unresolved for node %s", node.NodeID)
	}

	serverArgs := slices.Clone(nodeSettings.VNC.ServerArgs)
	startOpts := ""
	useCgroup := nodeSettings.VNC.UseCgroup
	if len(vncConfig) > 0 {
		var cfg struct {
			StartOptions string `json:"startOptions"`
			Resolution   string `json:"resolution"`
			UseCgroup    *bool  `json:"useCgroup"`
		}
		if err := json.Unmarshal(vncConfig, &cfg); err != nil {
			return 0, 0, 0, fmt.Errorf("parse resource vnc config: %w", err)
		}
		if cfg.UseCgroup != nil {
			useCgroup = *cfg.UseCgroup
		}
		extraArgs := make([]string, 0, 4)
		if cfg.Resolution != "" {
			extraArgs = append(extraArgs, "-geometry", cfg.Resolution)
		}
		if cfg.StartOptions != "" {
			extraArgs = append(extraArgs, strings.Fields(cfg.StartOptions)...)
		}
		if len(extraArgs) > 0 {
			serverArgs = append(serverArgs, extraArgs...)
			startOpts = strings.Join(extraArgs, " ")
		}
	}

	actionName := execution.ActionVNCControl
	if useCgroup {
		actionName = execution.ActionVNCCgroupControl
	}

	payloadVNC := payload.VNCControl{
		ConnectionID:  connectionID,
		Display:       fixedDisplay,
		Operation:     "start",
		VNCServerPath: serverPath,
		VNCPasswdPath: nodeSettings.VNC.ResolvedPasswdPath(),
		PasswdMode:    nodeSettings.VNC.PasswdMode,
		StartOptions:  startOpts,
		ServerArgs:    serverArgs,
		AvoidDisplays: avoidDisplays,
		DisplayMin:    dynamicDisplayMin,
		DisplayMax:    dynamicDisplayMax,
	}

	execLabel := "dynamic"
	if fixedDisplay != nil {
		execLabel = fmt.Sprintf("fixed-%d", *fixedDisplay)
	}
	executionID := fmt.Sprintf("vnc-launch-%s-%s-%d", username, execLabel, time.Now().UnixNano())
	req := execution.NewExecuteRequest(actionName, executionID,
		execution.WithTargetUser(username),
		execution.WithTargetNode(node.NodeID),
		execution.WithPayload(payloadVNC),
		execution.WithTimeout(30*time.Second),
	)

	res, err := execution.Run(ctx, s.execSvc, req)
	if err != nil {
		if s.logger != nil {
			err = execution.HandleError(s.logger, err, "vnc.launch")
		}
		return 0, 0, 0, classifyVNCExecuteError(err)
	}

	var out struct {
		Display int `json:"display"`
		Port    int `json:"port"`
		PID     int `json:"pid"`
	}
	if err := json.Unmarshal([]byte(res), &out); err != nil {
		return 0, 0, 0, fmt.Errorf("parse vnc launch result: %w", err)
	}
	if out.Display < 0 {
		return 0, 0, 0, fmt.Errorf("PrivilegedExec did not report a display")
	}
	if out.Port <= 0 {
		return 0, 0, 0, fmt.Errorf("PrivilegedExec did not report a listen port")
	}
	chosenDisplay = out.Display
	port = uint(out.Port)
	if out.PID > 0 {
		pid = uint(out.PID)
	}
	return chosenDisplay, port, pid, nil
}

func (s *Service) rollbackLaunchedVNC(ctx context.Context, nodeID, username string, connectionID uint, display int) {
	if display < 0 {
		return
	}
	if err := s.dispatchKillVNC(ctx, nodeID, username, connectionID, display); err != nil && s.logger != nil {
		s.logger.Warn("rollback kill vnc after finalize conflict", "connectionID", connectionID, "display", display, "error", err)
	}
}
