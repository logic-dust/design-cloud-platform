//go:build linux

package node

import (
	"os"
	"path/filepath"
	"testing"

	fsutil "design-cloud-engine/internal/common/fs"
)

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}

// 权限矩阵七行逐项断言（§3.2 唯一事实源），并验证 MkdirShared 能修复 0700 的共享目录。
func TestClusterPathPermissions(t *testing.T) {
	dir := t.TempDir()
	clusterDir := filepath.Join(dir, "certs", "cluster")
	caDir := filepath.Join(clusterDir, "ca")
	caCert := filepath.Join(clusterDir, "ca.crt")
	caKey := filepath.Join(caDir, "ca.key")
	nodeDir := filepath.Join(clusterDir, "cn01")
	nodeCert := filepath.Join(nodeDir, "cn01.crt")
	nodeKey := filepath.Join(nodeDir, "cn01.key")

	if _, err := InitializeCA(caCert, caKey, false); err != nil {
		t.Fatalf("InitializeCA: %v", err)
	}
	if _, err := IssueNodeCert(CertOptions{
		CommonName: "cn01",
		Hosts:      []string{"cn01"},
		CACertFile: caCert,
		CAKeyFile:  caKey,
		CertFile:   nodeCert,
		KeyFile:    nodeKey,
	}); err != nil {
		t.Fatalf("IssueNodeCert: %v", err)
	}

	assertPerm(t, clusterDir, 0o755)
	assertPerm(t, caCert, 0o644)
	assertPerm(t, caDir, 0o700)
	assertPerm(t, caKey, 0o600)
	assertPerm(t, nodeDir, 0o700)
	assertPerm(t, nodeCert, 0o644)
	assertPerm(t, nodeKey, 0o600)

	// Repair path: a historically wrong 0700 on certs/cluster must be fixed by MkdirShared.
	if err := os.Chmod(clusterDir, 0o700); err != nil {
		t.Fatalf("chmod setup: %v", err)
	}
	if err := fsutil.MkdirShared(clusterDir); err != nil {
		t.Fatalf("MkdirShared repair: %v", err)
	}
	assertPerm(t, clusterDir, 0o755)
}
