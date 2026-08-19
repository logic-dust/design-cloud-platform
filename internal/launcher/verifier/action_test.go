//go:build !windows

package verifier

import (
	"testing"

	"design-cloud-engine/internal/execution"
)

func TestAction_UnknownAction(t *testing.T) {
	t.Parallel()
	_, _, err := Action("NON_EXISTENT_ACTION")
	if err == nil {
		t.Fatal("expected error for unknown action, got nil")
	}
}

func TestAction_DirectModeRejected(t *testing.T) {
	t.Parallel()
	// ActionUserList is registered as a direct action (Launcher == nil).
	_, _, err := Action(execution.ActionUserList)
	if err == nil {
		t.Fatal("expected error for direct action via launcher, got nil")
	}
}

func TestAction_StreamAction_ReturnsNilHandler(t *testing.T) {
	t.Parallel()
	// ActionFileUpload is registered with IOModelStreaming.
	handler, spec, err := Action(execution.ActionFileUpload)
	if err != nil {
		t.Fatalf("expected no error for stream action, got: %v", err)
	}
	if handler != nil {
		t.Fatal("expected nil handler for stream action")
	}
	if !spec.IsStream() {
		t.Fatal("expected spec.IsStream() = true for stream action")
	}
}

func TestAction_ValidUserModeAction(t *testing.T) {
	t.Parallel()
	// ActionFileList is registered as target_user launcher action with a handler.
	handler, spec, err := Action(execution.ActionFileList)
	if err != nil {
		t.Fatalf("expected no error for user action, got: %v", err)
	}
	if handler == nil {
		t.Fatal("expected non-nil handler for user action")
	}
	if spec.Launcher == nil || spec.Launcher.Privilege != execution.PrivilegeTargetUser {
		t.Fatalf("launcher privilege = %v, want %v", spec.Launcher, execution.PrivilegeTargetUser)
	}
}
