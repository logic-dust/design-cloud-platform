package main

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"

	"design-cloud-engine/cmd/engine/command"
	"design-cloud-engine/cmd/engine/console"
	"design-cloud-engine/internal/agent"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/platform/logger"
)

// runEngine orchestrates the runtime initialization, startup, and graceful shutdown
// of the engine's core services (Agent component unconditionally on all nodes; Server component additionally when ServerEnabled).
func runEngine(ctx context.Context, flags *command.Flags) error {
	ctx, cancel := context.WithCancel(ctx)
	var cleanups []func()
	defer func() {
		// Stop every context-bound worker before releasing the resources it uses.
		// Keep cancellation and cleanup in one defer so LIFO ordering cannot stop
		// Server dependencies before their background loops have been told to exit.
		cancel()
		for _, cleanup := range slices.Backward(cleanups) {
			cleanup()
		}
	}()

	// --- 加载配置（配置文件模式或 configless 内存装配；模式冲突/缺省会 Fail-Fast）
	cfg, mode, err := loadConfigForStart(flags)
	if err != nil {
		return err
	}

	// --- 运行期覆盖项（两种模式都生效；必须在配置解析完成之后应用）
	if err := applyLogDirOverride(cfg, flags.LogDir); err != nil {
		return err
	}
	if err := applyLogLevelOverride(cfg, flags.LogLevel); err != nil {
		return err
	}

	// --- Configless 前置检查：证书缺失时引导 node register
	// （放在覆盖项校验之后：非法 --log-level/--log-dir 应先于证书引导暴露）
	if mode == command.StartModeConfigless {
		if err := ensureAgentCertPresent(cfg); err != nil {
			return err
		}
	}

	// --- 初始化 app 日志记录器（stdout；journald / 容器运行时接管落盘与轮转）
	appLogger, _, err := logger.NewLogger(logger.LogConfig{
		Host:  cfg.Runtime.NodeID,
		Level: cfg.Log.AppLevel,
	})
	if err != nil {
		return fmt.Errorf("app logger init failed: %w", err)
	}

	// --- 错误通道，用于接收服务启动或运行过程中发生的错误，触发cleanup
	errCh := make(chan error, 2)
	// --- 显示启动信息
	showStartupInfo(cfg)

	// --- 启动（http & GRPC）server：portal + control
	if cfg.Runtime.ServerEnabled {
		appLogger.With("component", "server").Info("Starting SERVER", "node_id", cfg.Runtime.NodeID, "grpc_port", cfg.Cluster.Port, "portal_port", cfg.Portal.Port, "tls", cfg.Portal.TLS)
		cleanup, err := startServer(ctx, cfg, appLogger, errCh)
		if err != nil {
			return fmt.Errorf("server start failed: %w", err)
		}
		if cleanup != nil {
			cleanups = append(cleanups, cleanup) // LIFO：先 append server（index 0），停服时倒序执行 → agent(后注册)先停、server(先注册)后停
		}
	}

	// --- 启动（GRPC）client：agent（全节点运行）
	appLogger.With("component", "agent").Info("Starting AGENT", "node_id", cfg.Runtime.NodeID, "servers", cfg.Runtime.Servers, "heartbeat_interval", cfg.Cluster.HeartbeatInterval)
	cleanup, err := startAgent(ctx, cfg, appLogger)
	if err != nil {
		return fmt.Errorf("agent start failed: %w", err)
	}
	if cleanup != nil {
		cleanups = append(cleanups, cleanup)
	}

	// --- 等待中断信号或服务错误
	select {
	case <-ctx.Done():
		appLogger.Info("Shutdown signal received, initiating graceful shutdown...")
	case err := <-errCh:
		appLogger.Error("Critical service failure, shutting down...", "error", err)
		cancel()
		return err
	}
	return nil
}

func startAgent(ctx context.Context, cfg *config.Config, logger *slog.Logger) (func(), error) {
	agentLog := logger.With("component", "agent")

	if len(cfg.Runtime.Servers) == 0 {
		return nil, fmt.Errorf("no cluster servers resolved from cluster.servers")
	}

	agentMgr, err := agent.NewManager(ctx, logger, cfg)
	if err != nil {
		return nil, err
	}

	// --- 启动 agent 各组件
	if err := agentMgr.Start(); err != nil {
		return nil, err
	}

	agentLog.Info("Agent services started successfully")

	return func() {
		// --- 停止 agent 各组件
		agentMgr.Stop()
	}, nil
}

// --- 启动信息 ---

func showStartupInfo(cfg *config.Config) {
	console.PrintlnStdout("+++++++++++++++++++++++++")
	console.PrintlnStdout(config.AppName)
	console.PrintlnStdout("+++++++++++++++++++++++++")

	console.PrintlnStdout("Base Dir:      ", cfg.Runtime.BaseDir)

	var roles []string
	if cfg.Runtime.ServerEnabled {
		roles = append(roles, "server")
		console.PrintlnStdout("Listen Host:   ", cfg.Portal.Host)
		console.PrintlnStdout("Listen Port:   ", cfg.Portal.Port)

		console.PrintlnStdout("Database Type: ", cfg.Database.Type)
		if cfg.Database.Type == "sqlite" {
			appDbAbsPath, _ := filepath.Abs(cfg.Database.Path)
			console.PrintlnStdout("Database File: ", appDbAbsPath)
		}
	}

	roles = append(roles, "agent")

	console.PrintlnStdout("Node Roles:    ", roles)
	console.PrintlnStdout("Log Level:     ", cfg.Log.AppLevel.String())
	console.PrintlnStdout("Access Level:  ", cfg.Log.AccessLevel.String())
	console.PrintlnStdout("Audit Log Dir: ", cmp.Or(cfg.Log.Dir, "(unset)"))
}
