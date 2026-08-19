package connection

import (
	"cmp"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"design-cloud-engine/internal/common/host"
	"design-cloud-engine/internal/contract"
)

// CreateManualConnectionForm defines the writable fields accepted when users add a connection manually.
type CreateManualConnectionForm struct {
	Alias        string            `json:"alias"`
	Protocol     contract.Protocol `json:"protocol"`
	Host         string            `json:"host"`
	Port         uint              `json:"port"`
	TargetNodeID string            `json:"targetNodeId,omitempty"`
}

func (f CreateManualConnectionForm) normalize() (CreateManualConnectionForm, error) {
	// Protocol validated below — no case normalization, fail on mismatch.

	if f.Alias == "" {
		return CreateManualConnectionForm{}, fmt.Errorf("%w: alias is required", fs.ErrInvalid)
	}
	if len(f.Alias) > 100 {
		return CreateManualConnectionForm{}, fmt.Errorf("%w: alias is too long", fs.ErrInvalid)
	}
	if f.Protocol == "" {
		return CreateManualConnectionForm{}, fmt.Errorf("%w: protocol is required", fs.ErrInvalid)
	}
	if f.Protocol != contract.VNCProtocol && f.Protocol != contract.RDPProtocol && f.Protocol != contract.SSHProtocol {
		return CreateManualConnectionForm{}, fmt.Errorf("%w: unsupported protocol %q", fs.ErrInvalid, f.Protocol)
	}
	if f.Host == "" {
		return CreateManualConnectionForm{}, fmt.Errorf("%w: host is required", fs.ErrInvalid)
	}
	if len(f.Host) > 255 {
		return CreateManualConnectionForm{}, fmt.Errorf("%w: host is too long", fs.ErrInvalid)
	}
	if err := host.ValidateHost(f.Host); err != nil {
		return CreateManualConnectionForm{}, fmt.Errorf("%w: %w", fs.ErrInvalid, err)
	}
	if f.Port == 0 || f.Port > 65535 {
		return CreateManualConnectionForm{}, fmt.Errorf("%w: port must be between 1 and 65535", fs.ErrInvalid)
	}
	return f, nil
}

// CreateFixedConnectionForm defines the writable fields accepted when an admin
// creates a fixed connection record for a pre-assigned user→node mapping.
type CreateFixedConnectionForm struct {
	Name         string            `json:"name"`
	Alias        string            `json:"alias,omitempty"`
	Username     string            `json:"username"`
	Shared       bool              `json:"shared"` // true = 所有用户可见可连（如共享 Windows RDP），Username 留空
	TargetNodeID string            `json:"targetNodeId"`
	Host         string            `json:"host"`
	Protocol     contract.Protocol `json:"protocol"`
	Port         uint              `json:"port"`
	Display      string            `json:"display,omitempty"`
	StartOptions string            `json:"startOptions,omitempty"`
	OTPEnabled   *bool             `json:"otpEnabled,omitempty"`
}

// FormatDefaultConnectionName constructs a standardized display name for connections:
// <protocol>-<user/shared>-<node>[:display]
func FormatDefaultConnectionName(protocol contract.Protocol, username, nodeID, display string) string {
	protoStr := cmp.Or(strings.ToLower(string(protocol)), "vnc")
	userStr := cmp.Or(strings.TrimSpace(username), "shared")
	nodeStr := strings.TrimSpace(nodeID)
	displayPart := ""
	if d := strings.TrimSpace(display); d != "" {
		displayPart = ":" + strings.TrimPrefix(d, ":")
	}
	if nodeStr != "" {
		return fmt.Sprintf("%s-%s-%s%s", protoStr, userStr, nodeStr, displayPart)
	}
	return fmt.Sprintf("%s-%s%s", protoStr, userStr, displayPart)
}

