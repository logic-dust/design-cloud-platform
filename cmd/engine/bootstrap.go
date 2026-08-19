package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"design-cloud-engine/cmd/engine/command"
	"design-cloud-engine/internal/config"
)

// loadConfigForStart resolves the startup mode and produces the effective config:
//
//   - join / configless mode → pure in-memory assembly (no config.toml);
//   - config-file mode → config.Load only (join flags already rejected by ResolveStartMode).
//
// Certificate presence for configless agents is checked by the caller after the
// runtime overrides, so invalid --log-level/--log-dir fail before cert guidance.
func loadConfigForStart(flags *command.Flags) (*config.Config, command.StartMode, error) {
	mode, err := command.ResolveStartMode(flags)
	if err != nil {
		return nil, mode, err
	}

	if mode == command.StartModeConfigless {
		cfg, err := config.NewMinimalAgentConfig(flags.Servers, flags.Port, config.CertInput{
			DataDir: flags.DataDir,
		}, flags.LogDir)
		if err != nil {
			return nil, mode, fmt.Errorf("bootstrap configless agent: %w", err)
		}
		return cfg, mode, nil
	}

	cfg, err := config.Load(flags.ConfigFile)
	if err != nil {
		return nil, mode, fmt.Errorf("config load failed from %s: %w", flags.ConfigFile, err)
	}
	return cfg, mode, nil
}

// ensureAgentCertPresent fails fast when the configless agent has not been
// registered yet, pointing the operator at node register.
func ensureAgentCertPresent(cfg *config.Config) error {
	for _, p := range []string{cfg.Runtime.CertPath, cfg.Runtime.KeyPath, cfg.Runtime.CAPath} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf(
				"certificate material not found at %s;\n"+
					"register this node first (run as the agent service account):\n"+
					"  %s node register --portal-url <https://url> --token <token> [--insecure-http] [--data-dir <dir>]",
				p, config.AppName)
		}
	}
	return nil
}

// applyLogDirOverride applies --log-dir after config resolution (both modes):
// relative paths are anchored to the current working directory — NOT the config
// file directory, which resolve() already used for relative config values.
func applyLogDirOverride(cfg *config.Config, logDir string) error {
	if logDir == "" {
		return nil
	}
	abs, err := filepath.Abs(logDir)
	if err != nil {
		return fmt.Errorf("resolve --log-dir %q: %w", logDir, err)
	}
	cfg.Log.Dir = abs
	return nil
}

// applyLogLevelOverride applies --log-level after config loading; invalid values
// fail fast with the accepted enumeration.
func applyLogLevelOverride(cfg *config.Config, level string) error {
	if level == "" {
		return nil
	}
	parsed, err := parseLogLevel(level)
	if err != nil {
		return err
	}
	cfg.Log.AppLevel = parsed
	return nil
}

func parseLogLevel(level string) (slog.Level, error) {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return slog.LevelDebug, nil
	case "INFO":
		return slog.LevelInfo, nil
	case "WARN":
		return slog.LevelWarn, nil
	case "ERROR":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid --log-level %q: must be one of DEBUG/INFO/WARN/ERROR", level)
	}
}
