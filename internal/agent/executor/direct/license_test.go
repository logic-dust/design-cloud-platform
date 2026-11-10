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
		_, err := exec.resolveLmutilPath("")
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
		_, err := exec.resolveLmutilPath("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "configured lmutil not found")
	})

	t.Run("valid settings path accepted", func(t *testing.T) {
		tmpDir := t.TempDir()
		fakeLmutil := filepath.Join(tmpDir, "lmutil")
		if runtime.GOOS == "windows" {
			fakeLmutil += ".exe"
		}
		require.NoError(t, os.WriteFile(fakeLmutil, []byte("#!/bin/sh\nexit 0\n"), 0755))

		exec := NewExecutor(Config{
			GetLmutilPath: func() string { return fakeLmutil },
		}, logger)
		path, err := exec.resolveLmutilPath("")
		require.NoError(t, err)
		assert.Equal(t, fakeLmutil, path)
	})

	t.Run("empty settings and override fails without sniffing", func(t *testing.T) {
		exec := NewExecutor(Config{
			GetLmutilPath: func() string { return "" },
		}, logger)
		_, err := exec.resolveLmutilPath("")
		require.ErrorIs(t, err, errLmutilPathNotConfigured)
	})

	t.Run("service override wins over settings", func(t *testing.T) {
		tmpDir := t.TempDir()
		settingsPath := filepath.Join(tmpDir, "settings-lmutil")
		overridePath := filepath.Join(tmpDir, "service-lmutil")
		if runtime.GOOS == "windows" {
			settingsPath += ".exe"
			overridePath += ".exe"
		}
		require.NoError(t, os.WriteFile(settingsPath, []byte("#!/bin/sh\nexit 0\n"), 0755))
		require.NoError(t, os.WriteFile(overridePath, []byte("#!/bin/sh\nexit 0\n"), 0755))

		exec := NewExecutor(Config{
			GetLmutilPath: func() string { return settingsPath },
		}, logger)
		path, err := exec.resolveLmutilPath(overridePath)
		require.NoError(t, err)
		assert.Equal(t, overridePath, path)
	})
}

func TestExecuteLicenseCommand(t *testing.T) {
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("skipping license test as root")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tmpDir := t.TempDir()
	fakeLmutil := filepath.Join(tmpDir, "fake_lmutil")
	overrideLmutil := filepath.Join(tmpDir, "override_lmutil")
	if runtime.GOOS == "windows" {
		fakeLmutil += ".bat"
		overrideLmutil += ".bat"
		script := "@echo off\necho args: %*\n"
		require.NoError(t, os.WriteFile(fakeLmutil, []byte(script), 0755))
		require.NoError(t, os.WriteFile(overrideLmutil, []byte(script), 0755))
	} else {
		script := "#!/bin/sh\necho \"args: $@\"\n"
		require.NoError(t, os.WriteFile(fakeLmutil, []byte(script), 0755))
		require.NoError(t, os.WriteFile(overrideLmutil, []byte(script), 0755))
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

	t.Run("status uses per-service lmutilPath override", func(t *testing.T) {
		req := execution.NewExecuteRequest(execution.ActionLicenseStatus, "", execution.WithPayload(payload.LicenseStatus{
			LicensePath: "27000@localhost",
			LmutilPath:  overrideLmutil,
		}))

		resp, err := exec.Execute(context.Background(), req)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Contains(t, resp.Stdout, "args: lmstat -c 27000@localhost")
	})
}
