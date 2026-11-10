package schema

import (
	"bytes"
	"design-cloud-engine/internal/contract"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"path/filepath"
	"strings"
)

// NodeSettings defines the typed settings contract stored in model.Node.Settings.
// Top-level keys strictly correspond to supported override domains: "agent", "alert", "license", "scheduler".
type NodeSettings struct {
	Agent     *NodeAgentOverride     `json:"agent,omitempty"`
	Alert     *NodeAlertOverride     `json:"alert,omitempty"`
	License   *NodeLicenseOverride   `json:"license,omitempty"`
	Scheduler *NodeSchedulerOverride `json:"scheduler,omitempty"`
}

// NodeAgentOverride defines sparse overrides over the global AgentSettings.
// Nil pointers indicate that the global default applies.
type NodeAgentOverride struct {
	ExecBinPath string               `json:"execBinPath,omitempty"`
	VNC         *NodeVNCOverride     `json:"vnc,omitempty"`
	Monitor     *NodeMonitorOverride `json:"monitor,omitempty"`
}

// NodeVNCOverride defines sparse overrides for VNC server and connection probe.
type NodeVNCOverride struct {
	BinDir       string   `json:"binDir,omitempty"`
	ScanInterval *int     `json:"scanInterval,omitempty" validate:"min=30,max=86400" unit:"s"` // 显式指针覆盖
	ScanEnabled  *bool    `json:"scanEnabled,omitempty"`                                       // pointer: allows explicit false to disable
	ServerArgs   []string `json:"serverArgs,omitempty" validate:"maxLen=128"`                  // slice: nil = inherit global, non-nil = override; per-element maxLen
	UseCgroup    *bool    `json:"useCgroup,omitempty"`                                         // pointer: Linux VNC 是否默认启用 systemd transient slice
}

// NodeMonitorOverride defines sparse overrides for node hardware monitoring.
// 仅硬件与路径相关项可覆盖：metricsEnabled / metricsInterval 属集群可观测性
// 策略（采样粒度需全局一致，否则概览聚合与历史曲线失真），固定为全局配置。
type NodeMonitorOverride struct {
	NvidiaSmiPath string            `json:"nvidiaSmiPath,omitempty"`
	DiskMounts    []DiskMountConfig `json:"diskMounts,omitempty"`
}

// NodeAlertOverride defines sparse overrides for node alert thresholds.
// 仅阈值相关项可覆盖（阈值取决于具体硬件规格）；cooldownInterval 是通知节奏策略，
// 与节点硬件无关，固定为全局配置。
// 容量/空间下限（MinFreeSpace）统一为 KiB（1024 Bytes），使用率统一为百分比，单核负载统一为百分比 (load1/cores * 100)，参见 schema.AlertSettings 规范。
type NodeAlertOverride struct {
	Enabled *bool                    `json:"enabled,omitempty"`
	CPU     *NodeCPUAlertOverride    `json:"cpu,omitempty"`
	Load    *NodeLoadAlertOverride   `json:"load,omitempty"`
	Memory  *NodeMemoryAlertOverride `json:"memory,omitempty"`
	Disk    *NodeDiskAlertOverride   `json:"disk,omitempty"`
}

// NodeCPUAlertOverride defines sparse overrides for CPU alerts.
type NodeCPUAlertOverride struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	MaxUsedPercent *float64 `json:"maxUsedPercent,omitempty" validate:"min=1,max=100"`
}

// NodeLoadAlertOverride defines sparse overrides for Load alerts.
type NodeLoadAlertOverride struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	MaxLoadPercent *float64 `json:"maxLoadPercent,omitempty" validate:"min=50,max=200"`
}

// NodeMemoryAlertOverride defines sparse overrides for Memory alerts.
type NodeMemoryAlertOverride struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	MaxUsedPercent *float64 `json:"maxUsedPercent,omitempty" validate:"min=0,max=100"`
	MinFreeSpace   *int64   `json:"minFreeSpace,omitempty" validate:"min=0" unit:"KiB"`
}

