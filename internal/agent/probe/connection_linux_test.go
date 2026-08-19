package probe

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseVNCConnectionDetailsUsesRFBPort(t *testing.T) {
	t.Parallel()

	args := "/opt/TurboVNC/bin/Xvnc :1 -desktop TurboVNC: cent7:1 (hpcadmin) -auth /home/hpcadmin/.Xauthority -geometry 1240x900 -depth 24 -rfbauth /home/hpcadmin/.vnc/passwd -x509cert /home/hpcadmin/.vnc/x509_cert.pem -x509key /home/hpcadmin/.vnc/x509_private.pem -rfbport 5901 -fp catalogue:/etc/X11/fontpath.d"
	connSnapshot := parseVNCConnectionDetails("5918", "hpcadmin", args, false)
	if connSnapshot.PID != "5918" {
		t.Fatalf("pid = %q, want 5918", connSnapshot.PID)
	}
	if connSnapshot.Username != "hpcadmin" {
		t.Fatalf("username = %q, want hpcadmin", connSnapshot.Username)
	}
	if connSnapshot.Display != 1 {
		t.Fatalf("display = %d, want 1", connSnapshot.Display)
	}
	if connSnapshot.Port != 5901 {
		t.Fatalf("port = %d, want 5901", connSnapshot.Port)
	}
	if connSnapshot.ConnectionName != "TurboVNC: cent7:1 (hpcadmin)" {
		t.Fatalf("connection name = %q", connSnapshot.ConnectionName)
	}
}

func TestParseVNCConnectionDetailsFallsBackToDisplayPort(t *testing.T) {
	t.Parallel()

	args := "/usr/bin/Xtigervnc :2 -rfbauth /home/hpcadmin/.vnc/passwd"
	connSnapshot := parseVNCConnectionDetails("6665", "hpcadmin", args, false)
	if connSnapshot.Display != 2 {
		t.Fatalf("display = %d, want 2", connSnapshot.Display)
	}
	if connSnapshot.Port != 5902 {
		t.Fatalf("port = %d, want 5902", connSnapshot.Port)
	}
	if connSnapshot.ConnectionName != "vnc-hpcadmin-2" {
		t.Fatalf("connection name = %q, want generated fallback", connSnapshot.ConnectionName)
	}
}

func TestParseVNCConnectionDetailsUsesManagedAuthDirectoryName(t *testing.T) {
	t.Parallel()

	args := "/opt/TurboVNC/bin/Xvnc :8 -rfbauth /home/alice/.cache/design-cloud-engine/connections/gui-123/passwd"
	connSnapshot := parseVNCConnectionDetails("7777", "alice", args, false)
	if connSnapshot.ConnectionName != "gui-123" {
		t.Fatalf("connection name = %q, want gui-123", connSnapshot.ConnectionName)
	}
	if connSnapshot.Port != 5908 {
		t.Fatalf("port = %d, want 5908", connSnapshot.Port)
	}
}

func TestParseVNCConnectionDetailsWithEnviron(t *testing.T) {
	// Not setting parallel because we mutate global readProcEnvironFn
	oldFn := readProcEnvironFn
	defer func() { readProcEnvironFn = oldFn }()

	readProcEnvironFn = func(pid string) map[string]string {
		if pid == "8888" {
			return map[string]string{
				"DCE_CONNECTION_ID":   "456",
				"DCE_CONNECTION_NAME": "env-name",
			}
		}
		return map[string]string{}
	}

	args := "/usr/bin/Xvnc :10"
	connSnapshot := parseVNCConnectionDetails("8888", "bob", args, false)

	// ConnectionID is matched server-side via runtime user/port/PID, probe snapshot remains 0
	if connSnapshot.ConnectionID != 0 {
		t.Fatalf("ConnectionID = %d, want 0", connSnapshot.ConnectionID)
	}
	if connSnapshot.ConnectionName != "env-name" {
		t.Fatalf("ConnectionName = %q, want env-name", connSnapshot.ConnectionName)
	}
}

func TestCollectRemoteConnections_FromProc(t *testing.T) {
	fakeProc := t.TempDir()
	oldProc := procFSRoot
	procFSRoot = fakeProc
	defer func() { procFSRoot = oldProc }()

	// 1. Create a non-matching process (bash)
	bashDir := filepath.Join(fakeProc, "100")
	if err := os.MkdirAll(bashDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bashDir, "cmdline"), []byte("/bin/bash\x00-i\x00"), 0644); err != nil {
		t.Fatalf("write cmdline failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bashDir, "status"), []byte("Name:\tbash\nUid:\t1000\t1000\t1000\t1000\n"), 0644); err != nil {
		t.Fatalf("write status failed: %v", err)
	}

	// 2. Create a matching VNC process
	vncDir := filepath.Join(fakeProc, "200")
	if err := os.MkdirAll(vncDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	vncCmdline := []byte("/opt/TurboVNC/bin/Xvnc\x00:5\x00-rfbport\x005905\x00-desktop\x00TurboVNC: host:5 (alice)\x00")
	if err := os.WriteFile(filepath.Join(vncDir, "cmdline"), vncCmdline, 0644); err != nil {
		t.Fatalf("write cmdline failed: %v", err)
	}
	// 3. Create a transient job VNC process (should be filtered out)
	jobVncDir := filepath.Join(fakeProc, "300")
	if err := os.MkdirAll(jobVncDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	jobVncCmdline := []byte("/opt/TurboVNC/bin/Xvnc\x00:6\x00-rfbport\x005906\x00-desktop\x00dce-job-9999\x00")
	if err := os.WriteFile(filepath.Join(jobVncDir, "cmdline"), jobVncCmdline, 0644); err != nil {
		t.Fatalf("write cmdline failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobVncDir, "status"), []byte("Name:\tXvnc\nUid:\t1001\t1001\t1001\t1001\n"), 0644); err != nil {
		t.Fatalf("write status failed: %v", err)
	}

	connections, err := CollectRemoteConnections(context.Background())
	if err != nil {
		t.Fatalf("CollectRemoteConnections failed: %v", err)
	}
	if len(connections) != 1 {
		t.Fatalf("got %d connections, want 1", len(connections))
	}
	s := connections[0]
	if s.PID != "200" {
		t.Fatalf("PID = %q, want 200", s.PID)
	}
	if s.Display != 5 {
		t.Fatalf("Display = %d, want 5", s.Display)
	}
	if s.Port != 5905 {
		t.Fatalf("Port = %d, want 5905", s.Port)
	}
	if s.ConnectionName != "TurboVNC: host:5 (alice)" {
		t.Fatalf("ConnectionName = %q, want TurboVNC: host:5 (alice)", s.ConnectionName)
	}
	if s.Protocol != "VNC" {
		t.Fatalf("Protocol = %q, want VNC", s.Protocol)
	}
}

func TestCheckRDPReadiness_Linux(t *testing.T) {
	t.Parallel()

	got := CollectRDPReadiness()
	if got.Determinate {
		t.Errorf("CollectRDPReadiness on Linux must stay indeterminate, got %+v", got)
	}
}
