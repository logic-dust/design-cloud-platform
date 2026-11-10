package contract

type VNCSeatInfo struct {
	ConnectionID uint   `json:"connectionId,omitempty"`
	Name         string `json:"name,omitempty"`
	Username     string `json:"username,omitempty"`
	Port         uint   `json:"port,omitempty"`
	Display      *int   `json:"display,omitempty"`
	PID          uint   `json:"pid,omitempty"`
	StartedAt    int64  `json:"startedAt,omitempty"`
}

type XRDPSeatInfo struct {
	ConnectionID           uint   `json:"connectionId,omitempty"`
	Name                   string `json:"name,omitempty"`
	Username               string `json:"username,omitempty"`
	Display                *int   `json:"display,omitempty"`
	PID                    uint   `json:"pid,omitempty"`
	SessionEndingConfirmed bool   `json:"sessionEndingConfirmed,omitempty"`
	StartedAt              int64  `json:"startedAt,omitempty"`
}

// RDPSessionInfo is a full-desktop Windows RDP session (not RemoteApp / console).
type RDPSessionInfo struct {
	Username    string `json:"username,omitempty"`
	SessionID   int    `json:"sessionId,omitempty"`
	SessionName string `json:"sessionName,omitempty"`
}

type ConnectionReport struct {
	ReportedAt  int64
	VNCSeats    []VNCSeatInfo
	XRDPSeats   []XRDPSeatInfo
	RDPSessions []RDPSessionInfo
}
