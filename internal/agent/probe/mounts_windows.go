package probe

import (
	"path/filepath"
	"strings"
	"unsafe"

	pb "design-cloud-engine/internal/contract/proto"
	"golang.org/x/sys/windows"
)

// ResolveMountTargets returns the disk targets to probe on Windows.
// All fixed local drives are always included and cannot be disabled.
// Extra configured disk mounts are filtered by enabled and os ("windows" or empty).
func ResolveMountTargets(configured []*pb.MountTarget) []*pb.MountTarget {
	drives := fixedLogicalDrives()
	targets := make([]*pb.MountTarget, 0, len(drives)+len(configured))
	existingPaths := make(map[string]struct{})

	for _, drive := range drives {
		clean := strings.ToUpper(filepath.Clean(drive))
		existingPaths[clean] = struct{}{}
		targets = append(targets, &pb.MountTarget{
			Name:    strings.TrimRight(drive, `\`),
			Path:    drive,
			Enabled: true,
		})
	}

	for _, t := range configured {
		if t == nil || !t.Enabled {
			continue
		}
		if t.Os != "" && t.Os != "windows" {
			continue
		}
		clean := strings.ToUpper(filepath.Clean(t.Path))
		if _, exists := existingPaths[clean]; exists {
			continue
		}
		targets = append(targets, t)
	}
	return targets
}

// fixedLogicalDrives enumerates mounted fixed local volumes ("C:\", "D:\", ...).
func fixedLogicalDrives() []string {
	buf := make([]uint16, 256)
	n, _, _ := procGetLogicalDriveStrings.Call(uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	if n == 0 {
		return nil
	}

	var drives []string
	for _, raw := range strings.Split(windows.UTF16ToString(buf[:n]), "\x00") {
		drive := strings.TrimSpace(raw)
		if drive == "" {
			continue
		}
		root, err := windows.UTF16PtrFromString(drive)
		if err != nil {
			continue
		}
		if windows.GetDriveType(root) != windows.DRIVE_FIXED {
			continue
		}
		drives = append(drives, drive)
	}
	return drives
}
