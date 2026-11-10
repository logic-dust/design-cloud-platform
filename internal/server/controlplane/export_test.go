package controlplane

import "time"

// SetHeartbeatFlushIntervalForTest overrides the heartbeat flush threshold for tests.
// Defined in export_test.go so production binaries do not carry the hook.
func (s *Server) SetHeartbeatFlushIntervalForTest(d time.Duration) {
	s.heartbeatFlushInterval = d
}
