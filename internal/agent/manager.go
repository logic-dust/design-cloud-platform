package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"design-cloud-engine/internal/agent/executor/direct"
	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/common/fs"
	"design-cloud-engine/internal/config"
)

// Manager manages agent and its dependencies.
type Manager struct {
	agent    *Agent
	logger   *slog.Logger
	auditSvc *audit.Service
}

// NewManager creates and initializes the agent manager.
func NewManager(logger *slog.Logger, cfg *config.Config) (*Manager, error) {
	agentLog := logger.With("component", "agent")

	if err := fs.MkdirPrivate(fs.RuntimeDir()); err != nil {
		return nil, fmt.Errorf("failed to create runtime directory: %w", err)
	}

	if err := initLauncher(logger); err != nil {
		return nil, err
	}

	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  cfg.AuditLogPath(),
		SecurityLogPath: cfg.SecurityLogPath(),
		NodeID:          cfg.Runtime.NodeID,
		Version:         config.VERSION,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize audit service: %w", err)
	}

	settingsMgr := NewSettingsManager()

	onAudit := func(ctx context.Context, actionName string, result string, targetUser string, traceID string, level slog.Level, details map[string]any, duration time.Duration) {
		auditSvc.LogExecution(ctx, actionName, result, targetUser, traceID, level, details, duration)
	}

	directExec := direct.NewExecutor(direct.Config{
		GetLmutilPath: func() string {
			if s := settingsMgr.Get(); s != nil {
				return s.LmutilPath
			}
			return ""
		},
	}, agentLog)
	executorService := createExecutorService(settingsMgr, directExec, agentLog, onAudit)

	agentLog.Debug("Creating cluster agent", "node_id", cfg.Runtime.NodeID, "servers", cfg.Runtime.Servers)

	agentCfg := Config{
		NodeID:            cfg.Runtime.NodeID,
		HeartbeatInterval: time.Duration(cfg.Cluster.HeartbeatInterval) * time.Second,

		ClusterPort:     cfg.Cluster.Port,
		ConnectTimeout:  time.Duration(cfg.Cluster.ConnectTimeout) * time.Second,
		ServerAddresses: buildServerAddresses(cfg),
		TLS: TLSClientConfig{
			ServerName: cfg.Cluster.TLSServerName,
			CertPath:   cfg.ClusterNodeCertPath(cfg.Runtime.NodeID),
			KeyPath:    cfg.ClusterNodeKeyPath(cfg.Runtime.NodeID),
			CAPath:     cfg.ClusterCACertPath(),
		},
	}
	clusterAgent := NewAgent(executorService, agentCfg, agentLog, settingsMgr)

	agentLog.Debug("Agent manager initialization complete")
	return &Manager{
		agent:    clusterAgent,
		logger:   agentLog,
		auditSvc: auditSvc,
	}, nil
}

// Start starts the agent if configured.
func (m *Manager) Start() error {
	if m.agent == nil {
		m.logger.Info("No agent to start (no server configuration)")
		return nil
	}
	m.agent.Start()
	m.logger.Info("Agent started")
	return nil
}

// Stop gracefully stops the agent.
func (m *Manager) Stop() {
	if m.agent == nil {
		return
	}
	m.logger.Info("Stopping agent...")
	m.agent.Stop()
	if m.auditSvc != nil {
		if err := m.auditSvc.Close(); err != nil {
			m.logger.Warn("Failed to close audit service", "error", err)
		}
	}
	m.logger.Info("Agent stopped")
}

func buildServerAddresses(cfg *config.Config) []string {
	serverAddresses := make([]string, 0, len(cfg.Runtime.Servers))
	for _, name := range cfg.Runtime.Servers {
		serverAddresses = append(serverAddresses, net.JoinHostPort(name, strconv.Itoa(cfg.Cluster.Port)))
	}
	return serverAddresses
}
