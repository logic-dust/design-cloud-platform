//go:build !linux && !windows

package probe

func detectSysInfo() SysInfo {
	return SysInfo{}
}
