package probe

import (
	"context"
	"runtime"

	pb "design-cloud-engine/internal/contract/proto"
)

// StatusProbe collects resource metrics (CPU, memory, disk, GPU, load).
// Instantiate once with NewStatusProbe and call Collect repeatedly.
type StatusProbe struct {
	cpuState *cpuUsageState
}

// NewStatusProbe creates a ready-to-use StatusProbe.
func NewStatusProbe() *StatusProbe {
	return &StatusProbe{cpuState: &cpuUsageState{}}
}

// Collect gathers a full metrics snapshot for the current cycle.
//
//   - activeTasks: number of currently running jobs (reported verbatim)
//   - nvidiaSmiPath: override path for nvidia-smi; pass "" to use auto-discovery
//   - mounts: list of mount targets to probe for disk usage
func (p *StatusProbe) Collect(ctx context.Context, activeTasks int, nvidiaSmiPath string, mounts []*pb.MountTarget) (*pb.Metrics, error) {
	cpuUsage, err := p.cpuState.readCPUUsage()
	if err != nil {
		return nil, err
	}

	memInfo, err := readMemInfo()
	if err != nil {
		return nil, err
	}

	load1, load5, load15, err := readLoadAvg()
	if err != nil {
		return nil, err
	}

	uptimeSeconds, err := readUptimeSeconds()
	if err != nil {
		return nil, err
	}

	smiPath := nvidiaSmiPath
	gpus := collectGPUInfo(ctx, func(_ context.Context) string { return smiPath })

	diskUsages, _ := CollectDiskUsage(ctx, mounts)

	return &pb.Metrics{
		CpuUtil:     cpuUsage,
		MemTotal:    memInfo["MemTotal"],
		MemUsed:     memInfo["MemTotal"] - memInfo["MemAvailable"],
		SwapTotal:   memInfo["SwapTotal"],
		SwapFree:    memInfo["SwapFree"],
		Load1:       load1,
		Load5:       load5,
		Load15:      load15,
		Uptime:      uptimeSeconds,
		ActiveTasks: int64(activeTasks),
		Gpus:        gpus,
		CpuCores:    int64(runtime.NumCPU()),
		DiskUsages:  diskUsages,
	}, nil
}
