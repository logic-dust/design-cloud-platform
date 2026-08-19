package command

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	nodeService "design-cloud-engine/internal/server/service/node"
)

func setupTestEnvironment(t *testing.T) (*Flags, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	dataDir := filepath.Join(dir, "data")
	logDir := filepath.Join(dir, "logs")
	certDir := filepath.Join(dataDir, "certs", "cluster")

	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: dbPath,
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Migrate(); err != nil {
		_ = store.Close()
		t.Fatalf("migrate: %v", err)
	}
	_ = store.Close()

	caCertFile := filepath.Join(certDir, "ca.crt")
	caKeyFile := filepath.Join(certDir, "ca", "ca.key")
	if _, err := nodeService.InitializeCA(caCertFile, caKeyFile, true); err != nil {
		t.Fatalf("init CA: %v", err)
	}

	configContent := fmt.Sprintf(`
data_dir = %q

[database]
type = "sqlite"
path = %q

[cluster]
port = 52255
servers = ["test-srv"]

[log]
dir = %q
`, dataDir, dbPath, logDir)

	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	flags := &Flags{
		ConfigFile: configPath,
		Port:       52255,
	}
	return flags, dir
}

func TestIssueCertCommand_Validation(t *testing.T) {
	flags, _ := setupTestEnvironment(t)

	t.Run("missing hostname fails", func(t *testing.T) {
		err := runIssueCertCommand(flags, []string{})
		if err == nil || !strings.Contains(err.Error(), "--hostname is required") {
			t.Fatalf("expected --hostname error, got: %v", err)
		}
	})

	t.Run("mutually exclusive out-dir and archive fails", func(t *testing.T) {
		err := runIssueCertCommand(flags, []string{
			"--hostname", "agent-x",
			"--out-dir", "/tmp/out",
			"--archive", "/tmp/out.tar.gz",
		})
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("expected mutually exclusive error, got: %v", err)
		}
	})
}

func TestIssueCertCommand_OutDir(t *testing.T) {
	flags, dir := setupTestEnvironment(t)
	outDir := filepath.Join(dir, "export_agent01")

	err := runIssueCertCommand(flags, []string{
		"--hostname", "cli-agent-01",
		"--out-dir", outDir,
	})
	if err != nil {
		t.Fatalf("runIssueCertCommand failed: %v", err)
	}

	caPath := filepath.Join(outDir, "ca.crt")
	certPath := filepath.Join(outDir, "cli-agent-01", "cli-agent-01.crt")
	keyPath := filepath.Join(outDir, "cli-agent-01", "cli-agent-01.key")

	if _, err := os.Stat(caPath); err != nil {
		t.Errorf("expected CA file at %s: %v", caPath, err)
	}
	if _, err := os.Stat(certPath); err != nil {
		t.Errorf("expected cert file at %s: %v", certPath, err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("expected key file at %s: %v", keyPath, err)
	}
}

func TestIssueCertCommand_Archive(t *testing.T) {
	flags, dir := setupTestEnvironment(t)
	archivePath := filepath.Join(dir, "agent02.tar.gz")

	err := runIssueCertCommand(flags, []string{
		"--hostname", "cli-agent-02",
		"--archive", archivePath,
	})
	if err != nil {
		t.Fatalf("runIssueCertCommand failed: %v", err)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer func() {
		_ = f.Close()
	}()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("new gzip reader: %v", err)
	}
	defer func() {
		_ = gzr.Close()
	}()

	tr := tar.NewReader(gzr)
	found := make(map[string]int64)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read tar header: %v", err)
		}
		found[hdr.Name] = hdr.Mode
	}

	if _, ok := found["ca.crt"]; !ok {
		t.Errorf("ca.crt missing in archive")
	}
	if _, ok := found["cli-agent-02/cli-agent-02.crt"]; !ok {
		t.Errorf("cert missing in archive")
	}
	if mode, ok := found["cli-agent-02/cli-agent-02.key"]; !ok {
		t.Errorf("key missing in archive")
	} else if mode != 0600 {
		t.Errorf("expected key mode 0600, got 0%o", mode)
	}
}
