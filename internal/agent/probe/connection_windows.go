package probe

import "context"

// CollectConnectionRuntime on Windows reports full-desktop RDP sessions.
// VNC/XRDP seats are Linux-only.
func CollectConnectionRuntime(ctx context.Context) (ConnectionSnapshot, error) {
	return ConnectionSnapshot{
		RDPSessions: collectRDPDesktopSessions(ctx),
	}, nil
}
