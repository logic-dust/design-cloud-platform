//go:build linux

package probe

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeMockProcess(t *testing.T, root, pid, cmdline, status, ppidComm, environ, cgroup string) {
	t.Helper()
	dir := filepath.Join(root, pid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644); err != nil {
		t.Fatalf("write cmdline: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
		t.Fatalf("write status: %v", err)
	}
	if environ != "" {
		if err := os.WriteFile(filepath.Join(dir, "environ"), []byte(environ), 0o644); err != nil {
			t.Fatalf("write environ: %v", err)
		}
	}
	if cgroup != "" {
		if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte(cgroup), 0o644); err != nil {
			t.Fatalf("write cgroup: %v", err)
		}
	}
	if ppidComm != "" {
		// Mock PPid status parsing
		ppid := "9999"
		ppidDir := filepath.Join(root, ppid)
		_ = os.MkdirAll(ppidDir, 0o755)
		_ = os.WriteFile(filepath.Join(ppidDir, "comm"), []byte(ppidComm+"\n"), 0o644)
	}
}

func TestCollectConnectionRuntime_FromProc(t *testing.T) {
	tmpDir := t.TempDir()
	origProc := procFSRoot
	procFSRoot = tmpDir
	defer func() { procFSRoot = origProc }()

	// 1. Valid VNC process
	cmdlineVNC := "Xvnc\x00:1\x00-desktop\x00My Desktop\x00-rfbport\x005901\x00"
	statusVNC := "Name:\tXvnc\nUid:\t1000\t1000\t1000\t1000\nPPid:\t1\n"
	writeMockProcess(t, tmpDir, "1234", cmdlineVNC, statusVNC, "", "", "")

	// 2. App / job GUI seat (dce-app* must be excluded)
	cmdlineApp := "Xvnc\x00:2\x00-desktop\x00dce-app-123\x00-rfbport\x005902\x00"
	statusApp := "Name:\tXvnc\nUid:\t1000\t1000\t1000\t1000\nPPid:\t1\n"
	writeMockProcess(t, tmpDir, "9012", cmdlineApp, statusApp, "", "", "")

	// 3. XRDP Xorg seat
	cmdlineXRDP := "/usr/libexec/Xorg\x00:10\x00-config\x00xrdp/xorg.conf\x00"
	statusXRDP := "Name:\tXorg\nUid:\t1000\t1000\t1000\t1000\nPPid:\t1\n"
	writeMockProcess(t, tmpDir, "5555", cmdlineXRDP, statusXRDP, "", "", "")

	// 4. Plain local Xorg without xrdp — ignored
	cmdlineLocal := "/usr/libexec/Xorg\x00:0\x00-seat\x00seat0\x00"
	statusLocal := "Name:\tXorg\nUid:\t1000\t1000\t1000\t1000\nPPid:\t1\n"
	writeMockProcess(t, tmpDir, "1", cmdlineLocal, statusLocal, "", "", "")

	// 5. VNC process without explicit -rfbport flag on cmdline, defaults to 5900+display (5903)
	cmdlineNoFlag := "Xvnc\x00:3\x00-desktop\x00Wild Desktop\x00"
	statusNoFlag := "Name:\tXvnc\nUid:\t1000\t1000\t1000\t1000\nPPid:\t1\n"
	writeMockProcess(t, tmpDir, "2345", cmdlineNoFlag, statusNoFlag, "", "", "")

	snapshot, err := CollectConnectionRuntime(context.Background())
	if err != nil {
		t.Fatalf("CollectConnectionRuntime failed: %v", err)
	}

	if len(snapshot.VNCSeats) != 2 {
		t.Fatalf("expected 2 VNC desktops, got %d: %+v", len(snapshot.VNCSeats), snapshot.VNCSeats)
	}
	var vnc1, vnc3 *VNCSeatSnapshot
	for i := range snapshot.VNCSeats {
		switch snapshot.VNCSeats[i].PID {
		case "1234":
			vnc1 = &snapshot.VNCSeats[i]
		case "2345":
			vnc3 = &snapshot.VNCSeats[i]
		}
	}
	if vnc1 == nil || vnc1.Display != 1 || vnc1.Port != 5901 {
		t.Errorf("unexpected VNC desktop 1: %+v", vnc1)
	}
	if vnc3 == nil || vnc3.Display != 3 || vnc3.Port != 5903 {
		t.Errorf("unexpected VNC desktop 3 (computed 5900+display): %+v", vnc3)
	}
	if len(snapshot.XRDPSeats) != 1 {
		t.Fatalf("expected 1 XRDP desktop, got %d", len(snapshot.XRDPSeats))
	}
	xrdp := snapshot.XRDPSeats[0]
	if xrdp.PID != "5555" || xrdp.Display != 10 {
		t.Errorf("unexpected XRDP desktop: %+v", xrdp)
	}

	testCases := []struct {
		args string
		want int
		ok   bool
	}{
		{"Xvnc :1 -rfbport 5901", 1, true},
		{"/usr/bin/Xtigervnc :2 -desktop test", 2, true},
		{"/usr/libexec/Xorg :10 -config xrdp/xorg.conf", 10, true},
		{"xorgxrdp :11 -auth foo", 11, true},
		{"other :12", 0, false},
	}
	for _, tc := range testCases {
		got, ok := parseDisplayNumber(tc.args)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseDisplayNumber(%q) = (%d, %v), want (%d, %v)", tc.args, got, ok, tc.want, tc.ok)
		}
	}
}

func TestLookupUsernameByUID(t *testing.T) {
	// Empty UID returns empty string
	if got := lookupUsernameByUID(""); got != "" {
		t.Errorf("expected empty string for empty UID, got %q", got)
	}

	// UID 0 is root
	if got := lookupUsernameByUID("0"); got != "root" {
		t.Errorf("expected root for UID 0, got %q", got)
	}
}
