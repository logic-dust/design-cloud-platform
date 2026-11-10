package contract

// Agent / service 共享默认常量
//
// agent 进程（internal/agent）受架构约束不能 import internal/server，因此无法直接
// 引用 settings schema 的默认值；而服务端 settings 种子（schema/defaults.go）又需要
// 相同的安装路径默认。为消除「同一默认值双源漂移」，把这些跨进程共享的安装默认
// 收敛到中立契约层，供 internal/agent 与 internal/server/service/settings/schema 共同引用。
//
// Invariant（推送前）：Agent 本地 bootstrap 的 pb.AgentSettings 应与「新装全局默认 +
// 默认 Storage 探测周期」一致（不含节点覆盖、不含按节点展开的 storages 列表）。
// 推送后以服务端下发的 AgentSettings 为准。
const (
	// DefaultExecBinPath 是 Agent 底座 PrivilegedExec 的默认绝对路径
	// （对应 settings DomainAgent.execBinPath 默认）。
	DefaultExecBinPath = "/opt/dce/dc-exec"

	// DefaultVNCBinDir 是 TurboVNC 工具目录
	// （对应 settings DomainAgent.vnc.binDir 默认）。
	DefaultVNCBinDir = "/opt/TurboVNC/bin"

	// DefaultVNCScanInterval 是 VNC 进程周期扫描默认间隔（秒）
	// （对应 settings DomainAgent.vnc.scanInterval 默认，1 分钟；0=不启用周期扫描）。
	DefaultVNCScanInterval = 60

	// DefaultMetricsInterval 是 Agent 资源指标上报默认间隔（秒）
	// （对应 settings DomainAgent.monitor.metricsInterval 默认）。
	DefaultMetricsInterval = 60

	// DefaultMetricsFlushInterval 是控制面 node_runtime 侧表落盘的最小间隔（秒）。
	// Agent 上报更密时按此节流，避免每帧全字段 upsert；overview 的资源新鲜度
	// 窗口按此值的两倍兜底，调整时须同步 overview.metricsStaleFloor。
	DefaultMetricsFlushInterval = 180

	// DefaultStorageProbeInterval 是共享存储探测默认间隔（秒）
	// （对应 settings DomainStorage.probeInterval 默认；下发在 AgentSettings.probe_interval）。
	DefaultStorageProbeInterval = 3600
)
