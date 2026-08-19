package probe

import (
	"cmp"
	"context"
	"path/filepath"
	"strings"
	"unsafe"

	pb "design-cloud-engine/internal/contract/proto"
	"golang.org/x/sync/singleflight"
	"golang.org/x/sys/windows"
)

var (
	kernel32Dll                = windows.NewLazySystemDLL("kernel32.dll")
	procGetLogicalDriveStrings = kernel32Dll.NewProc("GetLogicalDriveStringsW")
	diskSFGroupWin             singleflight.Group
)

// StatfsWithTimeout queries disk space on Windows using GetDiskFreeSpaceExW with timeout.
func StatfsWithTimeout(ctx context.Context, path string) (*pb.DiskUsage, error) {
	type result struct {
		usage *pb.DiskUsage
		err   error
	}

	cleanPath := filepath.Clean(path)
	ch := make(chan result, 1)
	go func() {
		v, err, _ := diskSFGroupWin.Do(cleanPath, func() (any, error) {
			pathPtr, err := windows.UTF16PtrFromString(cleanPath)
			if err != nil {
				return nil, err
			}
			var freeBytesAvailable, totalBytes, totalFreeBytes uint64
			if err := windows.GetDiskFreeSpaceEx(pathPtr, &freeBytesAvailable, &totalBytes, &totalFreeBytes); err != nil {
				return nil, err
			}
			totalKB := int64(totalBytes / 1024)
			usedKB := totalKB - int64(totalFreeBytes/1024)
			return &pb.DiskUsage{
				MountPoint: cleanPath,
				Total:      totalKB,
				Used:       usedKB,
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

// detectFilesystemMap on Windows returns drive letter -> filesystem type.
func detectFilesystemMap() map[string]string {
	result := make(map[string]string)
	buf := make([]uint16, 256)
	n, _, _ := procGetLogicalDriveStrings.Call(uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	if n == 0 {
		return result
	}
	drives := windows.UTF16ToString(buf[:n])
	for _, d := range strings.Split(drives, "\x00") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		cleanDrive := filepath.Clean(d)
		// GetVolumeInformation expects a trailing backslash for root paths (e.g. "C:\").
		rootPath := d
		if !strings.HasSuffix(rootPath, `\`) {
			rootPath += `\`
		}
		rootPtr, err := windows.UTF16PtrFromString(rootPath)
		if err != nil {
			result[cleanDrive] = "NTFS"
			continue
		}

		fsNameBuf := make([]uint16, 256)
		if err := windows.GetVolumeInformation(rootPtr, nil, 0, nil, nil, nil, &fsNameBuf[0], uint32(len(fsNameBuf))); err != nil {
			// Query may fail for offline, unformatted, or network-disconnected drives;
			// gracefully fallback to NTFS rather than omitting the drive.
			result[cleanDrive] = "NTFS"
			continue
		}

		result[cleanDrive] = cmp.Or(windows.UTF16ToString(fsNameBuf), "NTFS")
	}
	return result
}
