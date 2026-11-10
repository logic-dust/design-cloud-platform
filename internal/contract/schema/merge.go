package schema

import (
	"bytes"
	"design-cloud-engine/internal/contract"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
)

// MergeDomainPayload 把 patch 递归叠加到 base 之上，使设置更新具备"局部更新"语义：
// **未携带的字段保持 base 原值**（而不是被解码成零值覆盖），携带的字段整体替换。
//
// 递归到嵌套对象（website / vnc / monitor / alert 阈值 / desktop 等），
// 因此前端只需提交它真正修改的字段。
func MergeDomainPayload(base, patch []byte) ([]byte, error) {
	baseObj := map[string]json.RawMessage{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &baseObj); err != nil {
			return nil, contract.NewValidationError(fmt.Sprintf("base settings payload: %s", err), err)
		}
	}

	patchObj := map[string]json.RawMessage{}
	if err := json.Unmarshal(patch, &patchObj); err != nil {
		return nil, contract.NewValidationError(err.Error(), err)
	}

	for key, patchVal := range patchObj {
		baseVal, ok := baseObj[key]
		if !ok {
			baseObj[key] = patchVal
			continue
		}
		merged, err := mergeJSONValue(baseVal, patchVal)
		if err != nil {
			return nil, err
		}
		baseObj[key] = merged
	}

	out, err := json.Marshal(baseObj)
	if err != nil {
		return nil, fmt.Errorf("marshal merged settings: %w", err)
	}
	return out, nil
}

// mergeJSONValue 两个值都是 JSON 对象时递归合并，否则以 patch 整体替换。
func mergeJSONValue(base, patch json.RawMessage) (json.RawMessage, error) {
	if !isJSONObject(base) || !isJSONObject(patch) {
		return patch, nil
	}
	return MergeDomainPayload(base, patch)
}

func isJSONObject(raw json.RawMessage) bool {
	var obj map[string]json.RawMessage
	return json.Unmarshal(raw, &obj) == nil && obj != nil
}

// MergeAgentSettings merges node-level overrides into base AgentSettings with zero-value safety.
// Nil fields retain their base values, while non-nil fields override base values.
func MergeAgentSettings(base AgentSettings, o *NodeAgentOverride) AgentSettings {
	if o == nil {
		return base
	}
	if o.ExecBinPath != "" {
		base.ExecBinPath = o.ExecBinPath
	}
	if o.VNC != nil {
		v := o.VNC
		if v.BinDir != "" {
			base.VNC.BinDir = v.BinDir
		}
		if v.ScanInterval != nil {
			base.VNC.ScanInterval = *v.ScanInterval
		}
		if v.ScanEnabled != nil {
			base.VNC.ScanEnabled = *v.ScanEnabled
		}
		if v.ServerArgs != nil {
			base.VNC.ServerArgs = v.ServerArgs
		}
		if v.UseCgroup != nil {
			base.VNC.UseCgroup = *v.UseCgroup
		}
	}
	if o.Monitor != nil {
		m := o.Monitor
		if m.NvidiaSmiPath != "" {
			base.Monitor.NvidiaSmiPath = m.NvidiaSmiPath
		}
		if m.DiskMounts != nil {
			base.Monitor.DiskMounts = m.DiskMounts
		}
	}
	return base
}

// MergeAlertSettings merges node-level overrides into base AlertSettings with zero-value safety.
func MergeAlertSettings(base AlertSettings, o *NodeAlertOverride) AlertSettings {
	if o == nil {
		return base
	}
	if o.Enabled != nil {
		base.Enabled = *o.Enabled
	}
	if o.CPU != nil {
		if o.CPU.Enabled != nil {
			base.CPU.Enabled = *o.CPU.Enabled
		}
		if o.CPU.MaxUsedPercent != nil {
			base.CPU.MaxUsedPercent = *o.CPU.MaxUsedPercent
		}
	}
	if o.Load != nil {
		if o.Load.Enabled != nil {
			base.Load.Enabled = *o.Load.Enabled
		}
		if o.Load.MaxLoadPercent != nil {
			base.Load.MaxLoadPercent = *o.Load.MaxLoadPercent
		}
	}
	if o.Memory != nil {
		if o.Memory.Enabled != nil {
			base.Memory.Enabled = *o.Memory.Enabled
		}
		if o.Memory.MaxUsedPercent != nil {
			base.Memory.MaxUsedPercent = *o.Memory.MaxUsedPercent
		}
		if o.Memory.MinFreeSpace != nil {
			base.Memory.MinFreeSpace = *o.Memory.MinFreeSpace
		}
	}
	if o.Disk != nil {
		if o.Disk.Enabled != nil {
			base.Disk.Enabled = *o.Disk.Enabled
		}
		if o.Disk.MaxUsedPercent != nil {
			base.Disk.MaxUsedPercent = *o.Disk.MaxUsedPercent
		}
		if o.Disk.MinFreeSpace != nil {
			base.Disk.MinFreeSpace = *o.Disk.MinFreeSpace
		}
	}
	return base
}

