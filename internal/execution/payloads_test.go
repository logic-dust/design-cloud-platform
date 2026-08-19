package execution_test

import (
	"encoding/json"
	"testing"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
)

func TestGetPayload(t *testing.T) {
	t.Parallel()

	t.Run("nil payload", func(t *testing.T) {
		req := &execution.ExecuteRequest{
			ActionName: execution.ActionLicenseStatus,
			Payload:    nil,
		}
		_, err := execution.GetPayload[payload.LicenseStatus](req)
		if err == nil {
			t.Fatal("expected error for nil payload")
		}
	})

	t.Run("direct pointer", func(t *testing.T) {
		expected := &payload.LicenseStatus{LicensePath: "/opt/flexlm/license.dat"}
		req := &execution.ExecuteRequest{
			ActionName: execution.ActionLicenseStatus,
			Payload:    expected,
		}
		got, err := execution.GetPayload[payload.LicenseStatus](req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.LicensePath != expected.LicensePath {
			t.Fatalf("expected %s, got %s", expected.LicensePath, got.LicensePath)
		}
	})

	t.Run("value type", func(t *testing.T) {
		expected := payload.LicenseStatus{LicensePath: "/opt/flexlm/license.dat"}
		req := &execution.ExecuteRequest{
			ActionName: execution.ActionLicenseStatus,
			Payload:    expected,
		}
		got, err := execution.GetPayload[payload.LicenseStatus](req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.LicensePath != expected.LicensePath {
			t.Fatalf("expected %s, got %s", expected.LicensePath, got.LicensePath)
		}
	})

	t.Run("json.RawMessage", func(t *testing.T) {
		req := &execution.ExecuteRequest{
			ActionName: execution.ActionLicenseStatus,
			Payload:    json.RawMessage(`{"licensePath":"/opt/flexlm/license.dat"}`),
		}
		got, err := execution.GetPayload[payload.LicenseStatus](req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.LicensePath != "/opt/flexlm/license.dat" {
			t.Fatalf("expected licensePath, got %s", got.LicensePath)
		}
	})

	t.Run("byte slice", func(t *testing.T) {
		req := &execution.ExecuteRequest{
			ActionName: execution.ActionLicenseStatus,
			Payload:    []byte(`{"licensePath":"/opt/flexlm/license.dat"}`),
		}
		got, err := execution.GetPayload[payload.LicenseStatus](req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.LicensePath != "/opt/flexlm/license.dat" {
			t.Fatalf("expected licensePath, got %s", got.LicensePath)
		}
	})

	t.Run("string", func(t *testing.T) {
		req := &execution.ExecuteRequest{
			ActionName: execution.ActionLicenseStatus,
			Payload:    `{"licensePath":"/opt/flexlm/license.dat"}`,
		}
		got, err := execution.GetPayload[payload.LicenseStatus](req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.LicensePath != "/opt/flexlm/license.dat" {
			t.Fatalf("expected licensePath, got %s", got.LicensePath)
		}
	})

	t.Run("map[string]any rejected", func(t *testing.T) {
		req := &execution.ExecuteRequest{
			ActionName: execution.ActionLicenseStatus,
			Payload: map[string]any{
				"licensePath": "/opt/flexlm/license.dat",
			},
		}
		_, err := execution.GetPayload[payload.LicenseStatus](req)
		if err == nil {
			t.Fatal("expected error for map[string]any payload")
		}
	})

	t.Run("type mismatch with registered spec", func(t *testing.T) {
		req := &execution.ExecuteRequest{
			ActionName: execution.ActionLicenseStatus,
			Payload:    &payload.LicenseFeatures{LicensePath: "test"},
		}
		_, err := execution.GetPayload[payload.LicenseFeatures](req)
		if err == nil {
			t.Fatal("expected spec type mismatch error")
		}
	})

	t.Run("invalid json bytes", func(t *testing.T) {
		req := &execution.ExecuteRequest{
			ActionName: execution.ActionLicenseStatus,
			Payload:    []byte(`{invalid`),
		}
		_, err := execution.GetPayload[payload.LicenseStatus](req)
		if err == nil {
			t.Fatal("expected unmarshal error for invalid json bytes")
		}
	})
}
