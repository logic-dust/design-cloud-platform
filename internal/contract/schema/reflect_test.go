package schema

import (
	"reflect"
	"strings"
	"testing"
)

type mockSettings struct {
	Enabled   bool   `json:"enabled"`
	Threshold int    `json:"threshold" validate:"min=10,max=100" unit:"s"`
	Category  int    `json:"category" validate:"enum=-1|1|2"`
	AllowZero int    `json:"allowZero" validate:"min=10,max=100,allowzero" unit:"B"`
	DiskSpace int64  `json:"diskSpace" unit:"KiB"`
	IgnoreMe  string `json:"-"`
	Name      string
}

func TestGenerateJSONSchema(t *testing.T) {
	mock := mockSettings{
		Enabled:   true,
		Threshold: 42,
		Category:  1,
		DiskSpace: 1024,
		Name:      "test-name",
	}
	schema := GenerateJSONSchema(mock)

	// It should return an object type
	if schema["type"] != "object" {
		t.Errorf("expected type object, got %v", schema["type"])
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties to be a map")
	}

	// Check mapped names, types and default values
	enabledSchema := props["enabled"].(map[string]any)
	if enabledSchema["type"] != "boolean" {
		t.Errorf("expected enabled to be boolean")
	}
	if def, ok := enabledSchema["default"].(bool); !ok || !def {
		t.Errorf("expected enabled default to be true")
	}

	thresholdSchema := props["threshold"].(map[string]any)
	if thresholdSchema["type"] != "number" {
		t.Errorf("expected threshold to be number")
	}
	// reflect.Int returns int64
	if thresholdSchema["default"] != int64(42) {
		t.Errorf("expected threshold default to be 42")
	}
	if thresholdSchema["minimum"] != int64(10) {
		t.Errorf("expected threshold minimum to be 10, got %v", thresholdSchema["minimum"])
	}
	if thresholdSchema["maximum"] != int64(100) {
		t.Errorf("expected threshold maximum to be 100, got %v", thresholdSchema["maximum"])
	}
	if thresholdSchema["unit"] != "s" {
		t.Errorf("expected threshold unit to be s, got %v", thresholdSchema["unit"])
	}

	categorySchema := props["category"].(map[string]any)
	if enumVals, ok := categorySchema["enum"].([]any); !ok || len(enumVals) != 3 || enumVals[0] != int64(-1) || enumVals[1] != int64(1) || enumVals[2] != int64(2) {
		t.Errorf("expected category enum to be [-1 1 2], got %v", categorySchema["enum"])
	}

	azSchema := props["allowZero"].(map[string]any)
	if az, ok := azSchema["allowZero"].(bool); !ok || !az {
		t.Errorf("expected allowZero to be true, got %v", azSchema["allowZero"])
	}
	if azSchema["unit"] != "B" {
		t.Errorf("expected allowZero unit to be B, got %v", azSchema["unit"])
	}

	diskSchema := props["diskSpace"].(map[string]any)
	if diskSchema["unit"] != "KiB" {
		t.Errorf("expected diskSpace unit to be KiB, got %v", diskSchema["unit"])
	}

	// Check fallback to field name if no json tag
	nameSchema := props["Name"].(map[string]any)
	if nameSchema["type"] != "string" {
		t.Errorf("expected Name to be string")
	}
	if nameSchema["default"] != "test-name" {
		t.Errorf("expected Name default to be test-name")
	}
	if _, exists := nameSchema["unit"]; exists {
		t.Errorf("expected Name to not have unit")
	}

	// Check ignored fields
	if _, exists := props["IgnoreMe"]; exists {
		t.Errorf("expected IgnoreMe to be excluded")
	}
	if _, exists := props["-"]; exists {
		t.Errorf("expected - to be excluded")
	}
}