// MergeLicenseSettings merges node-level overrides into base LicenseSettings.
func MergeLicenseSettings(base LicenseSettings, o *NodeLicenseOverride) LicenseSettings {
	if o == nil {
		return base
	}
	if o.LmutilPath != "" {
		base.LmutilPath = o.LmutilPath
	}
	return base
}

// MergeSchedulerSettings merges node-level overrides into base SchedulerSettings.
func MergeSchedulerSettings(base SchedulerSettings, o *NodeSchedulerOverride) SchedulerSettings {
	if o == nil {
		return base
	}
	if o.EnvScript != "" {
		base.EnvScript = o.EnvScript
	}
	return base
}

// MergeNodeSettingsPatch applies a partial patch to the existing stored node settings JSON.
// Supports null deletion semantics, validates against NodeSettings schema, and returns
// the merged JSON.
func MergeNodeSettingsPatch(base, patch []byte) ([]byte, error) {
	trimmedPatch := bytes.TrimSpace(patch)
	if len(trimmedPatch) == 0 {
		if len(bytes.TrimSpace(base)) == 0 {
			return []byte("{}"), nil
		}
		return base, nil
	}

	// 1. Must be a JSON object
	var probe any
	if err := json.Unmarshal(trimmedPatch, &probe); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrMalformed, err)
	}
	if _, ok := probe.(map[string]any); !ok {
		return nil, fmt.Errorf("%w: payload must be a JSON object", ErrMalformed)
	}

	// 2. Recursive merge with null deletion
	mergedRaw, err := mergeNodeJSON(base, trimmedPatch)
	if err != nil {
		return nil, err
	}

	// 3. Strict decode to reject unknown domains / fields
	nodeSettings, err := DecodeNodeSettingsPayload(mergedRaw)
	if err != nil {
		return nil, err
	}

	// 4. Validate constraints (paths, bounds, etc.)
	if err := ValidateNodeSettings(nodeSettings); err != nil {
		return nil, err
	}

	// 5. If all domains are empty, normalize to "{}"
	if nodeSettings.Agent == nil && nodeSettings.Alert == nil && nodeSettings.License == nil && nodeSettings.Scheduler == nil {
		return []byte("{}"), nil
	}

	return mergedRaw, nil
}

func mergeNodeJSON(base, patch []byte) ([]byte, error) {
	var patchObj map[string]json.RawMessage
	if err := json.Unmarshal(patch, &patchObj); err != nil {
		return nil, contract.NewValidationError(fmt.Sprintf("patch must be a JSON object: %s", err), err)
	}

	baseObj := make(map[string]json.RawMessage)
	if len(bytes.TrimSpace(base)) > 0 {
		if err := json.Unmarshal(base, &baseObj); err != nil {
			baseObj = make(map[string]json.RawMessage)
		}
	}

	for k, v := range patchObj {
		trimmed := bytes.TrimSpace(v)
		if bytes.Equal(trimmed, []byte("null")) {
			delete(baseObj, k)
			continue
		}
		if isJSONObject(v) {
			if existing, ok := baseObj[k]; ok && isJSONObject(existing) {
				subMerged, err := mergeNodeJSON(existing, v)
				if err != nil {
					return nil, err
				}
				if string(bytes.TrimSpace(subMerged)) == "{}" {
					delete(baseObj, k)
				} else {
					baseObj[k] = subMerged
				}
				continue
			}
		}
		baseObj[k] = v
	}

	out, err := json.Marshal(baseObj)
	if err != nil {
		return nil, fmt.Errorf("marshal merged node settings: %w", err)
	}
	return out, nil
}
