package probe

import (
	"context"
	"os"
	"strings"
	"syscall"

	pb "design-cloud-engine/internal/contract/proto"
	"golang.org/x/sync/singleflight"
)

// statfsFn is the underlying syscall.Statfs implementation.
// Replaced in tests to inject faults or simulate latency without root or real mounts.
var statfsFn = syscall.Statfs

// diskSFGroup deduplicates concurrent Statfs calls on the same path.
// Prevents goroutine accumulation when a path hangs (e.g. NFS hard mount offline).
var diskSFGroup singleflight.Group

// StatfsWithTimeout runs statfsFn with context cancellation support via singleflight.
// Concurrent callers for the same path share a single in-flight syscall.
// If the context expires before the syscall returns, context.DeadlineExceeded is returned;
// the blocked goroutine is left running (OS limitation) but will be GC'd once it unblocks.
func StatfsWithTimeout(ctx context.Context, path string) (*pb.DiskUsage, error) {
	type result struct {
		usage *pb.DiskUsage
		err   error
	}

	ch := make(chan result, 1)
	go func() {
		v, err, _ := diskSFGroup.Do(path, func() (any, error) {
			var stat syscall.Statfs_t
			if err := statfsFn(path, &stat); err != nil {
				return nil, err
			}
			total := (stat.Blocks * uint64(stat.Frsize)) / 1024
			free := (stat.Bfree * uint64(stat.Frsize)) / 1024
			used := total - free
			return &pb.DiskUsage{
				MountPoint: path,
				Total:      int64(total),
				Used:       int64(used),
			}, nil
		})
		if err != nil {
			ch <- result{nil, err}
			return
		}
		ch <- result{v.(*pb.DiskUsage), nil}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		return res.usage, res.err
	}
}

// detectFilesystemMap 从 Linux /proc/mounts 读取当前系统的挂载点与文件系统类型映射。
func detectFilesystemMap() map[string]string {
	res := make(map[string]string)
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return res
	}
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			// fields[1] 为挂载点路径，fields[2] 为文件系统类型（如 ext4, xfs, nfs4 等）
			res[fields[1]] = fields[2]
		}
	}
	return res
}
