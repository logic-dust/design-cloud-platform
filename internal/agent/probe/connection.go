package probe

// VNCSeatSnapshot is a VNC seat process seen on this node.
type VNCSeatSnapshot struct {
	PID            string
	Username       string
	ConnectionID   int64
	ConnectionName string
	Display        int
	Port           int
	StartedAt      int64
}

// XRDPSeatSnapshot is an xrdp-managed Xorg seat seen on this node.
type XRDPSeatSnapshot struct {
	PID                    string
	Username               string
	ConnectionID           int64
	ConnectionName         string
	Display                int
	SessionEndingConfirmed bool
	StartedAt              int64
}

// RDPSessionSnapshot is a full-desktop Windows RDP session.
type RDPSessionSnapshot struct {
	Username    string
	SessionID   int
	SessionName string
}

// ConnectionSnapshot encapsulates connection seats/sessions collected from a node.
type ConnectionSnapshot struct {
	VNCSeats    []VNCSeatSnapshot
	XRDPSeats   []XRDPSeatSnapshot
	RDPSessions []RDPSessionSnapshot
}
