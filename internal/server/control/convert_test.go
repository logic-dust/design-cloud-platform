package control

import (
	"strings"
	"testing"

	"design-cloud-engine/internal/execution"
)

func TestToCommandRequestEncodesPayloadJSONOutsideEnv(t *testing.T) {
	t.Parallel()

	req := execution.NewExecuteRequest(
		execution.ActionUserList,
		"request-1",
		execution.WithPayload(map[string]any{"username": "alice"}),
	)

	cmd, err := toCommandRequest(req)
	if err != nil {
		t.Fatalf("to command request: %v", err)
	}

	if !strings.Contains(cmd.GetPayloadJson(), `"username":"alice"`) {
		t.Fatalf("payload_json = %q, want encoded payload", cmd.GetPayloadJson())
	}
	if _, ok := cmd.GetEnv()["payload_json"]; ok {
		t.Fatalf("env must not contain payload_json: %#v", cmd.GetEnv())
	}
}

func TestToCommandRequestRejectsStringPayload(t *testing.T) {
	t.Parallel()

	req := execution.NewExecuteRequest(
		execution.ActionUserList,
		"request-1",
		execution.WithPayload(`{"username":"alice"}`),
	)

	if _, err := toCommandRequest(req); err == nil {
		t.Fatalf("expected string payload error")
	}
}
