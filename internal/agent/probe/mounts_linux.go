package probe

import (
	pb "design-cloud-engine/internal/contract/proto"
)

// ResolveMountTargets returns the disk targets to probe on Linux.
// OS root ("/") is always included and cannot be disabled.
// Extra configured disk mounts are filtered by enabled and os ("linux" or empty).
func ResolveMountTargets(configured []*pb.MountTarget) []*pb.MountTarget {
	targets := []*pb.MountTarget{
		{
			Name:    "root",
			Path:    "/",
			Enabled: true,
		},
	}
	for _, t := range configured {
		if t == nil || !t.Enabled {
			continue
		}
		if t.Os != "" && t.Os != "linux" {
			continue
		}
		if t.Path == "/" {
			continue
		}
		targets = append(targets, t)
	}
	return targets
}
