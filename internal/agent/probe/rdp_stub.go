//go:build !windows

package probe

import "context"

func collectRDPDesktopSessions(_ context.Context) []RDPSessionSnapshot { return nil }
