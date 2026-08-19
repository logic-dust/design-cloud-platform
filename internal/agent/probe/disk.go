package probe

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
)

// CollectDiskUsage 并发采集所有已启用的挂载点磁盘使用情况。
// 每个路径独立 10s timeout；底层通过 singleflight 防重入，防止挂死（如 NFS hard mount 离线）导致 goroutine 持续堆积。
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
