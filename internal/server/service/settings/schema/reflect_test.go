package schema

import (
	"testing"
)

type mockSettings struct {
	Enabled   bool   `json:"enabled"`
	Threshold int    `json:"threshold" validate:"min=10,max=100,enum=-1"`
	AllowZero int    `json:"allowZero" validate:"min=10,max=100,allowzero"`
	IgnoreMe  string `json:"-"`
	Name      string
}

func TestGenerateJSONSchema(t *testing.T) {
	mock := mockSettings{
		Enabled:   true,
		Threshold: 42,
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
	if thresholdSchema["minimum"] != float64(10) {
		t.Errorf("expected threshold minimum to be 10, got %v", thresholdSchema["minimum"])
	}
	if thresholdSchema["maximum"] != float64(100) {
		t.Errorf("expected threshold maximum to be 100, got %v", thresholdSchema["maximum"])
	}
	if enumVals, ok := thresholdSchema["enum"].([]any); !ok || len(enumVals) != 1 || enumVals[0] != float64(-1) {
		t.Errorf("expected threshold enum to be [-1], got %v", thresholdSchema["enum"])
	}

	azSchema := props["allowZero"].(map[string]any)
	if az, ok := azSchema["allowZero"].(bool); !ok || !az {
		t.Errorf("expected allowZero to be true, got %v", azSchema["allowZero"])
	}

	// Check fallback to field name if no json tag
	nameSchema := props["Name"].(map[string]any)
	if nameSchema["type"] != "string" {
		t.Errorf("expected Name to be string")
	}
	if nameSchema["default"] != "test-name" {
		t.Errorf("expected Name default to be test-name")
	}

	// Check ignored fields
	if _, exists := props["IgnoreMe"]; exists {
		t.Errorf("expected IgnoreMe to be excluded")
	}
	if _, exists := props["-"]; exists {
		t.Errorf("expected - to be excluded")
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
