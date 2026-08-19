package contract

// Protocol defines a supported proxy/connection protocol.
type Protocol string

const (
	VNCProtocol Protocol = "VNC"
	RDPProtocol Protocol = "RDP"
	SSHProtocol Protocol = "SSH"
)

// ConnectionInfo is the lightweight runtime payload used to identify a
// connection outside of the persisted Connection model.
type ConnectionInfo struct {
	ConnectionID uint     `json:"connectionId,omitempty"`
	Name         string   `json:"name,omitempty"`
	Username     string   `json:"username,omitempty"`
	Host         string   `json:"host,omitempty"`
	Port         uint     `json:"port,omitempty"`
	Display      string   `json:"display,omitempty"`
	Protocol     Protocol `json:"protocol,omitempty"`
	PID          uint     `json:"pid,omitempty"`
}
