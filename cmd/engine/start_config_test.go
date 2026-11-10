package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/platform/config"
)

func TestApplyLogDirOverride(t *testing.T) {
	cfg := &config.Config{}
	cfg.Log.Dir = "/from/config"

	// Empty value keeps the config untouched.
	if err := applyLogDirOverride(cfg, ""); err != nil {
		t.Fatalf("applyLogDirOverride(empty): %v", err)
	}
	if cfg.Log.Dir != "/from/config" {
		t.Fatalf("empty override must not modify Log.Dir, got %q", cfg.Log.Dir)
	}

	// Relative paths are anchored to the CWD, never to the config file dir.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := applyLogDirOverride(cfg, filepath.Join("var", "log", "dce")); err != nil {
		t.Fatalf("applyLogDirOverride(relative): %v", err)
	}
	if want := filepath.Join(wd, "var", "log", "dce"); cfg.Log.Dir != want {
		t.Fatalf("Log.Dir = %q, want %q", cfg.Log.Dir, want)
	}

	// Absolute paths are used as-is.
	if err := applyLogDirOverride(cfg, "/local/disk/logs"); err != nil {
		t.Fatalf("applyLogDirOverride(absolute): %v", err)
	}
	if cfg.Log.Dir != "/local/disk/logs" {
		t.Fatalf("Log.Dir = %q", cfg.Log.Dir)
	}
}

func TestApplyLogLevelOverride(t *testing.T) {
	cfg := &config.Config{}
	cfg.Log.AppLevel = slog.LevelInfo

	if err := applyLogLevelOverride(cfg, ""); err != nil {
		t.Fatalf("empty override: %v", err)
	}
	if cfg.Log.AppLevel != slog.LevelInfo {
		t.Fatal("empty override must not modify AppLevel")
	}

	for level, want := range map[string]slog.Level{
		"DEBUG": slog.LevelDebug,
		"debug": slog.LevelDebug,
		"INFO":  slog.LevelInfo,
		"WARN":  slog.LevelWarn,
		"ERROR": slog.LevelError,
	} {
		if err := applyLogLevelOverride(cfg, level); err != nil {
			t.Fatalf("applyLogLevelOverride(%q): %v", level, err)
		}
		if cfg.Log.AppLevel != want {
			t.Fatalf("level %q → %v, want %v", level, cfg.Log.AppLevel, want)
		}
	}

	if err := applyLogLevelOverride(cfg, "verbose"); err == nil {
		t.Fatal("invalid level must fail fast")
	}
}
