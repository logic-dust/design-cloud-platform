//go:build !linux

package probe

import "context"

func enrichXRDPSeatsSessionEnding(_ context.Context, seats []XRDPSeatSnapshot) []XRDPSeatSnapshot {
	return seats
}