func (f CreateFixedConnectionForm) normalize() (CreateFixedConnectionForm, error) {
	if !f.Shared && f.Username == "" {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: username is required for non-shared fixed connection", fs.ErrInvalid)
	}
	if f.Shared {
		f.Username = "" // 共享连接不归属特定用户
	}
	if f.TargetNodeID == "" {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: targetNodeId is required", fs.ErrInvalid)
	}
	f.Protocol = cmp.Or(f.Protocol, contract.VNCProtocol)
	if f.Protocol != contract.VNCProtocol && f.Protocol != contract.RDPProtocol && f.Protocol != contract.SSHProtocol {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: unsupported protocol %q", fs.ErrInvalid, f.Protocol)
	}
	if f.Shared && f.Protocol == contract.VNCProtocol {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: VNC connections cannot be shared (VNC is single-session)", fs.ErrInvalid)
	}
	if f.Host == "" {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: host is required", fs.ErrInvalid)
	}
	if len(f.Host) > 255 {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: host is too long", fs.ErrInvalid)
	}
	if err := host.ValidateHost(f.Host); err != nil {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: %w", fs.ErrInvalid, err)
	}
	if f.Display != "" {
		f.Display = strings.TrimSpace(f.Display)
		if len(f.Display) > 32 {
			return CreateFixedConnectionForm{}, fmt.Errorf("%w: display is too long", fs.ErrInvalid)
		}
		if !strings.HasPrefix(f.Display, ":") {
			f.Display = ":" + f.Display
		}
	}
	if f.Protocol == contract.VNCProtocol {
		if f.Port == 0 && f.Display != "" {
			displayNumStr := strings.TrimPrefix(f.Display, ":")
			if num, err := strconv.ParseUint(displayNumStr, 10, 32); err == nil && num > 0 && num < 59635 {
				f.Port = 5900 + uint(num)
			}
		} else if f.Port >= 5901 && f.Port <= 65535 && f.Display == "" {
			f.Display = fmt.Sprintf(":%d", f.Port-5900)
		}
	}
	if f.Port == 0 || f.Port > 65535 {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: port must be between 1 and 65535", fs.ErrInvalid)
	}
	if f.Name == "" {
		f.Name = FormatDefaultConnectionName(f.Protocol, f.Username, f.TargetNodeID, f.Display)
	} else if len(f.Name) > 128 {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: name is too long", fs.ErrInvalid)
	}
	if f.Alias != "" && len(f.Alias) > 100 {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: alias is too long", fs.ErrInvalid)
	}
	if len(f.StartOptions) > 255 {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: startOptions is too long", fs.ErrInvalid)
	}
	if strings.ContainsAny(f.StartOptions, "\r\n\x00") {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: startOptions contains invalid control characters", fs.ErrInvalid)
	}
	return f, nil
}

// normalizeForUpsert validates fields for the batch-upsert path.
// Compared to normalize(), it relaxes port (0 is allowed) and host (empty is allowed for
// node-level fixed connections where the agent fills the host later).
// Port and display together determine the lookup key: port takes priority over display.
func (f CreateFixedConnectionForm) normalizeForUpsert() (CreateFixedConnectionForm, error) {
	if !f.Shared && f.Username == "" {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: username is required for non-shared fixed connection", fs.ErrInvalid)
	}
	if f.Shared {
		f.Username = ""
	}
	if f.TargetNodeID == "" {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: targetNodeId is required", fs.ErrInvalid)
	}
	f.Protocol = cmp.Or(f.Protocol, contract.VNCProtocol)
	if f.Protocol != contract.VNCProtocol && f.Protocol != contract.RDPProtocol && f.Protocol != contract.SSHProtocol {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: unsupported protocol %q", fs.ErrInvalid, f.Protocol)
	}
	if f.Shared && f.Protocol == contract.VNCProtocol {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: VNC connections cannot be shared (VNC is single-session)", fs.ErrInvalid)
	}
	if f.Display != "" {
		f.Display = strings.TrimSpace(f.Display)
		if len(f.Display) > 32 {
			return CreateFixedConnectionForm{}, fmt.Errorf("%w: display is too long", fs.ErrInvalid)
		}
		if !strings.HasPrefix(f.Display, ":") {
			f.Display = ":" + f.Display
		}
	}
	if f.Protocol == contract.VNCProtocol {
		if f.Port == 0 && f.Display != "" {
			displayNumStr := strings.TrimPrefix(f.Display, ":")
			if num, err := strconv.ParseUint(displayNumStr, 10, 32); err == nil && num > 0 && num < 59635 {
				f.Port = 5900 + uint(num)
			}
		} else if f.Port >= 5901 && f.Port <= 65535 && f.Display == "" {
			f.Display = fmt.Sprintf(":%d", f.Port-5900)
		}
	}
	// port == 0 is allowed (node-level slot without port/display); port in (1,65535] is valid
	if f.Port > 65535 {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: port must be between 0 and 65535", fs.ErrInvalid)
	}
	// host is optional in upsert (node-level connections may not have host yet)
	if f.Host != "" {
		if len(f.Host) > 255 {
			return CreateFixedConnectionForm{}, fmt.Errorf("%w: host is too long", fs.ErrInvalid)
		}
		if err := host.ValidateHost(f.Host); err != nil {
			return CreateFixedConnectionForm{}, fmt.Errorf("%w: %w", fs.ErrInvalid, err)
		}
	}
	if f.Name == "" {
		f.Name = FormatDefaultConnectionName(f.Protocol, f.Username, f.TargetNodeID, f.Display)
	} else if len(f.Name) > 128 {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: name is too long", fs.ErrInvalid)
	}
	if f.Alias != "" && len(f.Alias) > 100 {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: alias is too long", fs.ErrInvalid)
	}
	if len(f.StartOptions) > 255 {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: startOptions is too long", fs.ErrInvalid)
	}
	if strings.ContainsAny(f.StartOptions, "\r\n\x00") {
		return CreateFixedConnectionForm{}, fmt.Errorf("%w: startOptions contains invalid control characters", fs.ErrInvalid)
	}
	return f, nil
}
