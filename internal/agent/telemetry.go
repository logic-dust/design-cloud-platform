package agent

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"design-cloud-engine/internal/agent/probe"
	pb "design-cloud-engine/internal/contract/proto"
)

// TelemetryConfig holds fixed configurations for the telemetry runner.
type TelemetryConfig struct {
	HeartbeatInterval time.Duration
}

// telemetryRunner encapsulates the state needed to construct reports.
type telemetryRunner struct {
	agent       *Agent
	config      TelemetryConfig
	logger      *slog.Logger
	statusProbe *probe.StatusProbe
}

// startTelemetry initializes and starts all telemetry background tasks.
// It returns a pointer to the created PeriodicRunner which can be used to Stop tasks or kick out-of-cycle runs.
func startTelemetry(
	ctx context.Context,
	agent *Agent,
	cfg TelemetryConfig,
	logger *slog.Logger,
) *PeriodicRunner {

	mgr := NewPeriodicRunner()

	runner := &telemetryRunner{
		agent:       agent,
		config:      cfg,
		logger:      logger,
		statusProbe: probe.NewStatusProbe(),
	}

	// 1. Heartbeat Task
	mgr.Add(task{
		Name:           "heartbeat",
		Interval:       runner.config.HeartbeatInterval,
		JitterFraction: 0.2, // ±20% randomized jitter to prevent thundering herd
		StopOnError:    true,
		Run:            runner.runHeartbeat,
	})

	// 2. Status Report Task
	mgr.Add(task{
		Name: "resource-report",
		DynamicInterval: func() time.Duration {
			if settings := runner.agent.GetSettings(); settings != nil {
				if sec := settings.GetMonitor().GetResourceReportInterval(); sec > 0 {
					return time.Duration(sec) * time.Second
				}
			}
			// Default fallback before receiving the first configuration from the server
			return 60 * time.Second
		},
		JitterFraction: 0.2,
		Run:            runner.runResourceReport,
	})

	// 3. Storage Probe Task
	mgr.Add(task{
		Name: "storage-probe",
		DynamicInterval: func() time.Duration {
			if settings := runner.agent.GetSettings(); settings != nil {
				if sec := settings.GetProbeInterval(); sec > 0 {
					return time.Duration(sec) * time.Second
				}
			}
			return 1 * time.Hour // default
		},
		Run: runner.runStorageProbe,
	})

	// 4. VNC Probe Task (periodic, replaces on-connect one-shot)
	mgr.Add(task{
		Name: "vnc-probe",
		DynamicInterval: func() time.Duration {
			if settings := runner.agent.GetSettings(); settings != nil {
				if sec := settings.GetVnc().GetScanInterval(); sec > 0 {
					return time.Duration(sec) * time.Second
				}
			}
			return 1800 * time.Second // default: 30 minutes when not configured
		},
		Run: runner.runVNCProbe,
	})

	mgr.Start(ctx)
	agent.telemetry = mgr
	return mgr
}

func (r *telemetryRunner) runHeartbeat(_ context.Context) error {
	rdp := probe.CollectRDPReadiness()
	if rdp.Determinate && !rdp.Available {
		r.logger.Warn("Remote desktop not ready", "port", rdp.Port, "reason", rdp.Reason)
	}

	// Only a determinate probe is reported; otherwise the server keeps its previous state.
	var rdpStatus *pb.RDPStatus
	if rdp.Determinate {
		rdpStatus = &pb.RDPStatus{Available: rdp.Available, Port: rdp.Port}
	}

	return r.agent.sendOnActiveStream(&pb.AgentMessage{
		Payload: &pb.AgentMessage_HeartbeatRequest{
			HeartbeatRequest: &pb.HeartbeatRequest{
				ReportedAt: time.Now().Unix(),
				Rdp:        rdpStatus,
			},
		},
	})
}

func (r *telemetryRunner) runResourceReport(ctx context.Context) error {
	settings := r.agent.GetSettings()
	if settings != nil && !settings.GetMonitor().GetResourceReportEnabled() {
		return nil // Skip reporting if disabled
	}

	var (
		smiPath string
		mounts  []*pb.MountTarget
	)
	if settings != nil && settings.GetMonitor() != nil {
		smiPath = settings.GetMonitor().GetNvidiaSmiPath()
		// 平台相关展开：Windows 会把 Linux 口径的默认 "/" 展开为全部固定盘符
		mounts = probe.ResolveMountTargets(settings.GetMonitor().GetDiskMounts())
	}

	activeTasks := r.agent.GetActiveTaskCount()
	report, err := r.statusProbe.Collect(ctx, int(activeTasks), smiPath, mounts)
	if err != nil {
		r.logger.Warn("Failed to collect status report", "error", err)
		return nil
	}
	// 采集端打点：上报数据时间（与资源数据一起组装后上报）
	report.ReportedAt = time.Now().Unix()

	return r.agent.sendOnActiveStream(&pb.AgentMessage{
		Payload: &pb.AgentMessage_ResourceReport{
			ResourceReport: report,
		},
	})
}

