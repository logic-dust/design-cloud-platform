package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// NodeSettings defines the typed settings contract stored in model.Node.Settings.
// Top-level keys strictly correspond to supported override domains: "agent", "alert", "license".
type NodeSettings struct {
	Agent   *NodeAgentOverride   `json:"agent,omitempty"`
	Alert   *NodeAlertOverride   `json:"alert,omitempty"`
	License *NodeLicenseOverride `json:"license,omitempty"`
}

// NodeAgentOverride defines sparse overrides over the global AgentSettings.
// Nil pointers indicate that the global default applies.
type NodeAgentOverride struct {
	LauncherPath string               `json:"launcherPath,omitempty"`
	VNC          *NodeVNCOverride     `json:"vnc,omitempty"`
	Monitor      *NodeMonitorOverride `json:"monitor,omitempty"`
}

// NodeVNCOverride defines sparse overrides for VNC server and connection probe.
type NodeVNCOverride struct {
	BinDir       string   `json:"binDir,omitempty"`
	ScanInterval *int     `json:"scanInterval,omitempty" validate:"omitempty,min=60,max=86400,allowzero"` // pointer: allows explicit 0 to disable probe
	ScanEnabled  *bool    `json:"scanEnabled,omitempty"`                                                  // pointer: allows explicit false to disable
	ServerArgs   []string `json:"serverArgs,omitempty"`                                                   // slice: nil = inherit global, non-nil = override
}

// NodeMonitorOverride defines sparse overrides for node hardware monitoring.
type NodeMonitorOverride struct {
	NvidiaSmiPath          string            `json:"nvidiaSmiPath,omitempty"`
	DiskMounts             []DiskMountConfig `json:"diskMounts,omitempty"`
	ResourceReportEnabled  *bool             `json:"resourceReportEnabled,omitempty"`
	ResourceReportInterval *int              `json:"resourceReportInterval,omitempty" validate:"omitempty,min=30,max=3600,allowzero"`
}

// NodeAlertOverride defines sparse overrides for node alert thresholds.
// 容量/空间下限（MinFreeSpace）统一为 KB，时间间隔统一为秒，使用率统一为百分比，单核负载统一为百分比 (load1/cores * 100)，参见 schema.AlertSettings 规范。
type NodeAlertOverride struct {
	Enabled *bool                    `json:"enabled,omitempty"`
	CPU     *NodeCPUAlertOverride    `json:"cpu,omitempty"`
	Load    *NodeLoadAlertOverride   `json:"load,omitempty"`
	Memory  *NodeMemoryAlertOverride `json:"memory,omitempty"`
	Disk    *NodeDiskAlertOverride   `json:"disk,omitempty"`
}

// NodeCPUAlertOverride defines sparse overrides for CPU alerts.
type NodeCPUAlertOverride struct {
	Enabled          *bool    `json:"enabled,omitempty"`
	MaxUsedPercent   *float64 `json:"maxUsedPercent,omitempty" validate:"omitempty,min=0,max=100,allowzero"`
	CooldownInterval *int     `json:"cooldownInterval,omitempty" validate:"omitempty,min=60,max=86400"`
}

// NodeLoadAlertOverride defines sparse overrides for Load alerts.
type NodeLoadAlertOverride struct {
	Enabled          *bool    `json:"enabled,omitempty"`
	MaxLoadPercent   *float64 `json:"maxLoadPercent,omitempty" validate:"omitempty,min=0,max=9999,allowzero"`
	CooldownInterval *int     `json:"cooldownInterval,omitempty" validate:"omitempty,min=60,max=86400"`
}

// NodeMemoryAlertOverride defines sparse overrides for Memory alerts.
type NodeMemoryAlertOverride struct {
	Enabled          *bool    `json:"enabled,omitempty"`
	MaxUsedPercent   *float64 `json:"maxUsedPercent,omitempty" validate:"omitempty,min=0,max=100,allowzero"`
	MinFreeSpace     *int64   `json:"minFreeSpace,omitempty" validate:"omitempty,min=0,allowzero"`
	CooldownInterval *int     `json:"cooldownInterval,omitempty" validate:"omitempty,min=60,max=86400"`
}

// NodeDiskAlertOverride defines sparse overrides for Disk alerts.
type NodeDiskAlertOverride struct {
	Enabled          *bool    `json:"enabled,omitempty"`
	MaxUsedPercent   *float64 `json:"maxUsedPercent,omitempty" validate:"omitempty,min=0,max=100,allowzero"`
	MinFreeSpace     *int64   `json:"minFreeSpace,omitempty" validate:"omitempty,min=0,allowzero"`
	CooldownInterval *int     `json:"cooldownInterval,omitempty" validate:"omitempty,min=60,max=86400"`
}

// NodeLicenseOverride defines sparse overrides for license settings.
type NodeLicenseOverride struct {
	LmutilPath string `json:"lmutilPath,omitempty"`
}

