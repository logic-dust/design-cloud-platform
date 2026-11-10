package schema

import (
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/contract"
)

func TestMergeAgentSettings(t *testing.T) {
	base := AgentSettings{
		ExecBinPath: absTestPath("dc-exec"),
		VNC: VNCConfig{
			BinDir:       absTestPath("turbovnc", "bin"),
			ScanInterval: 1800,
			ScanEnabled:  true,
			ServerArgs:   []string{"-wm", "2d"},
		},
		Monitor: MonitorConfig{
			NvidiaSmiPath:   absTestPath("nvidia-smi"),
			MetricsEnabled:  true,
			MetricsInterval: 60,
		},
	}

	// Case 1: nil override returns unchanged base
	merged := MergeAgentSettings(base, nil)
	if merged.ExecBinPath != base.ExecBinPath {
		t.Errorf("expected ExecBinPath %s, got %s", base.ExecBinPath, merged.ExecBinPath)
	}

	// Case 2: Sparse override with zero values and cleared slice
	zeroInt := 0
	falseBool := false
	override := &NodeAgentOverride{
		ExecBinPath: absTestPath("custom", "bin", "dcexec"),
		VNC: &NodeVNCOverride{
			BinDir:       absTestPath("bin"),
			ScanInterval: &zeroInt,
			ScanEnabled:  &falseBool,
			ServerArgs:   []string{}, // explicitly clear
		},
		Monitor: &NodeMonitorOverride{
			NvidiaSmiPath: absTestPath("override", "nvidia-smi"),
		},
	}

	merged = MergeAgentSettings(base, override)
	if merged.ExecBinPath != absTestPath("custom", "bin", "dcexec") {
		t.Errorf("expected ExecBinPath /custom/bin/dcexec, got %s", merged.ExecBinPath)
	}
	if merged.VNC.BinDir != absTestPath("bin") {
		t.Errorf("expected VNC.BinDir /usr/bin, got %s", merged.VNC.BinDir)
	}
	if merged.VNC.ScanInterval != 0 {
		t.Errorf("expected ScanInterval 0, got %d", merged.VNC.ScanInterval)
	}
	if merged.VNC.ScanEnabled != false {
		t.Errorf("expected ScanEnabled false, got %v", merged.VNC.ScanEnabled)
	}
	if len(merged.VNC.ServerArgs) != 0 {
		t.Errorf("expected ServerArgs to be cleared, got %v", merged.VNC.ServerArgs)
	}
	if merged.Monitor.NvidiaSmiPath != absTestPath("override", "nvidia-smi") {
		t.Errorf("expected Monitor.NvidiaSmiPath overridden, got %s", merged.Monitor.NvidiaSmiPath)
	}
	// metricsInterval 属全局策略，不接受节点覆盖，必须保持 base 原值。
	if merged.Monitor.MetricsInterval != base.Monitor.MetricsInterval {
		t.Errorf("expected Monitor.MetricsInterval %d, got %d", base.Monitor.MetricsInterval, merged.Monitor.MetricsInterval)
	}
}

