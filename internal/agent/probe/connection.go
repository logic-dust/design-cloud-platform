package probe

// RDPReadiness is the platform-neutral result of a remote-desktop readiness probe.
// Linux never produces a determinate result (remote desktops are reported as VNC/xrdp
// connections instead), so callers must check Determinate before reporting upstream.
type RDPReadiness struct {
	Determinate bool   // false = 探活不可判定，调用方不应上报（保持服务端既有状态）
	Available   bool   // RDP 是否可接受连接
	Port        uint32 // RDP 监听端口
	Reason      string // 判定依据摘要，用于日志与排障
}

// ConnectionSnapshot is a platform-neutral remote-desktop session snapshot.
// Linux populates it from VNC/XRDP process scanning; Windows reports none
// (RDP is a single system service, not multi-process).
type ConnectionSnapshot struct {
	PID            string
	Username       string
	ConnectionID   int64
	ConnectionName string
	Display        int
	Port           int
	Protocol       string
}