func TestGenerateJSONSchema_NilPointer(t *testing.T) {
	var ptr *mockSettings
	schema := GenerateJSONSchema(ptr)
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties to be a map")
	}
	thresholdSchema := props["threshold"].(map[string]any)
	if thresholdSchema["unit"] != "s" {
		t.Errorf("expected nil pointer schema threshold unit to be s, got %v", thresholdSchema["unit"])
	}
	diskSchema := props["diskSpace"].(map[string]any)
	if diskSchema["unit"] != "KiB" {
		t.Errorf("expected nil pointer schema diskSpace unit to be KiB, got %v", diskSchema["unit"])
	}
}

// validUnits 是 Settings JSON Schema 允许的 unit 封闭集合（国际标准符号），仅用于下方守卫单测，
// 防止 struct tag 拼写错误（Go 无编译期 tag 校验）。
var validUnits = map[string]struct{}{
	"s":   {}, // 秒
	"h":   {}, // 小时
	"d":   {}, // 天
	"B":   {}, // 字节
	"KiB": {}, // 1024 字节（与 Linux 监控采集粒度一致）
}

// unitBySuffix 约定：字段名带下列后缀的整数字段必须声明对应 unit，避免新增字段漏标或单位不一致。
var unitBySuffix = map[string]string{
	"TTL":       "s",
	"Interval":  "s",
	"Duration":  "s",
	"Window":    "s",
	"Days":      "d",
	"Hours":     "h",
	"Bytes":     "B",
	"FreeSpace": "KiB",
}

func isIntegerLike(rt reflect.Type) bool {
	for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice {
		rt = rt.Elem()
	}
	switch rt.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
}

func TestAllSettingsUnitsAreValid(t *testing.T) {
	allRoots := []any{
		PlatformSettings{},
		ConnectionSettings{},
		FileSettings{},
		ProxySettings{},
		AuthSettings{},
		AgentSettings{},
		SchedulerSettings{},
		OverviewSettings{},
		StorageSettings{},
		DesktopSettings{},
		AlertSettings{},
		CertSettings{},
		LicenseSettings{},
		NodeSettings{},
	}

	visited := make(map[reflect.Type]bool)

	var checkType func(rt reflect.Type)
	checkType = func(rt reflect.Type) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || visited[rt] {
			return
		}
		visited[rt] = true

		for f := range rt.Fields() {
			f := f
			unitTag := f.Tag.Get("unit")
			if unitTag != "" {
				if _, ok := validUnits[unitTag]; !ok {
					t.Errorf("%s.%s: invalid unit tag %q; must be one of %v", rt.Name(), f.Name, unitTag, validUnits)
				}
			}
			if isIntegerLike(f.Type) {
				for suffix, want := range unitBySuffix {
					if strings.HasSuffix(f.Name, suffix) && unitTag != want {
						t.Errorf("%s.%s: name suffix %q requires unit:%q, got %q", rt.Name(), f.Name, suffix, want, unitTag)
					}
				}
			}
			checkType(f.Type)
		}
	}

	for _, root := range allRoots {
		checkType(reflect.TypeOf(root))
	}
}

func TestGenerateJSONSchema_NodeSettings(t *testing.T) {
	schema := GenerateJSONSchema(NodeSettings{})
	props := schema["properties"].(map[string]any)
	agentSchema := props["agent"].(map[string]any)
	agentProps := agentSchema["properties"].(map[string]any)
	monitorSchema := agentProps["monitor"].(map[string]any)
	monitorProps := monitorSchema["properties"].(map[string]any)
	diskMountsSchema := monitorProps["diskMounts"].(map[string]any)
	if diskMountsSchema["type"] != "array" {
		t.Errorf("expected diskMounts type array, got %v", diskMountsSchema["type"])
	}
}

func TestGenerateJSONSchema_PlatformNameLength(t *testing.T) {
	s := GenerateJSONSchema(PlatformSettings{})
	props := s["properties"].(map[string]any)
	nameSchema := props["name"].(map[string]any)
	if nameSchema["minLength"] != 1 {
		t.Errorf("name minLength = %v, want 1", nameSchema["minLength"])
	}
	if nameSchema["maxLength"] != 64 {
		t.Errorf("name maxLength = %v, want 64", nameSchema["maxLength"])
	}
}

