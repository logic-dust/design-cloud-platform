//go:build dev

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/platform/config"
)

// setupServerDevEnv initializes the development environment prerequisites for the Server.
// This runs before cluster control node startup to ensure dev settings and mock data.
func setupServerDevEnv(cfg *config.Config, store *db.Store, log *slog.Logger) error {
	devLog := log.With("component", "devmode")
	devLog.Debug("🔧 Setting up Server development environment...")

	// Override timeouts for dev mode to facilitate debugging
	cfg.Portal.ReadHeaderTimeout = 0
	cfg.Portal.ReadTimeout = 0
	cfg.Portal.IdleTimeout = 0
	devLog.Debug("🔧 Overrode Portal timeouts for dev mode (set to 0 for infinite)")

	repos := db.NewRepos(store)

	if err := ensureDevData(context.Background(), repos, devLog, cfg); err != nil {
		return fmt.Errorf("failed to ensure dev data: %w", err)
	}
	return nil
}

func ensureDevData(ctx context.Context, repos *db.Repos, log *slog.Logger, cfg *config.Config) error {
	log.Debug("🔧 Ensuring dev data...")

	if err := ensureDevAgentSettings(ctx, repos, log); err != nil {
		return err
	}
	if err := ensureDevStorageSettings(ctx, repos, log, cfg); err != nil {
		return err
	}
	return nil
}

func ensureDevStorageSettings(ctx context.Context, repos *db.Repos, log *slog.Logger, cfg *config.Config) error {
	var storageSettings schema.StorageSettings
	if err := repos.Setting.Load(ctx, string(schema.DomainStorage), &storageSettings); err != nil {
		return fmt.Errorf("failed to load dev storage settings: %w", err)
	}

	// Always overwrite with a mock shared storage for dev mode
	storageSettings.Storages = []schema.StorageConfig{
		{
			ID:          "share",
			Enabled:     true,
			Name:        "Dev Mock NFS",
			Type:        "nfs",
			MountPoint:  "/share",
			ProbeNodes:  cfg.Runtime.Servers,
			Description: "Auto-generated mock storage for dev mode testing",
		},
	}
	if err := repos.Setting.Save(ctx, string(schema.DomainStorage), storageSettings); err != nil {
		return fmt.Errorf("failed to save dev storage settings: %w", err)
	}
	log.Info("🔧 Ensured default mock storage")

	return nil
}

func ensureDevAgentSettings(ctx context.Context, repos *db.Repos, log *slog.Logger) error {
	var agentSettings schema.AgentSettings
	if err := repos.Setting.Load(ctx, string(schema.DomainAgent), &agentSettings); err != nil {
		return fmt.Errorf("failed to load dev agent settings: %w", err)
	}

	// 仅当设置了 DCE_DEV_MOCK_EXEC_BIN=true 时，用 tmp 下的 mock PrivilegedExec 覆盖 settings
	if os.Getenv("DCE_DEV_MOCK_EXEC_BIN") == "true" {
		mockExecBinPath := filepath.Join(os.TempDir(), "dce-mock-dcexec")
		if agentSettings.ExecBinPath != mockExecBinPath {
			agentSettings.ExecBinPath = mockExecBinPath
			if err := repos.Setting.Save(ctx, string(schema.DomainAgent), agentSettings); err != nil {
				return fmt.Errorf("failed to save dev agent settings: %w", err)
			}
			log.Info("🔧 Dev agent settings override applied", "execBinPath", mockExecBinPath)
		}
	}
	return nil
}
