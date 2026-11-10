package action

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	defaultDisplayMin      = 1
	defaultDisplayMax      = 99
	maxDynamicDisplayTries = 3
)

var (
	vncDisplayPickMu sync.Mutex
	x11SocketDir     = "/tmp/.X11-unix"
	x11LockDir       = "/tmp"
)

// scanXDisplays returns display numbers that appear occupied on this host (X11 socket / lock).
func scanXDisplays() map[int]struct{} {
	taken := make(map[int]struct{})
	for n := range 100 {
		if displaySocketExists(n) {
			taken[n] = struct{}{}
		}
	}
	return taken
}

func displaySocketExists(display int) bool {
	sock := filepath.Join(x11SocketDir, fmt.Sprintf("X%d", display))
	if _, err := os.Stat(sock); err == nil {
		return true
	}
	lock := filepath.Join(x11LockDir, fmt.Sprintf(".X%d-lock", display))
	if _, err := os.Stat(lock); err == nil {
		return true
	}
	return false
}

func mergeAvoidSets(live map[int]struct{}, avoid []int) map[int]struct{} {
	taken := make(map[int]struct{}, len(live)+len(avoid))
	for d := range live {
		taken[d] = struct{}{}
	}
	for _, d := range avoid {
		taken[d] = struct{}{}
	}
	return taken
}

func pickLowestFreeDisplay(taken map[int]struct{}, min, max int) (int, bool) {
	if min < 0 {
		min = defaultDisplayMin
	}
	if max <= 0 || max < min {
		max = defaultDisplayMax
	}
	for d := min; d <= max; d++ {
		if _, ok := taken[d]; !ok {
			return d, true
		}
	}
	return 0, false
}

func displayTaken(taken map[int]struct{}, display int) bool {
	_, ok := taken[display]
	return ok
}