func TestGenerateJSONSchema_PlatformDisabledModules(t *testing.T) {
	defaults, ok := DefaultSettings(DomainPlatform)
	if !ok {
		t.Fatal("platform defaults missing")
	}

	schema := GenerateJSONSchema(defaults)
	props := schema["properties"].(map[string]any)
	modulesSchema := props["disabledModules"].(map[string]any)
	moduleProperties := modulesSchema["properties"].(map[string]any)
	for _, module := range userPortalModuleIDs {
		moduleSchema, ok := moduleProperties[module].(map[string]any)
		if !ok {
			t.Errorf("disabledModules schema missing %q", module)
			continue
		}
		if moduleSchema["type"] != "boolean" || moduleSchema["default"] != false {
			t.Errorf("disabledModules.%s schema = %v, want boolean default false", module, moduleSchema)
		}
	}
}

func TestGenerateJSONSchema_SchedulerTypeEnum(t *testing.T) {
	s := GenerateJSONSchema(SchedulerSettings{Type: JobSchedulerLSF})
	props := s["properties"].(map[string]any)
	typeSchema := props["type"].(map[string]any)
	got, ok := typeSchema["enum"].([]any)
	if !ok {
		t.Fatalf("scheduler.type schema has no enum: %v", typeSchema)
	}
	want := []any{"lsf", "slurm", "openlava"}
	if len(got) != len(want) {
		t.Fatalf("enum = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("enum = %v, want %v", got, want)
		}
	}
	for _, typ := range []JobScheduler{JobSchedulerLSF, JobSchedulerSlurm, JobSchedulerOpenLava} {
		if err := validateSchedulerSettings(SchedulerSettings{Type: typ, JobCacheTTL: 60}); err != nil {
			t.Errorf("type %q rejected: %v", typ, err)
		}
	}
	if err := validateSchedulerSettings(SchedulerSettings{Type: "pbs", JobCacheTTL: 60}); err == nil {
		t.Error("type pbs should be rejected")
	}
}

func TestGenerateJSONSchema_CertBackoffScheduleHoursItems(t *testing.T) {
	s := GenerateJSONSchema(CertSettings{
		BackoffScheduleHours: []int{1, 6, 24},
	})
	props := s["properties"].(map[string]any)
	backoffSchema, ok := props["backoffScheduleHours"].(map[string]any)
	if !ok {
		t.Fatalf("missing backoffScheduleHours: %v", props)
	}
	if backoffSchema["type"] != "array" {
		t.Fatalf("expected type array, got %v", backoffSchema["type"])
	}
	if backoffSchema["unit"] != "h" {
		t.Fatalf("expected unit h on array, got %v", backoffSchema["unit"])
	}
	items, ok := backoffSchema["items"].(map[string]any)
	if !ok {
		t.Fatalf("missing items in backoffScheduleHours schema: %v", backoffSchema)
	}
	if items["minimum"] != int64(1) {
		t.Errorf("expected items.minimum=1, got %v (%T)", items["minimum"], items["minimum"])
	}
	if items["maximum"] != int64(168) {
		t.Errorf("expected items.maximum=168, got %v (%T)", items["maximum"], items["maximum"])
	}
}

func TestGenerateJSONSchema_PanicsOnInvalidTag(t *testing.T) {
	t.Run("combining enum with min", func(t *testing.T) {
		type badStruct struct {
			Field int `validate:"min=10,enum=1|2"`
		}
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic on combining enum with min/max, got nil")
			}
		}()
		GenerateJSONSchema(badStruct{})
	})

	t.Run("inverted min max", func(t *testing.T) {
		type badStruct struct {
			Field int `validate:"min=100,max=10"`
		}
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic on min > max, got nil")
			}
		}()
		GenerateJSONSchema(badStruct{})
	})
}
