package execution

import (
	"encoding/json"
	"fmt"
)

// GetPayload extracts and type-asserts the Payload from ExecuteRequest.
// Returns error if Payload is nil, wrong type, or mismatches the registered type for the action.
func GetPayload[T any](req *ExecuteRequest) (*T, error) {
	if req.Payload == nil {
		return nil, fmt.Errorf("payload is required")
	}

	// Validate that *T matches the registered payload type for this action.
	if spec, ok := Actions[req.ActionName]; ok && spec.NewPayload != nil {
		expected := spec.NewPayload()
		if _, match := expected.(*T); !match {
			return nil, fmt.Errorf("payload type mismatch for action %s: expected %T, got *%T", req.ActionName, expected, new(T))
		}
	}

	if ptr, ok := req.Payload.(*T); ok {
		return ptr, nil
	}
	if val, ok := req.Payload.(T); ok {
		return &val, nil
	}

	var rawBytes []byte
	switch p := req.Payload.(type) {
	case json.RawMessage:
		rawBytes = p
	case []byte:
		rawBytes = p
	case string:
		rawBytes = []byte(p)
	default:
		return nil, fmt.Errorf("invalid payload type: expected %T, got %T", new(T), req.Payload)
	}

	var result T
	if err := json.Unmarshal(rawBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}
	return &result, nil
}
