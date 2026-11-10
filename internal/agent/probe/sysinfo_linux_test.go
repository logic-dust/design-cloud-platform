//go:build linux

package probe

import (
	"testing"
)

func TestDetectSysInfoLinux(t *testing.T) {
	d := DetectSysInfo()
	if d.KernelVersion == "" {
		t.Fatal("expected non-empty kernel release on linux")
	}
}