func TestDecodeNodeSettingsPayload_Strict(t *testing.T) {
	// 1. Valid sparse payload
	validJSON := []byte(`{
		"agent": {
			"vnc": {
				"binDir": "/usr/bin",
				"scanInterval": 120
			}
		}
	}`)
	s, err := DecodeNodeSettingsPayload(validJSON)
	if err != nil {
		t.Fatalf("unexpected error decoding valid payload: %v", err)
	}
	if s.Agent == nil || s.Agent.VNC == nil || s.Agent.VNC.BinDir != "/usr/bin" {
		t.Fatalf("unexpected decoded struct: %+v", s)
	}
	if s.Agent.VNC.ScanInterval == nil || *s.Agent.VNC.ScanInterval != 120 {
		t.Fatalf("unexpected scanInterval: %v", s.Agent.VNC.ScanInterval)
	}

	// 2. Syntax error -> ErrMalformed
	syntaxErrJSON := []byte(`{ "agent": { bad json `)
	_, err = DecodeNodeSettingsPayload(syntaxErrJSON)
	if !errors.Is(err, ErrMalformed) {
		t.Errorf("expected ErrMalformed for syntax error, got: %v", err)
	}

	// 3. Deep unknown field -> contract.NewValidationError (422)
	unknownKeyJSON := []byte(`{
		"agent": {
			"vnc": {
				"unknownField": "value"
			}
		}
	}`)
	_, err = DecodeNodeSettingsPayload(unknownKeyJSON)
	if err == nil {
		t.Errorf("expected error for unknown field, got nil")
	} else {
		if contractErr, ok := errors.AsType[contract.DomainError](err); !ok {
			t.Errorf("expected contract.DomainError, got %T: %v", err, err)
		} else if contractErr.HTTPStatus() != http.StatusUnprocessableEntity {
			t.Errorf("expected 422 for unknown field, got %d: %v", contractErr.HTTPStatus(), err)
		}
	}

	// 4. Multiple root objects -> ErrMalformed or contract.DomainError (422)
	extraJSON := []byte(`{"agent": {}} {"extra": 1}`)
	_, err = DecodeNodeSettingsPayload(extraJSON)
	if err == nil {
		t.Errorf("expected error for extra data, got nil")
	} else {
		isMalformed := errors.Is(err, ErrMalformed)
		var contractErr contract.DomainError
		isValidation := errors.As(err, &contractErr) && contractErr.HTTPStatus() == http.StatusUnprocessableEntity
		if !isMalformed && !isValidation {
			t.Errorf("expected ErrMalformed or 422 DomainError for extra data, got: %v", err)
		}
	}
}

func TestValidateNodeSettings(t *testing.T) {
	valid := &NodeSettings{
		Agent: &NodeAgentOverride{
			ExecBinPath: absTestPath("dc-exec"),
			VNC: &NodeVNCOverride{
				BinDir: "C:\\opt\\turbovnc", // Windows absolute path
			},
		},
	}
	if err := ValidateNodeSettings(valid); err != nil {
		t.Errorf("expected valid NodeSettings, got: %v", err)
	}

	// Out of bounds scanInterval via pointer
	badInterval := 10 // min is 30
	badSettings := &NodeSettings{
		Agent: &NodeAgentOverride{
			VNC: &NodeVNCOverride{
				ScanInterval: &badInterval,
			},
		},
	}
	if err := ValidateNodeSettings(badSettings); err == nil {
		t.Error("expected error for scanInterval below min 30")
	}

	// Relative PrivilegedExec path
	badPath := &NodeSettings{
		Agent: &NodeAgentOverride{
			ExecBinPath: "relative/path",
		},
	}
	if err := ValidateNodeSettings(badPath); err == nil {
		t.Error("expected error for relative PrivilegedExec path")
	}
}

func TestDecodeNodeSettingsFromMap(t *testing.T) {
	// Empty map
	s, err := DecodeNodeSettingsFromMap(nil)
	if err != nil || s == nil || s.Agent != nil {
		t.Fatalf("expected empty struct for nil map, got: %+v, err: %v", s, err)
	}

	// Map with agent data
	m := map[string]any{
		"agent": map[string]any{
			"execBinPath": absTestPath("dcexec"),
		},
	}
	s, err = DecodeNodeSettingsFromMap(m)
	if err != nil {
		t.Fatalf("unexpected error decoding map: %v", err)
	}
	if s.Agent == nil || s.Agent.ExecBinPath != absTestPath("dcexec") {
		t.Errorf("unexpected decoded result: %+v", s)
	}

	_, err = DecodeNodeSettingsFromMap(map[string]any{
		"agent": map[string]any{
			"unknown": true,
		},
	})
	if err == nil {
		t.Fatalf("expected unknown stored field to be rejected, got nil")
	} else {
		var contractErr contract.DomainError
		if !errors.As(err, &contractErr) {
			t.Fatalf("expected contract.DomainError, got %T: %v", err, err)
		}
		if contractErr.HTTPStatus() != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for unknown field, got %d: %v", contractErr.HTTPStatus(), err)
		}
	}

	_, err = DecodeNodeSettingsFromMap(map[string]any{
		"agent": map[string]any{
			"execBinPath": "relative/dcexec",
		},
	})
	if err == nil {
		t.Fatalf("expected invalid stored path to be rejected, got nil")
	} else {
		var contractErr contract.DomainError
		if !errors.As(err, &contractErr) {
			t.Fatalf("expected contract.DomainError, got %T: %v", err, err)
		}
		if contractErr.HTTPStatus() != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for invalid path, got %d: %v", contractErr.HTTPStatus(), err)
		}
	}
}

