package execution

import "testing"

func TestMatchDcExecProtocolVersion(t *testing.T) {
	if err := MatchDcExecProtocolVersion("0.5.0", "0.5.0"); err != nil {
		t.Fatalf("same version must pass: %v", err)
	}
	if err := MatchDcExecProtocolVersion("", ""); err != nil {
		t.Fatalf("empty/empty (dev builds without ldflags) must pass: %v", err)
	}
	if err := MatchDcExecProtocolVersion("0.5.0", "0.4.0"); err == nil {
		t.Fatal("mismatch must fail")
	}
	if err := MatchDcExecProtocolVersion("0.5.0", ""); err == nil {
		t.Fatal("engine version without PrivilegedExec ldflags must fail")
	}
}