func (r *telemetryRunner) runStorageProbe(ctx context.Context) error {
	settings := r.agent.GetSettings()
	if settings == nil {
		return nil
	}

	storages := settings.GetStorages()
	if len(storages) == 0 {
		return nil
	}

	items := make([]*pb.StorageProbeItem, 0, len(storages))
	for _, s := range storages {
		usage, err := probe.StatfsWithTimeout(ctx, s.GetMountPoint())
		if err != nil {
			r.logger.Warn("Storage probe failed", "storage_id", s.GetId(), "error", err)
			continue
		}
		items = append(items, &pb.StorageProbeItem{
			StorageId: s.GetId(),
			Total:     usage.GetTotal(),
			Used:      usage.GetUsed(),
		})
	}

	if len(items) == 0 {
		return nil
	}

	return r.agent.sendOnActiveStream(&pb.AgentMessage{
		Payload: &pb.AgentMessage_StorageProbeReport{
			StorageProbeReport: &pb.StorageProbeReport{
				Items:      items,
				ReportedAt: time.Now().Unix(), // 采集端打点：上报数据时间
			},
		},
	})
}

func (r *telemetryRunner) runVNCProbe(_ context.Context) error {
	settings := r.agent.GetSettings()
	if settings == nil {
		return nil
	}
	vncSettings := settings.GetVnc()
	if vncSettings == nil || !vncSettings.GetScanEnabled() {
		return nil // VNC probe disabled on this node
	}
	r.agent.collectAndReportVNC()
	return nil
}

// Package reporter provides a lightweight lifecycle manager for periodic and
// one-shot agent-internal tasks. Each task runs as an independent goroutine;
// PeriodicRunner.Start/Stop controls them as a group.
//
// Usage:
//
//	mgr := reporter.NewPeriodicRunner()
//	mgr.Add(reporter.Task{Name: "health", Interval: 30*time.Second, Run: fn})
//	mgr.Add(reporter.Task{Name: "one-shot", Run: fnOnce})
//	mgr.Start(ctx)
//	defer mgr.Stop()
//
// Interval <= 0  → one-shot (runs once on Start).
// Interval > 0   → runs immediately, then every Interval.

// task represents a single task cycle.
// If Interval <= 0, Run is executed once (one-shot) when Start is called.
// If Interval > 0, Run is executed immediately and then every Interval.
type task struct {
	// Name identifies this loop for debugging and diagnostics.
	Name     string
	Interval time.Duration
	// DynamicInterval is called on each iteration if not nil, to determine the sleep duration dynamically.
	DynamicInterval func() time.Duration
	// JitterFraction introduces randomness to the Interval to prevent thundering herds.
	// E.g., 0.1 means the actual interval varies randomly by ±10%.
	JitterFraction float64
	Run            func(ctx context.Context) error
	// StopOnError causes the task to exit when Run returns a non-nil error.
	// Use this for tasks where an error means the underlying resource is
	// broken (e.g. heartbeat over a gRPC stream).
	StopOnError bool
}

// PeriodicRunner holds and manages multiple Tasks.
type PeriodicRunner struct {
	tasks     []task
	kickChans map[string]chan struct{}
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	started   bool
}

// NewPeriodicRunner creates an empty Task PeriodicRunner.
func NewPeriodicRunner() *PeriodicRunner {
	return &PeriodicRunner{
		kickChans: make(map[string]chan struct{}),
	}
}

// Add registers a task. Must be called before Start; calling Add after
// Start is a programming error and will be silently discarded with a warning.
func (m *PeriodicRunner) Add(t task) {
	if m.started {
		slog.Warn("reporter: Add called after Start — task discarded", "task", t.Name)
		return
	}
	m.tasks = append(m.tasks, t)
	m.kickChans[t.Name] = make(chan struct{}, 1)
}