// NodeDiskAlertOverride defines sparse overrides for Disk alerts.
type NodeDiskAlertOverride struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	MaxUsedPercent *float64 `json:"maxUsedPercent,omitempty" validate:"min=0,max=100"`
	MinFreeSpace   *int64   `json:"minFreeSpace,omitempty" validate:"min=0" unit:"KiB"`
}

// NodeLicenseOverride defines sparse overrides for license settings.
type NodeLicenseOverride struct {
	LmutilPath string `json:"lmutilPath,omitempty"`
}

// NodeSchedulerOverride defines sparse overrides for scheduler settings.
// Only EnvScript is node-local; Type and JobCacheTTL are cluster-wide.
type NodeSchedulerOverride struct {
	EnvScript string `json:"envScript,omitempty"`
}

// NodeEffectiveSettings contains the complete effective configuration for all node-overridable domains.
type NodeEffectiveSettings struct {
	Agent     AgentSettings     `json:"agent"`
	Alert     AlertSettings     `json:"alert"`
	License   LicenseSettings   `json:"license"`
	Scheduler SchedulerSettings `json:"scheduler"`
}

// NodeSettingsView is the response payload returned to management clients,
// providing both raw overrides and the resulting effective configuration.
type NodeSettingsView struct {
	Override  *NodeSettings          `json:"override"`
	Effective *NodeEffectiveSettings `json:"effective"`
}

// DecodeNodeSettingsPayload strictly unmarshals the JSON request body into NodeSettings,
// rejecting syntax errors (ErrMalformed) and any unknown fields across all nesting levels
// (contract.NewValidationError).
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

	var s NodeSettings
	if err := json.UnmarshalStrict(trimmed, &s); err != nil {
		return nil, contract.NewValidationError(err.Error(), err)
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
		if ag.ExecBinPath != "" && !isAcceptablePath(ag.ExecBinPath) {
			return contract.NewValidationError("execBinPath must be an absolute path")
		}
		if ag.VNC != nil {
			vnc := ag.VNC
			if vnc.BinDir != "" && !isAcceptablePath(vnc.BinDir) {
				return contract.NewValidationError("vnc.binDir must be an absolute path")
			}
			if err := validateVNCServerArgsContent(vnc.ServerArgs); err != nil {
				return err
			}
		}
		if ag.Monitor != nil {
			m := ag.Monitor
			if m.NvidiaSmiPath != "" && !isAcceptablePath(m.NvidiaSmiPath) {
				return contract.NewValidationError("monitor.nvidiaSmiPath must be an absolute path")
			}
			for _, dm := range m.DiskMounts {
				if dm.Name == "" {
					return contract.NewValidationError("monitor.diskMounts: name cannot be empty")
				}
				if dm.Path == "" {
					return contract.NewValidationError("monitor.diskMounts: path cannot be empty")
				}
				if !isAcceptablePath(dm.Path) {
					return contract.NewValidationError("monitor.diskMounts: path must be an absolute path")
				}
				if dm.OS != "" && dm.OS != "linux" && dm.OS != "windows" {
					return contract.NewValidationError("monitor.diskMounts: os must be linux or windows")
				}
			}
		}
	}
	if s.License != nil {
		lic := s.License
		if lic.LmutilPath != "" {
			if !isAcceptablePath(lic.LmutilPath) {
				return contract.NewValidationError("license.lmutilPath must be an absolute path")
			}
			if strings.ContainsAny(lic.LmutilPath, "\x00\r\n\t") {
				return contract.NewValidationError("license.lmutilPath contains invalid characters")
			}
		}
	}
	if s.Scheduler != nil {
		if err := validateEnvScript("scheduler.envScript", s.Scheduler.EnvScript); err != nil {
			return err
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
