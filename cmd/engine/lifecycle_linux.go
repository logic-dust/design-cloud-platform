package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"design-cloud-engine/cmd/engine/command"
	fsutil "design-cloud-engine/internal/common/fs"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/server"
	"design-cloud-engine/internal/server/service/settings"
	usersvc "design-cloud-engine/internal/server/service/user"
)

// --- 1. Pre-flight Environment Validation ---

// checkPlatformPreconditions performs process-level pre-flight environment checks for Linux.
// It executes as the very first line of main() (Fail-Fast): running as root is strictly prohibited
// across all commands to prevent permission corruptions on config files, DB, or TLS materials.
func checkPlatformPreconditions() error {
	runUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("resolve current user: %w", err)
	}
	if runUser.Uid == "0" {
		return fmt.Errorf("running as root is not allowed for security reasons")
	}
	return nil
}

// --- 2. Runtime Lifecycle Supervision (Daemon) ---

// runWithLifecycle provides the process lifecycle driver for Linux.
// It acquires an exclusive process lockfile to prevent multi-instance runs,
// intercepts POSIX SIGINT/SIGTERM for graceful context cancellation, and cleans up the lock on exit.
func runWithLifecycle(_ *command.Flags, runFn func(ctx context.Context) error) error {
	unlock, err := acquireProcessLock()
	if err != nil {
		return err
	}
	defer unlock()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return runFn(ctx)
}

// --- 3. Process Singleton Lock Helpers ---

func acquireProcessLock() (func(), error) {
	path := fsutil.PIDFilePath()
	dir := filepath.Dir(path)
	if err := fsutil.MkdirPrivate(dir); err != nil {
		return nil, err
	}

	pid := os.Getpid()
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open PID file %s: %w", path, err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		content, _ := os.ReadFile(path)
		return nil, fmt.Errorf("could not lock PID file %s: process %s might be running: %w", path, string(bytes.TrimSpace(content)), err)
	}

	content := strconv.Itoa(pid)
	if st, err := command.ProcessStartTime(pid); err == nil && st > 0 {
		content += " " + strconv.FormatUint(st, 10)
	}

	if err := f.Truncate(0); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to truncate %s: %w", path, err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to seek %s: %w", path, err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to write PID to %s: %w", path, err)
	}
	_ = f.Sync()

	// Keep the sentinel lockfile in place on exit; closing the file descriptor automatically
	// releases the kernel flock without introducing inode recreation TOCTOU races.
	return func() {
		_ = f.Close()
	}, nil
}

// --- 4. Linux Server Component Lifecycle ---

func startServer(ctx context.Context, cfg *config.Config, logger *slog.Logger, errCh chan<- error) (func(), error) {
	svrLog := logger.With("component", "server")

	// --- 连接数据库
	store, err := db.NewStore(cfg.Database, logger)
	if err != nil {
		return nil, fmt.Errorf("db connect failed: %w", err)
	}
	closeStore := func() { _ = store.Close() }
	started := false
	defer func() {
		if !started {
			closeStore()
		}
	}()

	// --- 统一执行数据库迁移与基础初始化
	if err := store.Migrate(); err != nil {
		return nil, fmt.Errorf("db migration failed: %w", err)
	}

	// --- 引导初始化（确保默认数据和管理用户存在）
	if err := settings.EnsureDefaults(store); err != nil {
		return nil, fmt.Errorf("ensure settings: %w", err)
	}
	if err := usersvc.EnsureAdminUsers(store, cfg.Admin.EnableThreeRoles); err != nil {
		return nil, fmt.Errorf("ensure admin users: %w", err)
	}

	// --- 准备 server 运行环境（开发模式下自动注入 mock 与超时覆盖，生产模式下为空存根）
	if err := setupServerDevEnv(cfg, store, logger); err != nil {
		return nil, fmt.Errorf("failed to setup server dev environment: %w", err)
	}

	// --- 确保 CA 与节点证书存在（构造前 IO，避免 NewServer 掺杂副作用）
	if err := server.PrepareCrypto(logger, cfg); err != nil {
		return nil, fmt.Errorf("prepare crypto: %w", err)
	}

	// --- 创建 server manager
	serverMgr, err := server.NewServer(ctx, logger, cfg, store)
	if err != nil {
		return nil, fmt.Errorf("failed to create server manager: %w", err)
	}

	// --- 启动 server 各组件
	if err := serverMgr.Start(strconv.Itoa(cfg.Cluster.Port), errCh); err != nil {
		return nil, err
	}

	svrLog.Info("All server services started successfully")
	started = true

	return func() {
		// --- 停止 server 各组件并释放数据库连接
		defer closeStore()
		serverMgr.Stop()
		svrLog.Info("Server services stopped")
	}, nil
}