func TestMergeNodeSettingsPatch(t *testing.T) {
	// 1. Initial patch on empty base
	patch1 := []byte(`{
		"agent": {
			"execBinPath": "` + filepath.ToSlash(absTestPath("dc-exec")) + `",
			"vnc": {"scanInterval": 120}
		},
		"license": {
			"lmutilPath": "` + filepath.ToSlash(absTestPath("lmutil")) + `"
		}
	}`)
	merged1, err := MergeNodeSettingsPatch(nil, patch1)
	if err != nil {
		t.Fatalf("unexpected error on patch1: %v", err)
	}

	// 2. Partial update retaining existing fields
	patch2 := []byte(`{
		"agent": {
			"vnc": {"binDir": "` + filepath.ToSlash(absTestPath("bin")) + `"}
		}
	}`)
	merged2, err := MergeNodeSettingsPatch(merged1, patch2)
	if err != nil {
		t.Fatalf("unexpected error on patch2: %v", err)
	}
	s2, err := DecodeNodeSettingsPayload(merged2)
	if err != nil {
		t.Fatalf("decode merged2 failed: %v", err)
	}
	if s2.Agent == nil || s2.Agent.ExecBinPath != filepath.ToSlash(absTestPath("dc-exec")) {
		t.Errorf("expected execBinPath preserved, got %+v", s2.Agent)
	}
	if s2.Agent.VNC == nil || s2.Agent.VNC.ScanInterval == nil || *s2.Agent.VNC.ScanInterval != 120 || s2.Agent.VNC.BinDir != filepath.ToSlash(absTestPath("bin")) {
		t.Errorf("expected vnc fields merged, got %+v", s2.Agent.VNC)
	}
	if s2.License == nil || s2.License.LmutilPath != filepath.ToSlash(absTestPath("lmutil")) {
		t.Errorf("expected license preserved, got %+v", s2.License)
	}

	// 3. Null deletion of a field
	patch3 := []byte(`{
		"agent": {
			"execBinPath": null
		}
	}`)
	merged3, err := MergeNodeSettingsPatch(merged2, patch3)
	if err != nil {
		t.Fatalf("unexpected error on patch3: %v", err)
	}
	s3, err := DecodeNodeSettingsPayload(merged3)
	if err != nil {
		t.Fatalf("decode merged3 failed: %v", err)
	}
	if s3.Agent.ExecBinPath != "" {
		t.Errorf("expected execBinPath deleted, got %s", s3.Agent.ExecBinPath)
	}

	// 4. Null deletion of a domain
	patch4 := []byte(`{
		"license": null
	}`)
	merged4, err := MergeNodeSettingsPatch(merged3, patch4)
	if err != nil {
		t.Fatalf("unexpected error on patch4: %v", err)
	}
	s4, err := DecodeNodeSettingsPayload(merged4)
	if err != nil {
		t.Fatalf("decode merged4 failed: %v", err)
	}
	if s4.License != nil {
		t.Errorf("expected license domain deleted, got %+v", s4.License)
	}

	// 5. Deletion of remaining domain reverts to "{}"
	patch5 := []byte(`{
		"agent": null
	}`)
	merged5, err := MergeNodeSettingsPatch(merged4, patch5)
	if err != nil {
		t.Fatalf("unexpected error on patch5: %v", err)
	}
	if string(merged5) != "{}" {
		t.Errorf("expected {}, got %s", string(merged5))
	}

	// 6. Reject non-object top-level
	for _, invalid := range [][]byte{[]byte(`[]`), []byte(`"str"`), []byte(`123`), []byte(`true`)} {
		_, err := MergeNodeSettingsPatch(nil, invalid)
		if !errors.Is(err, ErrMalformed) {
			t.Errorf("expected ErrMalformed for non-object %s, got %v", invalid, err)
		}
	}

	// 7. Reject unknown domain (e.g. storage)
	badDomain := []byte(`{"storage": {"foo": "bar"}}`)
	_, err = MergeNodeSettingsPatch(nil, badDomain)
	if err == nil {
		t.Errorf("expected error for unknown domain, got nil")
	} else {
		if contractErr, ok := errors.AsType[contract.DomainError](err); !ok {
			t.Errorf("expected contract.DomainError, got %T: %v", err, err)
		} else if contractErr.HTTPStatus() != http.StatusUnprocessableEntity {
			t.Errorf("expected 422 for unknown domain, got %d: %v", contractErr.HTTPStatus(), err)
		}
	}

	// 8. Reject unknown field in domain
	badField := []byte(`{"agent": {"unknown": 123}}`)
	_, err = MergeNodeSettingsPatch(nil, badField)
	if err == nil {
		t.Errorf("expected error for unknown field, got nil")
	} else {
		if contractErr, ok := errors.AsType[contract.DomainError](err); !ok {
			t.Errorf("expected contract.DomainError, got %T: %v", err, err)
		} else if contractErr.HTTPStatus() != http.StatusUnprocessableEntity {
			t.Errorf("expected 422 for unknown field, got %d: %v", contractErr.HTTPStatus(), err)
		}
	}
}

