package probe

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
)

type diskProbeCall struct {
	done  chan struct{}
	usage *pb.DiskUsage
	err   error
}

var diskProbeGroup = struct {
	sync.Mutex
	calls map[string]*diskProbeCall
}{calls: make(map[string]*diskProbeCall)}

// runDiskProbe coalesces syscalls by path without registering a waiter for each
// caller. A canceled waiter's state is released immediately even if an
// uninterruptible filesystem syscall remains blocked.
func runDiskProbe(ctx context.Context, path string, probe func() (*pb.DiskUsage, error)) (*pb.DiskUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	diskProbeGroup.Lock()
	call, exists := diskProbeGroup.calls[path]
	if !exists {
		call = &diskProbeCall{done: make(chan struct{})}
		diskProbeGroup.calls[path] = call
	}
	diskProbeGroup.Unlock()

	if !exists {
		go func() {
			var usage *pb.DiskUsage
			var err error
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("disk probe panic: %v", recovered)
				}
				diskProbeGroup.Lock()
				call.usage = usage
				call.err = err
				delete(diskProbeGroup.calls, path)
				close(call.done)
				diskProbeGroup.Unlock()
			}()
			usage, err = probe()
		}()
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-call.done:
		return call.usage, call.err
	}
}

// CollectDiskUsage 并发采集所有已启用的挂载点磁盘使用情况。
// 每个路径独立 10s timeout；底层共享每路径一个探测，避免挂死的文件系统导致等待 goroutine 持续堆积。
func CollectDiskUsage(ctx context.Context, mounts []*pb.MountTarget) ([]*pb.DiskUsage, error) {
	if len(mounts) == 0 {
		return nil, nil
	}

	fsMap := detectFilesystemMap()
	var (
		mu  sync.Mutex
		out = make([]*pb.DiskUsage, 0, len(mounts))
		wg  sync.WaitGroup
	)

	for _, m := range mounts {
		if m == nil || !m.GetEnabled() {
			continue
		}
		target := m
		wg.Go(func() {
			pathCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			usage, err := StatfsWithTimeout(pathCtx, target.GetPath())
			if err != nil {
				return
			}
			usage.MonitorName = target.GetName()
			usage.MountPoint = target.GetPath()
			usage.Filesystem = lookupFilesystem(fsMap, target.GetPath())

			mu.Lock()
			out = append(out, usage)
			mu.Unlock()
		})
	}

	wg.Wait()
	return out, nil
}

// lookupFilesystem 根据路径匹配文件系统类型（优先精准匹配，其次最长前缀匹配）。
func lookupFilesystem(fsMap map[string]string, path string) string {
	cleanPath := filepath.Clean(path)
	if fstype, ok := fsMap[cleanPath]; ok {
		return fstype
	}
	var bestMatch string
	var bestLen int
	sep := string(filepath.Separator)
	for mp, fstype := range fsMap {
		cleanMP := filepath.Clean(mp)
		prefixWithSep := cleanMP
		if !strings.HasSuffix(prefixWithSep, sep) {
			prefixWithSep += sep
		}
		if (cleanPath == cleanMP || strings.HasPrefix(cleanPath, prefixWithSep) || cleanMP == sep) && len(cleanMP) > bestLen {
			bestMatch = fstype
			bestLen = len(cleanMP)
		}
	}
	if bestMatch != "" {
		return bestMatch
	}
	return "unknown"
}
