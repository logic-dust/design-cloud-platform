package schema

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
)

func TestMergeAgentSettings(t *testing.T) {
	base := AgentSettings{
		LauncherPath: absTestPath("dc-launcher"),
		VNC: VNCConfig{
			BinDir:       absTestPath("turbovnc", "bin"),
			ScanInterval: 1800,
			ScanEnabled:  true,
			ServerArgs:   []string{"-wm", "2d"},
		},
		Monitor: MonitorConfig{
			NvidiaSmiPath:          absTestPath("nvidia-smi"),
			ResourceReportEnabled:  true,
			ResourceReportInterval: 60,
		},
	}

	// Case 1: nil override returns unchanged base
	merged := MergeAgentSettings(base, nil)
	if merged.LauncherPath != base.LauncherPath {
		t.Errorf("expected LauncherPath %s, got %s", base.LauncherPath, merged.LauncherPath)
	}

	// Case 2: Sparse override with zero values and cleared slice
	zeroInt := 0
	falseBool := false
	override := &NodeAgentOverride{
		LauncherPath: absTestPath("custom", "bin", "launcher"),
		VNC: &NodeVNCOverride{
			BinDir:       absTestPath("bin"),
			ScanInterval: &zeroInt,
			ScanEnabled:  &falseBool,
			ServerArgs:   []string{}, // explicitly clear
		},
		Monitor: &NodeMonitorOverride{
			ResourceReportInterval: &zeroInt,
		},
	}

	merged = MergeAgentSettings(base, override)
	if merged.LauncherPath != absTestPath("custom", "bin", "launcher") {
		t.Errorf("expected LauncherPath /custom/bin/launcher, got %s", merged.LauncherPath)
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
	if merged.Monitor.ResourceReportInterval != 0 {
		t.Errorf("expected Monitor.ResourceReportInterval 0, got %d", merged.Monitor.ResourceReportInterval)
	}
	if merged.Monitor.NvidiaSmiPath != base.Monitor.NvidiaSmiPath {
		t.Errorf("expected unchanged GPU path, got %s", merged.Monitor.NvidiaSmiPath)
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

	// 3. Deep unknown field -> fs.ErrInvalid
	unknownKeyJSON := []byte(`{
		"agent": {
			"vnc": {
				"unknownField": "value"
			}
		}
	}`)
	_, err = DecodeNodeSettingsPayload(unknownKeyJSON)
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("expected fs.ErrInvalid for unknown field, got: %v", err)
	}

	// 4. Multiple root objects -> ErrMalformed or fs.ErrInvalid
	extraJSON := []byte(`{"agent": {}} {"extra": 1}`)
	_, err = DecodeNodeSettingsPayload(extraJSON)
	if !errors.Is(err, ErrMalformed) && !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("expected ErrMalformed or fs.ErrInvalid for extra data, got: %v", err)
	}
}

func TestValidateNodeSettings(t *testing.T) {
	valid := &NodeSettings{
		Agent: &NodeAgentOverride{
			LauncherPath: absTestPath("dc-launcher"),
			VNC: &NodeVNCOverride{
				BinDir: "C:\\opt\\turbovnc", // Windows absolute path
			},
		},
	}
	if err := ValidateNodeSettings(valid); err != nil {
		t.Errorf("expected valid NodeSettings, got: %v", err)
	}

	// Out of bounds scanInterval via pointer
	badInterval := 10 // min is 60
	badSettings := &NodeSettings{
		Agent: &NodeAgentOverride{
			VNC: &NodeVNCOverride{
				ScanInterval: &badInterval,
			},
		},
	}
	if err := ValidateNodeSettings(badSettings); err == nil {
		t.Error("expected error for scanInterval below min 60")
	}

	// Relative launcher path
	badPath := &NodeSettings{
		Agent: &NodeAgentOverride{
			LauncherPath: "relative/path",
		},
	}
	if err := ValidateNodeSettings(badPath); err == nil {
		t.Error("expected error for relative launcher path")
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
			"launcherPath": absTestPath("launcher"),
		},
	}
	s, err = DecodeNodeSettingsFromMap(m)
	if err != nil {
		t.Fatalf("unexpected error decoding map: %v", err)
	}
	if s.Agent == nil || s.Agent.LauncherPath != absTestPath("launcher") {
		t.Errorf("unexpected decoded result: %+v", s)
	}

	_, err = DecodeNodeSettingsFromMap(map[string]any{
		"agent": map[string]any{
			"unknown": true,
		},
	})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("expected unknown stored field to be rejected, got %v", err)
	}

	_, err = DecodeNodeSettingsFromMap(map[string]any{
		"agent": map[string]any{
			"launcherPath": "relative/launcher",
		},
	})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("expected invalid stored path to be rejected, got %v", err)
	}
}

func TestMergeNodeSettingsPatch(t *testing.T) {
	// 1. Initial patch on empty base
	patch1 := []byte(`{
		"agent": {
			"launcherPath": "` + filepath.ToSlash(absTestPath("dc-launcher")) + `",
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
	if s2.Agent == nil || s2.Agent.LauncherPath != filepath.ToSlash(absTestPath("dc-launcher")) {
		t.Errorf("expected launcherPath preserved, got %+v", s2.Agent)
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
			"launcherPath": null
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
	if s3.Agent.LauncherPath != "" {
		t.Errorf("expected launcherPath deleted, got %s", s3.Agent.LauncherPath)
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
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("expected fs.ErrInvalid for unknown domain, got %v", err)
	}

	// 8. Reject unknown field in domain
	badField := []byte(`{"agent": {"unknown": 123}}`)
	_, err = MergeNodeSettingsPatch(nil, badField)
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("expected fs.ErrInvalid for unknown field, got %v", err)
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
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("expected fs.ErrInvalid for relative disk mount path, got: %v", err)
	}
}
