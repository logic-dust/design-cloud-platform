package payload

// --- Connection Check Action ---

// ConnectionCheckItem carries the identifying fields for a single connection in a CHECK request.
type ConnectionCheckItem struct {
	ConnectionID uint   `json:"connectionId"`
	Username     string `json:"username"`
	Host         string `json:"host,omitempty"`
	Port         uint   `json:"port,omitempty"`
	Display      string `json:"display,omitempty"`
	Protocol     string `json:"protocol,omitempty"`
	PID          uint   `json:"pid,omitempty"`
	Name         string `json:"name,omitempty"`
}

// ConnectionCheck is the payload for CONNECTION_CHECK.
type ConnectionCheck struct {
	Connections []ConnectionCheckItem `json:"connections"`
}

// VNCGenerateOTP asks the launcher to generate a TurboVNC
// protocol-level one-time password for an already-running VNC display.
type VNCGenerateOTP struct {
	ConnectionID  uint   `json:"connectionId"`
	Display       string `json:"display"`
	VNCPasswdPath string `json:"vncpasswdPath"`
}

type VNCGenerateOTPResult struct {
	OTP string `json:"otp"`
}

// VNCControl asks the launcher to start or stop a VNC server
// on the target node for a fixed connection.
type VNCControl struct {
	ConnectionID  uint     `json:"connectionId"`
	Display       string   `json:"display"`
	Operation     string   `json:"operation"` // "start" or "stop"
	VNCServerPath string   `json:"vncserverPath"`
	StartOptions  string   `json:"startOptions,omitempty"`
	ServerArgs    []string `json:"serverArgs,omitempty"`
}

type VNCControlResult struct{}

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