// TriggerNow requests an immediate out-of-cycle execution of a periodic task.
// If the task is currently running or already has a pending kick, the request
// is coalesced (dropped) — at most one extra run is queued. Returns false if
// the task name is unknown or the kick was dropped.
// Has no effect on one-shot tasks (they have already exited).
func (m *PeriodicRunner) TriggerNow(name string) bool {
	ch, ok := m.kickChans[name]
	if !ok {
		return false
	}
	select {
	case ch <- struct{}{}:
		return true
	default:
		return false
	}
}

// Start launches all registered tasks as independent goroutines.
// ctx is the parent context; PeriodicRunner stops all tasks when ctx is cancelled
// or when Stop is called.
func (m *PeriodicRunner) Start(ctx context.Context) {
	m.started = true
	ctx, m.cancel = context.WithCancel(ctx)
	for _, t := range m.tasks {
		m.wg.Go(func() {
			m.runTask(ctx, t)
		})
	}
}

// Stop cancels all tasks and waits for them to finish.
func (m *PeriodicRunner) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}

func (m *PeriodicRunner) executeTask(ctx context.Context, t task) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("task panic: %v", r)
			slog.Error("periodic task panic recovered",
				"task", t.Name,
				"panic", r,
				"stack", string(debug.Stack()),
			)
		}
	}()

	return t.Run(ctx)
}

func (m *PeriodicRunner) runTask(ctx context.Context, t task) {
	if t.Interval <= 0 && t.DynamicInterval == nil {
		// One-shot: run once and exit.
		_ = m.executeTask(ctx, t)
		return
	}

	// Periodic: run once immediately, then on randomized timer.
	if err := m.executeTask(ctx, t); err != nil && t.StopOnError {
		return
	}

	for {
		var sleepDuration time.Duration
		if t.DynamicInterval != nil {
			sleepDuration = t.DynamicInterval()
		} else {
			sleepDuration = t.Interval
		}

		if t.JitterFraction > 0 {
			jitter := float64(sleepDuration) * t.JitterFraction
			// rand.Float64() returns [0.0, 1.0) -> (rand.Float64()*2 - 1) returns [-1.0, 1.0)
			delta := time.Duration((rand.Float64()*2.0 - 1.0) * jitter)
			sleepDuration += delta
			if sleepDuration <= 0 {
				sleepDuration = time.Millisecond // safeguard
			}
		}

		timer := time.NewTimer(sleepDuration)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if err := m.executeTask(ctx, t); err != nil && t.StopOnError {
				return
			}
		case <-m.kickChans[t.Name]:
			// Out-of-cycle trigger: stop the pending timer, run now,
			// then the loop recalculates a fresh full interval.
			timer.Stop()
			if err := m.executeTask(ctx, t); err != nil && t.StopOnError {
				return
			}
		}
	}
}

// collectAndReportVNC collects local VNC connections and reports them to the control server.
// Called only by the vnc-probe periodic task; out-of-cycle triggers go through TriggerNow
// which wakes the same task goroutine, so no concurrent-run guard is needed here.
func (a *Agent) collectAndReportVNC() {
	connections, err := probe.CollectRemoteConnections(a.ctx)
	if err != nil {
		a.logger.Warn("Failed to collect VNC connections",
			"nodeID", a.nodeIdentity,
			"error", err)
		return
	}

	report := &pb.ConnectionReport{
		ReportedAt:  time.Now().Unix(),
		Connections: make([]*pb.ConnectionInfo, 0, len(connections)),
	}
	for _, conn := range connections {
		pid, _ := strconv.Atoi(conn.PID)
		report.Connections = append(report.Connections, &pb.ConnectionInfo{
			ConnectionId: conn.ConnectionID,
			Name:         conn.ConnectionName,
			Username:     conn.Username,
			Host:         a.nodeIdentity,
			Port:         int32(conn.Port),
			Display:      fmt.Sprintf(":%d", conn.Display),
			Protocol:     conn.Protocol,
			Pid:          int32(pid),
		})
	}

	err = a.sendOnActiveStream(&pb.AgentMessage{
		Payload: &pb.AgentMessage_ConnectionReport{
			ConnectionReport: report,
		},
	})

	if err != nil {
		a.logger.Warn("Failed to send connection report",
			"nodeID", a.nodeIdentity,
			"connections", len(connections),
			"error", err)
		return
	}

	a.logger.Debug("Sent VNC connection report",
		"nodeID", a.nodeIdentity,
		"connections", len(connections))
}
