package execution

import "testing"

func TestMatchLauncherProtocolVersion(t *testing.T) {
	if err := MatchLauncherProtocolVersion("0.5.0", "0.5.0"); err != nil {
		t.Fatalf("same version must pass: %v", err)
	}
	if err := MatchLauncherProtocolVersion("", ""); err != nil {
		t.Fatalf("empty/empty (dev builds without ldflags) must pass: %v", err)
	}
	if err := MatchLauncherProtocolVersion("0.5.0", "0.4.0"); err == nil {
		t.Fatal("mismatch must fail")
	}
	if err := MatchLauncherProtocolVersion("0.5.0", ""); err == nil {
		t.Fatal("engine version without launcher ldflags must fail")
	}
}
