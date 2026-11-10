//go:build windows

package probe

import (
	"context"
	"os/exec"

	"design-cloud-engine/internal/platform/winsession"
)

func collectRDPDesktopSessions(ctx context.Context) []RDPSessionSnapshot {
	out, err := exec.CommandContext(ctx, "query", "user").CombinedOutput()
	if err != nil && len(out) == 0 {
		return nil
	}
	var seats []RDPSessionSnapshot
	for _, s := range winsession.ParseQueryUserSessions(string(out)) {
		if !winsession.IsRemoteDesktopSessionName(s.Name) || s.ID == 0 {
			continue
		}
		ok, err := winsession.IsFullDesktop(ctx, s.ID)
		if err != nil || !ok {
			continue
		}
		seats = append(seats, RDPSessionSnapshot{
			Username:    s.Username,
			SessionID:   s.ID,
			SessionName: s.Name,
		})
	}
	return seats
}
