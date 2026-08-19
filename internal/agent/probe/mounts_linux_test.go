package probe

import (
	"testing"

	pb "design-cloud-engine/internal/contract/proto"
)

func TestResolveMountTargets_Linux(t *testing.T) {
	configured := []*pb.MountTarget{
		{Name: "data", Path: "/data", Enabled: true, Os: "linux"},
		{Name: "all_os", Path: "/mnt/share", Enabled: true, Os: ""},
		{Name: "win_only", Path: `E:\`, Enabled: true, Os: "windows"},
		{Name: "disabled", Path: "/disabled", Enabled: false, Os: "linux"},
		{Name: "dup_root", Path: "/", Enabled: true, Os: "linux"},
	}

	got := ResolveMountTargets(configured)
	if len(got) != 3 {
		t.Fatalf("expected 3 targets (root, /data, /mnt/share), got %d: %+v", len(got), got)
	}

	if got[0].GetPath() != "/" || got[0].GetName() != "root" {
		t.Errorf("expected first target to be root '/', got %+v", got[0])
	}
	if got[1].GetPath() != "/data" {
		t.Errorf("expected second target to be /data, got %+v", got[1])
	}
	if got[2].GetPath() != "/mnt/share" {
		t.Errorf("expected third target to be /mnt/share, got %+v", got[2])
	}
}
