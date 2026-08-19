package probe

import (
	"path/filepath"
	"testing"

	pb "design-cloud-engine/internal/contract/proto"
)

func TestResolveMountTargets_Windows(t *testing.T) {
	configured := []*pb.MountTarget{
		{Name: "extra_win", Path: `Z:\`, Enabled: true, Os: "windows"},
		{Name: "extra_linux", Path: "/data", Enabled: true, Os: "linux"},
		{Name: "disabled", Path: `Y:\`, Enabled: false, Os: "windows"},
	}

	got := ResolveMountTargets(configured)
	drives := fixedLogicalDrives()
	// Should contain all fixed drives + Z:\ (extra_win)
	expectedCount := len(drives) + 1
	if len(got) != expectedCount {
		t.Fatalf("expected %d targets, got %d: %+v", expectedCount, len(got), got)
	}

	foundZ := false
	for _, target := range got {
		if filepath.Clean(target.GetPath()) == `\` {
			t.Errorf("expanded target %q must name a concrete drive, not the current-drive root", target.GetPath())
		}
		if target.GetPath() == `Z:\` {
			foundZ = true
		}
		if target.GetPath() == "/data" {
			t.Errorf("did not expect Linux target /data on Windows")
		}
	}
	if !foundZ {
		t.Errorf("expected Z:\\ to be included")
	}
}
