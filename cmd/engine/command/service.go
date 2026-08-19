package command

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	fsutil "design-cloud-engine/internal/common/fs"
)

// readPIDRecord reads the process ID and optional start time from the sentinel PID file.
// Format on disk: "<pid>" or "<pid> <starttime>".
func readPIDRecord() (int, uint64, error) {
	path := fsutil.PIDFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, 0, fmt.Errorf("empty PID file %s", path)
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, err
	}
	var starttime uint64
	if len(fields) >= 2 {
		starttime, _ = strconv.ParseUint(fields[1], 10, 64)
	}
	return pid, starttime, nil
}
