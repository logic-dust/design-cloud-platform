package direct

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveLmutilPath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("relative path rejected", func(t *testing.T) {
		exec := NewExecutor(Config{
			GetLmutilPath: func() string { return "bin/lmutil" },
		}, logger)
		_, err := exec.resolveLmutilPath()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be an absolute path")
	})

	t.Run("non-existent configured path rejected", func(t *testing.T) {
		exec := NewExecutor(Config{
			GetLmutilPath: func() string {
				if runtime.GOOS == "windows" {
					return `C:\nonexistent\lmutil.exe`
				}
				return "/nonexistent/lmutil"
			},
		}, logger)
		_, err := exec.resolveLmutilPath()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "configured lmutil not found")
	})

	t.Run("valid configured file accepted", func(t *testing.T) {
		tmpDir := t.TempDir()
		fakeLmutil := filepath.Join(tmpDir, "lmutil")
		if runtime.GOOS == "windows" {
			fakeLmutil += ".exe"
		}
		require.NoError(t, os.WriteFile(fakeLmutil, []byte("#!/bin/sh\nexit 0\n"), 0755))

		exec := NewExecutor(Config{
			GetLmutilPath: func() string { return fakeLmutil },
		}, logger)
		path, err := exec.resolveLmutilPath()
		require.NoError(t, err)
		assert.Equal(t, fakeLmutil, path)
	})

	t.Run("empty path falls back to candidates or fails without PATH search", func(t *testing.T) {
		exec := NewExecutor(Config{
			GetLmutilPath: func() string { return "" },
		}, logger)
		// Unless one of the standard candidates actually exists on the host, this should fail with errLmutilNotFound
		path, err := exec.resolveLmutilPath()
		if err != nil {
			assert.ErrorIs(t, err, errLmutilNotFound)
		} else {
			assert.NotEmpty(t, path)
			assert.True(t, filepath.IsAbs(path))
		}
	})
}

func TestExecuteLicenseCommand(t *testing.T) {
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("skipping license test as root")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tmpDir := t.TempDir()
	fakeLmutil := filepath.Join(tmpDir, "fake_lmutil")
	if runtime.GOOS == "windows" {
		fakeLmutil += ".bat"
		script := "@echo off\necho args: %*\n"
		require.NoError(t, os.WriteFile(fakeLmutil, []byte(script), 0755))
	} else {
		script := "#!/bin/sh\necho \"args: $@\"\n"
		require.NoError(t, os.WriteFile(fakeLmutil, []byte(script), 0755))
	}

	exec := NewExecutor(Config{
		GetLmutilPath: func() string { return fakeLmutil },
	}, logger)

	t.Run("status action runs lmstat -c", func(t *testing.T) {
		req := execution.NewExecuteRequest(execution.ActionLicenseStatus, "", execution.WithPayload(payload.LicenseStatus{
			LicensePath: "27000@localhost",
		}))

		resp, err := exec.Execute(context.Background(), req)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Contains(t, resp.Stdout, "args: lmstat -c 27000@localhost")
	})

	t.Run("features action runs lmstat -t 5 -i -a -c", func(t *testing.T) {
		req := execution.NewExecuteRequest(execution.ActionLicenseFeatures, "", execution.WithPayload(payload.LicenseFeatures{
			LicensePath: "27000@localhost",
		}))

		resp, err := exec.Execute(context.Background(), req)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Contains(t, resp.Stdout, "args: lmstat -t 5 -i -a -c 27000@localhost")
	})
}