func TestMergeAgentSettings_DiskMounts(t *testing.T) {
	base := AgentSettings{
		Monitor: MonitorConfig{
			DiskMounts: []DiskMountConfig{
				{Name: "global-data", Path: absTestPath("data"), Enabled: true},
			},
		},
	}

	// 1. nil override -> retain base
	merged := MergeAgentSettings(base, nil)
	if len(merged.Monitor.DiskMounts) != 1 || merged.Monitor.DiskMounts[0].Name != "global-data" {
		t.Fatalf("expected base mounts retained, got: %+v", merged.Monitor.DiskMounts)
	}

	// 2. nil DiskMounts in override -> retain base
	merged = MergeAgentSettings(base, &NodeAgentOverride{
		Monitor: &NodeMonitorOverride{
			NvidiaSmiPath: absTestPath("smi"),
		},
	})
	if len(merged.Monitor.DiskMounts) != 1 || merged.Monitor.DiskMounts[0].Name != "global-data" {
		t.Fatalf("expected base mounts retained when override diskMounts is nil, got: %+v", merged.Monitor.DiskMounts)
	}

	// 3. Custom DiskMounts -> whole slice replacement
	customPath := absTestPath("custom-data")
	merged = MergeAgentSettings(base, &NodeAgentOverride{
		Monitor: &NodeMonitorOverride{
			DiskMounts: []DiskMountConfig{
				{Name: "node-fast", Path: customPath, Enabled: true, OS: "linux"},
			},
		},
	})
	if len(merged.Monitor.DiskMounts) != 1 || merged.Monitor.DiskMounts[0].Name != "node-fast" {
		t.Fatalf("expected custom mounts, got: %+v", merged.Monitor.DiskMounts)
	}

	// 4. Empty slice -> clear extra mounts
	merged = MergeAgentSettings(base, &NodeAgentOverride{
		Monitor: &NodeMonitorOverride{
			DiskMounts: []DiskMountConfig{},
		},
	})
	if len(merged.Monitor.DiskMounts) != 0 {
		t.Fatalf("expected empty mounts, got: %+v", merged.Monitor.DiskMounts)
	}

	// 5. MergeNodeSettingsPatch with diskMounts and null deletion
	patchJSON := []byte(`{
		"agent": {
			"monitor": {
				"diskMounts": [
					{"name": "nvme", "path": "` + filepath.ToSlash(customPath) + `", "enabled": true, "os": "linux"}
				]
			}
		}
	}`)
	mergedJSON, err := MergeNodeSettingsPatch(nil, patchJSON)
	if err != nil {
		t.Fatalf("MergeNodeSettingsPatch failed: %v", err)
	}
	s, err := DecodeNodeSettingsPayload(mergedJSON)
	if err != nil || len(s.Agent.Monitor.DiskMounts) != 1 || s.Agent.Monitor.DiskMounts[0].Name != "nvme" {
		t.Fatalf("unexpected decoded diskMounts: %+v (err=%v)", s, err)
	}

	// Clear diskMounts via null -> reverts to no override for diskMounts
	clearPatch := []byte(`{
		"agent": {
			"monitor": {
				"diskMounts": null
			}
		}
	}`)
	clearedJSON, err := MergeNodeSettingsPatch(mergedJSON, clearPatch)
	if err != nil {
		t.Fatalf("MergeNodeSettingsPatch clear failed: %v", err)
	}
	s2, err := DecodeNodeSettingsPayload(clearedJSON)
	if err != nil {
		t.Fatalf("Decode cleared failed: %v", err)
	}
	if s2.Agent != nil && s2.Agent.Monitor != nil && s2.Agent.Monitor.DiskMounts != nil {
		t.Fatalf("expected diskMounts to be nil, got %+v", s2.Agent.Monitor.DiskMounts)
	}

	// Reject invalid diskMounts (relative path)
	invalidPatch := []byte(`{
		"agent": {
			"monitor": {
				"diskMounts": [
					{"name": "rel", "path": "relative/path", "enabled": true}
				]
			}
		}
	}`)
	_, err = MergeNodeSettingsPatch(nil, invalidPatch)
	if err == nil {
		t.Fatalf("expected error for relative disk mount path, got nil")
	} else {
		var contractErr contract.DomainError
		if !errors.As(err, &contractErr) {
			t.Fatalf("expected contract.DomainError, got %T: %v", err, err)
		}
		if contractErr.HTTPStatus() != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for relative disk mount path, got %d: %v", contractErr.HTTPStatus(), err)
		}
	}
}

