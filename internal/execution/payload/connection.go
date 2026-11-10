package payload

// --- Connection Check Action ---

// ConnectionCheckItem carries the identifying fields for a single connection in a CHECK request.
type ConnectionCheckItem struct {
	ConnectionID uint   `json:"connectionId"`
	Username     string `json:"username"`
	Host         string `json:"host,omitempty"`
	Port         uint   `json:"port,omitempty"`
	Display      *int   `json:"display,omitempty"` // nil 表示无显示号；0 表示 :0
	Protocol     string `json:"protocol,omitempty"`
	PID          uint   `json:"pid,omitempty"`
	Name         string `json:"name,omitempty"`
}

// ConnectionCheck is the payload for CONNECTION_CHECK.
type ConnectionCheck struct {
	Connections []ConnectionCheckItem `json:"connections"`
}

// VNCGenerateOTP asks the PrivilegedExec to generate a TurboVNC
// protocol-level one-time password for an already-running VNC display.
type VNCGenerateOTP struct {
	ConnectionID  uint   `json:"connectionId"`
	Display       *int   `json:"display"` // 必填。0 表示 :0，不能把空值当成 0
	VNCPasswdPath string `json:"vncpasswdPath"`
}

type VNCGenerateOTPResult struct {
	OTP string `json:"otp"`
}

// VNCControl asks the PrivilegedExec to start or stop a VNC server
// on the target node for a fixed connection.
type VNCControl struct {
	ConnectionID  uint     `json:"connectionId"`
	Display       *int     `json:"display"`   // start：nil=Agent 动态选号；0= :0；>0=指定号。stop 必填。
	Operation     string   `json:"operation"` // "start" or "stop"
	VNCServerPath string   `json:"vncserverPath"`
	VNCPasswdPath string   `json:"vncpasswdPath,omitempty"` // TurboVNC vncpasswd；start 时用于缺省创建 ~/.vnc/passwd
	PasswdMode    string   `json:"passwdMode,omitempty"`    // "user"（默认，沿用 ~/.vnc/passwd）或 "managed"（私有路径，启动后删除）
	StartOptions  string   `json:"startOptions,omitempty"`
	ServerArgs    []string `json:"serverArgs,omitempty"`
	AvoidDisplays []int    `json:"avoidDisplays,omitempty"` // 动态 start：Server 计算的避开列表
	DisplayMin    int      `json:"displayMin,omitempty"`    // 动态 start，默认 1
	DisplayMax    int      `json:"displayMax,omitempty"`    // 动态 start，默认 99
}

type VNCControlResult struct {
	Display int `json:"display,omitempty"`
	Port    int `json:"port,omitempty"`
	PID     int `json:"pid,omitempty"`
}

// XRDPControl asks the PrivilegedExec to stop an XRDP user X seat as the target user.
// Never stops the system xrdp / xrdp-sesman daemons.
type XRDPControl struct {
	ConnectionID uint   `json:"connectionId"`
	Display      *int   `json:"display"`       // required for stop; 0 = :0
	Operation    string `json:"operation"`     // "stop"
	PID          uint   `json:"pid,omitempty"` // optional hint from DB/probe
}

type XRDPControlResult struct {
	Display int `json:"display,omitempty"`
	PID     int `json:"pid,omitempty"`
}

// SessionControl stops a remote login session for dial protocols (SSH/RDP).
// Never stops sshd / TermService system daemons. Scope is per-user on the target
// node (all ssh/RDP logins for that username), matching the dial MaxPerUser model.
type SessionControl struct {
	ConnectionID uint   `json:"connectionId"`
	Protocol     string `json:"protocol"`  // "SSH" or "RDP"
	Operation    string `json:"operation"` // "stop"
	Username     string `json:"username"`  // OS / portal mapped login name on the node
}

type SessionControlResult struct {
	Terminated int `json:"terminated,omitempty"` // sessions/processes ended
}

// ConnectionCheckState is the liveness verdict for a single connection.
type ConnectionCheckState string

const (
	ConnectionCheckStateRunning ConnectionCheckState = "running"
	ConnectionCheckStateStopped ConnectionCheckState = "stopped"
	ConnectionCheckStateUnknown ConnectionCheckState = "unknown"
)

// ConnectionCheckItemResult is the per-connection result returned by CONNECTION_CHECK.
type ConnectionCheckItemResult struct {
	ConnectionID uint                 `json:"connectionId"`
	State        ConnectionCheckState `json:"state"`
	Info         *ConnectionCheckItem `json:"info,omitempty"`
	Message      string               `json:"message,omitempty"`
}

// ConnectionCheckResult is the full result for CONNECTION_CHECK.
type ConnectionCheckResult struct {
	Connections []ConnectionCheckItemResult `json:"connections"`
}
