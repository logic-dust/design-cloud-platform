package probe

import (
	"context"
	"os/exec"
	"strconv"
	"strings"

	pb "design-cloud-engine/internal/contract/proto"
)

type cpuUsageState struct {
	prevIdle  uint64
	prevTotal uint64
	hasPrev   bool
}

func (s *cpuUsageState) readCPUUsage() (float64, error) {
	idle, total, err := readCPUStat()
	if err != nil {
		return 0, err
	}

	if !s.hasPrev {
		s.prevIdle = idle
		s.prevTotal = total
		s.hasPrev = true
		return 0, nil
	}

	totalDelta := total - s.prevTotal
	idleDelta := idle - s.prevIdle
	s.prevIdle = idle
	s.prevTotal = total

	if totalDelta == 0 {
		return 0, nil
	}

	busyDelta := totalDelta - idleDelta
	return float64(busyDelta) / float64(totalDelta), nil
}

// collectGPUInfo queries GPU metrics via nvidia-smi CLI.
//
// Design rationale: nvidia-smi is chosen over NVML library bindings because:
//   - Reporting interval is ≥30s (default 60s), so CLI overhead (~200ms per call) is negligible.
//   - nvidia-smi is universally available on NVIDIA GPU nodes; NVML requires libnvidia-ml.so.
//   - Non-NVIDIA GPUs (AMD, Intel) may be added in the future with their own CLI tools.
//   - For higher-frequency (≤10s) reporting, consider switching to go-nvml or DCGM.
func collectGPUInfo(ctx context.Context, getNvidiaSmiPath func(context.Context) string) []*pb.GPUInfo {
	smiPath := ""
	if getNvidiaSmiPath != nil {
		smiPath = getNvidiaSmiPath(ctx)
	}
	if smiPath == "" {
		smiPath = findNvidiaSmi()
	}

	if smiPath == "" {
		return nil
	}

	output, err := exec.CommandContext(ctx, smiPath,
		"--query-gpu=name,memory.total,memory.used,utilization.gpu",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}

	var gpus []*pb.GPUInfo
	for line := range strings.Lines(string(output)) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Split(line, ",")
		if len(parts) < 4 {
			continue
		}

		name := strings.TrimSpace(parts[0])
		memTotalStr := strings.TrimSpace(parts[1])
		memUsedStr := strings.TrimSpace(parts[2])
		utilStr := strings.TrimSpace(parts[3])

		memTotal, err1 := strconv.ParseInt(memTotalStr, 10, 64)
		memUsed, err2 := strconv.ParseInt(memUsedStr, 10, 64)
		util, err3 := strconv.ParseUint(utilStr, 10, 32)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}

		gpus = append(gpus, &pb.GPUInfo{
			Name:        name,
			MemTotal:    memTotal * 1024,
			MemUsed:     memUsed * 1024,
			Utilization: float64(util) / 100.0,
		})
	}

	return gpus
}
