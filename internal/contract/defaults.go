package contract

// Agent / service 共享默认常量
//
// agent 进程（internal/agent）受架构约束不能 import internal/server，因此无法直接
// 引用 settings schema 的默认值；而服务端 settings 种子（schema/defaults.go）又需要
// 相同的安装路径默认。为消除「同一默认值双源漂移」，把这些跨进程共享的安装默认
// 收敛到中立契约层，供 internal/agent 与 internal/server/service/settings/schema 共同引用。
const (
	// DefaultLauncherPath 是 Agent 底座 dc-launcher 的默认绝对路径
	// （对应 settings DomainAgent.launcherPath 默认）。
	DefaultLauncherPath = "/opt/design-cloud/dc-launcher"

	// DefaultVNCBinDir 是 TurboVNC 工具目录
	// （对应 settings DomainAgent.vnc.binDir 默认）。
	DefaultVNCBinDir = "/opt/TurboVNC/bin"
	// DefaultVNCScanInterval 是 VNC 进程周期扫描默认间隔（秒）
	// （对应 settings DomainAgent.vnc.scanInterval 默认，30 分钟）。
	DefaultVNCScanInterval = 1800
)
