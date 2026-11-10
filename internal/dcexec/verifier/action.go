//go:build !windows

package verifier

import (
	"fmt"

	"design-cloud-engine/internal/dcexec"
	"design-cloud-engine/internal/execution"
)

// Action performs L3 allowlist check and returns the PrivilegedExec handler plus unified action spec.
// This is the third layer of the 4-layer defense-in-depth security model.
//
// L3 validates operation authorization and parameters by:
// - Checking action is in allowlist
// - Validating action mode (User vs Privileged)
// - Ensuring handler exists for the action (skipped for IsStream actions)
//
// L3 cannot be replaced by L4 (seccomp) because:
// - seccomp cannot check business logic (which Actions are allowed)
// - seccomp cannot validate parameter semantics (path traversal, injection)
// - seccomp can only restrict system calls, not application-level operations
func Action(actionName execution.ActionName) (dcexec.Handler, execution.ActionSpec, error) {
	spec, ok := execution.Actions[actionName]
	if !ok {
		return nil, execution.ActionSpec{}, fmt.Errorf("action not allowed: %s", actionName)
	}
	if spec.IsDirect() {
		return nil, execution.ActionSpec{}, fmt.Errorf("action %s is not allowed via dc-exec", actionName)
	}
	// Stream actions are handled directly in main (not via the handler registry).
	if spec.IsStream() {
		return nil, spec, nil
	}
	handler, exists := dcexec.Handlers[actionName]
	if !exists {
		return nil, execution.ActionSpec{}, fmt.Errorf("action not allowed: %s", actionName)
	}
	return handler, spec, nil
}
