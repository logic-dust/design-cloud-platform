package probe

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
)

func TestCollectDiskUsageRootPartition(t *testing.T) {
	// Test with the root partition
	mounts := []*pb.MountTarget{
		{Name: "Root Partition", Path: "/", Enabled: true},
	}
	usages, err := CollectDiskUsage(t.Context(), mounts)
	if err != nil {
		t.Fatalf("CollectDiskUsage failed: %v", err)
	}
	if len(usages) == 0 {
		t.Fatal("expected at least one disk usage result for root partition")
	}
	if usages[0].GetMountPoint() != "/" {
		t.Fatalf("mount point = %s, want /", usages[0].GetMountPoint())
	}
	if usages[0].GetMonitorName() != "Root Partition" {
		t.Fatalf("monitorName = %s, want Root Partition", usages[0].GetMonitorName())
	}
	if usages[0].GetTotal() <= 0 {
		t.Fatal("expected total > 0")
	}
	if usages[0].GetFilesystem() == "" {
		t.Fatal("expected non-empty filesystem type for root partition")
	}
}

func TestCollectDiskUsageDisabledMount(t *testing.T) {
	// Disabled mounts should be skipped
	mounts := []*pb.MountTarget{
		{Name: "DisabledRoot", Path: "/", Enabled: false},
	}
	usages, err := CollectDiskUsage(t.Context(), mounts)
	if err != nil {
		t.Fatalf("CollectDiskUsage failed: %v", err)
	}
	if len(usages) != 0 {
		t.Fatalf("expected 0 entries for disabled mount, got %d", len(usages))
	}
}

func TestCollectDiskUsageWithInvalidPath(t *testing.T) {
	mounts := []*pb.MountTarget{
		{Name: "Invalid", Path: "/path/to/nowhere/that/does/not/exist", Enabled: true},
	}
	usages, err := CollectDiskUsage(t.Context(), mounts)
	if err != nil {
		t.Fatalf("CollectDiskUsage failed: %v", err)
	}
	// Invalid paths should be skipped
	if len(usages) != 0 {
		t.Fatalf("expected 0 entries for invalid path, got %d", len(usages))
	}
}

func TestStatfsWithTimeout_SingleflightPreventsGoroutineLeak(t *testing.T) {
	origStatfs := statfsFn
	defer func() { statfsFn = origStatfs }()

	var calls atomic.Int32
	blockCh := make(chan struct{})
	defer close(blockCh)

	statfsFn = func(path string, stat *syscall.Statfs_t) error {
		calls.Add(1)
		<-blockCh
		stat.Blocks = 1000
		stat.Bfree = 400
		stat.Frsize = 1024
		return nil
	}

	testPath := "/mock/nfs/hang"

	// Concurrently invoke multiple probes on the hanging path
	const concurrentCallers = 5
	var wg sync.WaitGroup
	errs := make([]error, concurrentCallers)

	for i := range concurrentCallers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			_, errs[idx] = StatfsWithTimeout(ctx, testPath)
		}(i)
	}

	wg.Wait()

	// All callers should have timed out
	for i, err := range errs {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("caller %d expected DeadlineExceeded, got %v", i, err)
		}
	}

	// Crucial assertion: singleflight coalesced all callers; statfsFn was invoked only ONCE!
	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 statfs call due to singleflight, got %d", calls.Load())
	}
}

func TestLookupFilesystem(t *testing.T) {
	fsMap := map[string]string{
		"/":         "ext4",
		"/data":     "ext4",
		"/data/sub": "xfs",
		"/data2":    "nfs4",
	}

	tests := []struct {
		path     string
		wantType string
	}{
		{"/", "ext4"},
		{"/data", "ext4"},
		{"/data/sub", "xfs"},
		{"/data/sub/deep/file.txt", "xfs"},
		{"/data/other/file.txt", "ext4"},
		{"/data2", "nfs4"},
		{"/data2/file.txt", "nfs4"},
		{"/etc/hosts", "ext4"}, // falls back to /
	}

	for _, tc := range tests {
		got := lookupFilesystem(fsMap, tc.path)
		if got != tc.wantType {
			t.Errorf("lookupFilesystem(%q) = %q, want %q", tc.path, got, tc.wantType)
		}
	}
}
