package probe

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func readCPUStat() (idle uint64, total uint64, err error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, fmt.Errorf("open /proc/stat: %w", err)
	}
	defer func(f *os.File) { _ = f.Close() }(f)

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return 0, 0, fmt.Errorf("scan /proc/stat: %w", err)
		}
		return 0, 0, fmt.Errorf("missing cpu line in /proc/stat")
	}

	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, fmt.Errorf("unexpected cpu stat line: %q", scanner.Text())
	}

	for i := 1; i < len(fields); i++ {
		value, parseErr := strconv.ParseUint(fields[i], 10, 64)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("parse /proc/stat field %q: %w", fields[i], parseErr)
		}
		total += value
		if i == 4 || i == 5 {
			idle += value
		}
	}

	return idle, total, nil
}

func readMemInfo() (map[string]int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, fmt.Errorf("open /proc/meminfo: %w", err)
	}
	defer func(f *os.File) { _ = f.Close() }(f)

	required := map[string]int64{
		"MemTotal":     0,
		"MemAvailable": 0,
		"SwapTotal":    0,
		"SwapFree":     0,
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if _, ok := required[key]; !ok {
			continue
		}
		value, parseErr := strconv.ParseInt(fields[1], 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("parse /proc/meminfo field %q: %w", key, parseErr)
		}
		required[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan /proc/meminfo: %w", err)
	}

	if required["MemTotal"] == 0 {
		return nil, fmt.Errorf("MemTotal not found in /proc/meminfo")
	}
	return required, nil
}

func readLoadAvg() (float64, float64, float64, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, fmt.Errorf("read /proc/loadavg: %w", err)
	}

	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("unexpected /proc/loadavg contents: %q", string(data))
	}

	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse load1: %w", err)
	}
	load5, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse load5: %w", err)
	}
	load15, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse load15: %w", err)
	}

	return load1, load5, load15, nil
}

func readUptimeSeconds() (int64, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, fmt.Errorf("read /proc/uptime: %w", err)
	}

	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("unexpected /proc/uptime contents: %q", string(data))
	}

	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("parse uptime: %w", err)
	}
	return int64(uptime), nil
}

// trustedNvidiaSmiPaths defines vetted, secure absolute paths to nvidia-smi on Linux.
// Relying strictly on fixed trusted system paths avoids CWE-426 (untrusted search path)
// vulnerabilities from resolving arbitrary executables in an unverified PATH environment.
var trustedNvidiaSmiPaths = []string{
	"/usr/bin/nvidia-smi",
	"/usr/local/bin/nvidia-smi",
	"/usr/local/cuda/bin/nvidia-smi",
	"/opt/nvidia/bin/nvidia-smi",
}

// findNvidiaSmi returns the absolute path to nvidia-smi on Linux using trusted system paths.
func findNvidiaSmi() string {
	for _, p := range trustedNvidiaSmiPaths {
		info, err := os.Stat(p)
		if err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}