// NodeEffectiveSettings contains the complete effective configuration for all node-overridable domains.
type NodeEffectiveSettings struct {
	Agent   AgentSettings   `json:"agent"`
	Alert   AlertSettings   `json:"alert"`
	License LicenseSettings `json:"license"`
}

// NodeSettingsView is the response payload returned to management clients,
// providing both raw overrides and the resulting effective configuration.
type NodeSettingsView struct {
	Override  *NodeSettings          `json:"override"`
	Effective *NodeEffectiveSettings `json:"effective"`
}

// DecodeNodeSettingsPayload strictly unmarshals the JSON request body into NodeSettings,
// rejecting syntax errors (ErrMalformed) and any unknown fields across all nesting levels (fs.ErrInvalid).
func DecodeNodeSettingsPayload(raw []byte) (*NodeSettings, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return &NodeSettings{}, nil
	}

	// 1. Check syntax first to distinguish malformed JSON (400) from validation errors (422)
	var probe any
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrMalformed, err)
	}
	if _, ok := probe.(map[string]any); !ok {
		return nil, fmt.Errorf("%w: payload must be a JSON object", ErrMalformed)
	}

	// 2. Strict decoding rejecting unknown fields recursively
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var s NodeSettings
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%w: %s", fs.ErrInvalid, err)
	}
	if err := rejectTrailingJSON(dec); err != nil {
		return nil, fmt.Errorf("%w: request body must contain a single JSON object", ErrMalformed)
	}
	return &s, nil
}

// DecodeNodeSettingsFromMap deserializes raw map[string]any stored in model.Node into typed NodeSettings.
func DecodeNodeSettingsFromMap(m map[string]any) (*NodeSettings, error) {
	if len(m) == 0 {
		return &NodeSettings{}, nil
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal raw node settings: %w", err)
	}
	s, err := DecodeNodeSettingsPayload(data)
	if err != nil {
		return nil, fmt.Errorf("decode node settings: %w", err)
	}
	if err := ValidateNodeSettings(s); err != nil {
		return nil, fmt.Errorf("validate node settings: %w", err)
	}
	return s, nil
}

// ValidateNodeSettings validates tag bounds and path constraints on the sparse override.
func ValidateNodeSettings(s *NodeSettings) error {
	if s == nil {
		return nil
	}
	if err := ValidateStructByTags(s); err != nil {
		return err
	}
	if s.Agent != nil {
		ag := s.Agent
		if ag.LauncherPath != "" && !isAcceptablePath(ag.LauncherPath) {
			return fmt.Errorf("%w: launcherPath must be an absolute path", fs.ErrInvalid)
		}
		if ag.VNC != nil {
			vnc := ag.VNC
			if vnc.BinDir != "" && !isAcceptablePath(vnc.BinDir) {
				return fmt.Errorf("%w: vnc.binDir must be an absolute path", fs.ErrInvalid)
			}
			for _, arg := range vnc.ServerArgs {
				if len(arg) > 255 {
					return fmt.Errorf("%w: vnc.serverArgs element is too long", fs.ErrInvalid)
				}
				if strings.ContainsAny(arg, "\r\n\x00") {
					return fmt.Errorf("%w: vnc.serverArgs element contains invalid control characters", fs.ErrInvalid)
				}
			}
		}
		if ag.Monitor != nil {
			m := ag.Monitor
			if m.NvidiaSmiPath != "" && !isAcceptablePath(m.NvidiaSmiPath) {
				return fmt.Errorf("%w: monitor.nvidiaSmiPath must be an absolute path", fs.ErrInvalid)
			}
			for _, dm := range m.DiskMounts {
				if dm.Name == "" {
					return fmt.Errorf("%w: monitor.diskMounts: name cannot be empty", fs.ErrInvalid)
				}
				if dm.Path == "" {
					return fmt.Errorf("%w: monitor.diskMounts: path cannot be empty", fs.ErrInvalid)
				}
				if !isAcceptablePath(dm.Path) {
					return fmt.Errorf("%w: monitor.diskMounts: path must be an absolute path", fs.ErrInvalid)
				}
				if dm.OS != "" && dm.OS != "linux" && dm.OS != "windows" {
					return fmt.Errorf("%w: monitor.diskMounts: os must be linux or windows", fs.ErrInvalid)
				}
			}
		}
	}
	if s.License != nil {
		lic := s.License
		if lic.LmutilPath != "" {
			if !isAcceptablePath(lic.LmutilPath) {
				return fmt.Errorf("%w: license.lmutilPath must be an absolute path", fs.ErrInvalid)
			}
			if strings.ContainsAny(lic.LmutilPath, "\x00\r\n\t") {
				return fmt.Errorf("%w: license.lmutilPath contains invalid characters", fs.ErrInvalid)
			}
		}
	}
	return nil
}

func isAcceptablePath(p string) bool {
	if filepath.IsAbs(p) {
		return true
	}
	// Windows drive absolute path support: e.g. C:\path or C:/path
	if len(p) >= 3 && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) && p[1] == ':' && (p[2] == '/' || p[2] == '\\') {
		return true
	}
	return false
}