func TestMergeSchedulerSettings(t *testing.T) {
	base := SchedulerSettings{Type: JobSchedulerSlurm, EnvScript: "/share/slurm/env.sh", JobCacheTTL: 60}

	if got := MergeSchedulerSettings(base, nil); got != base {
		t.Errorf("nil override changed base: %+v", got)
	}
	if got := MergeSchedulerSettings(base, &NodeSchedulerOverride{}); got != base {
		t.Errorf("empty override changed base: %+v", got)
	}
	got := MergeSchedulerSettings(base, &NodeSchedulerOverride{EnvScript: "/opt/local/env.sh"})
	want := SchedulerSettings{Type: JobSchedulerSlurm, EnvScript: "/opt/local/env.sh", JobCacheTTL: 60}
	if got != want {
		t.Errorf("merged = %+v, want %+v", got, want)
	}
}

func TestNodeSchedulerOverrideContract(t *testing.T) {
	abs := filepath.ToSlash(absTestPath("env.sh"))

	merged, err := MergeNodeSettingsPatch(nil, []byte(`{"scheduler":{"envScript":"`+abs+`"}}`))
	if err != nil {
		t.Fatalf("patch scheduler override: %v", err)
	}
	ns, err := DecodeNodeSettingsPayload(merged)
	if err != nil {
		t.Fatalf("decode merged: %v", err)
	}
	if ns.Scheduler == nil || ns.Scheduler.EnvScript != abs {
		t.Fatalf("scheduler override lost: %+v", ns.Scheduler)
	}

	cleared, err := MergeNodeSettingsPatch(merged, []byte(`{"scheduler":null}`))
	if err != nil {
		t.Fatalf("clear scheduler override: %v", err)
	}
	if string(cleared) != "{}" {
		t.Errorf("cleared = %s, want {}", cleared)
	}

	if _, err := MergeNodeSettingsPatch(nil, []byte(`{"scheduler":{"envScript":"relative/env.sh"}}`)); err == nil {
		t.Error("relative envScript must be rejected")
	}
	// type and jobCacheTTL are cluster-wide and must not be overridable per node.
	if _, err := MergeNodeSettingsPatch(nil, []byte(`{"scheduler":{"type":"slurm"}}`)); err == nil {
		t.Error("scheduler.type must not be a node override")
	}
}
