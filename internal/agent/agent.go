// Package agent implements the Agent-to-Server gRPC client for cluster communication.
// The Agent represents a compute node that connects to the Server to receive and execute commands.
//
// Connection Strategy:
//
// The agent uses a Strict Ordered Failover strategy:
// - Attempt to connect to each cluster server in Cluster.Servers list order
// - If connection fails, immediately try the next server in order
// - If all servers fail, wait ReconnectDelay seconds and retry from the first server
// - Always restart from the first server after any successful connection ends
//

package agent

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"design-cloud-engine/internal/agent/executor"
	"design-cloud-engine/internal/contract"
	pb "design-cloud-engine/internal/contract/proto"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// ErrServerShutdown is returned when the server requests the agent to disconnect.
var ErrServerShutdown = errors.New("server requested shutdown")

// errRegistrationRejected marks an explicit refusal of this node by the control plane
// (unknown/revoked certificate, fingerprint mismatch, bad token). It is deliberately a
// sentinel so the reconnect loop can tell it apart from transport errors: only an explicit
// refusal justifies rolling back to the previous certificate.
var errRegistrationRejected = errors.New("registration rejected")

type Config struct {
	NodeID            string
	HeartbeatInterval time.Duration

	ClusterPort     int
	ConnectTimeout  time.Duration
	ServerAddresses []string
	TLS             TLSClientConfig
}

type TLSClientConfig struct {
	ServerName string
	CertPath   string
	KeyPath    string
	CAPath     string
}

// Agent manages the connection to the Cluster gRPC server
type Agent struct {
	activeTasks     atomic.Int64
	executorService *executor.Service
	logger          *slog.Logger
	nodeIdentity    string
	cfg             Config
	settingsMgr     *SettingsManager

	stopOnce  sync.Once
	wg        sync.WaitGroup
	executeWg sync.WaitGroup // tracks in-flight handleCommandRequest goroutines

	currentConn   *grpc.ClientConn
	currentStream pb.ControlService_SyncClient
	streamMutex   sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc

	// sendMutex protects concurrent writes to the stream (gRPC requirement).
	sendMutex sync.Mutex

	tasks      map[string]context.CancelFunc
	tasksMutex sync.Mutex

	pendingResults   map[string]pendingCommandResult
	pendingResultsMu sync.Mutex

	// Certificate renewal state (see renew.go): pending key/CSR during the
	// CSR round-trip, plus the post-renewal rollback counter.
	renewMu       sync.Mutex
	pendingRenew  *pendingRenewal
	renewActive   bool
	renewFailures int

	// authRejections counts consecutive explicit authentication rejections from the
	// control plane (server refuses this node's certificate). lastAuthRollback
	// throttles the rollback so a node cannot loop when .prev is rejected too.
	authRejections   int
	lastAuthRollback time.Time

	// telemetry holds the PeriodicRunner started by startTelemetry; set before
	// the command stream loop begins. Allows command handlers to kick periodic
	// tasks out-of-cycle via TriggerNow.
	telemetry *PeriodicRunner
}

// NewAgent creates a new Agent
func NewAgent(parent context.Context, executorService *executor.Service, cfg Config, logger *slog.Logger, settingsMgr *SettingsManager) *Agent {
	ctx, cancel := context.WithCancel(parent)
	return &Agent{
		executorService: executorService,
		logger:          logger,
		nodeIdentity:    cfg.NodeID,
		cfg:             cfg,
		settingsMgr:     settingsMgr,
		tasks:           make(map[string]context.CancelFunc),
		pendingResults:  make(map[string]pendingCommandResult),
		ctx:             ctx,
		cancel:          cancel,
	}
}

func (a *Agent) registerTask(traceID string, cancel context.CancelFunc) {
	a.tasksMutex.Lock()
	defer a.tasksMutex.Unlock()
	a.tasks[traceID] = cancel
}

func (a *Agent) unregisterTask(traceID string) {
	a.tasksMutex.Lock()
	defer a.tasksMutex.Unlock()
	delete(a.tasks, traceID)
}

func (a *Agent) cancelTask(traceID string) {
	a.tasksMutex.Lock()
	defer a.tasksMutex.Unlock()

	if cancel, ok := a.tasks[traceID]; ok {
		cancel()
		a.logger.Info("Task cancelled", "trace_id", traceID)
	} else {
		a.logger.Warn("Task not found for cancellation", "trace_id", traceID)
	}
}

// Start begins the connection loop in a goroutine
func (a *Agent) Start() {
	a.wg.Go(func() {
		a.connectLoop()
	})
}

// Stop gracefully stops the agent
func (a *Agent) Stop() {
	a.stopOnce.Do(func() {
		a.cancel()
		a.streamMutex.Lock()
		if a.currentConn != nil {
			if err := a.currentConn.Close(); err != nil {
				a.logger.Debug("failed to close current grpc connection", "error", err)
			}
		}
		a.streamMutex.Unlock()
	})
	a.wg.Wait()
	a.executeWg.Wait() // wait for all in-flight handleCommandRequest goroutines
	a.logger.Info("Agent stopped")
}

func (a *Agent) serverAddresses() []string {
	return append([]string(nil), a.cfg.ServerAddresses...)
}

// SettingsManager holds the runtime configuration for the agent pushed from the server.
type SettingsManager struct {
	mu       sync.RWMutex
	settings *pb.AgentSettings
}

func NewSettingsManager() *SettingsManager {
	// 推送前本地默认，须与 schema 新装全局默认及 GetAgentSettingsForNode 核心字段一致（internal/contract/defaults.go）。
	return &SettingsManager{
		settings: &pb.AgentSettings{
			ExecBinPath: contract.DefaultExecBinPath,
			Vnc: &pb.VNCConfig{
				BinDir:       contract.DefaultVNCBinDir,
				ScanInterval: contract.DefaultVNCScanInterval,
				ScanEnabled:  true,
			},
			Monitor: &pb.MonitorConfig{
				MetricsEnabled:  true,
				MetricsInterval: contract.DefaultMetricsInterval,
				NvidiaSmiPath:   "",
			},
			ProbeInterval: contract.DefaultStorageProbeInterval,
		},
	}
}

func (m *SettingsManager) Update(settings *pb.AgentSettings) {
	if settings == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = settings
}

func (m *SettingsManager) Get() *pb.AgentSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.settings == nil {
		return nil
	}
	// Return a clone: multiple telemetry goroutines call Get() concurrently,
	// and protobuf messages are not safe for concurrent read access due to
	// internal lazy size-cache writes during marshal/clone.
	return proto.Clone(m.settings).(*pb.AgentSettings)
}
func (a *Agent) GetSettings() *pb.AgentSettings {
	return a.settingsMgr.Get()
}

func (a *Agent) GetActiveTaskCount() int64 {
	return a.activeTasks.Load()
}
