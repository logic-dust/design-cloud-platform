package probe

import (
	"cmp"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetSystemTimes       = kernel32Dll.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx = kernel32Dll.NewProc("GlobalMemoryStatusEx")
	procGetTickCount64       = kernel32Dll.NewProc("GetTickCount64")
)

func readCPUStat() (idle uint64, total uint64, err error) {
	var idleTime, kernelTime, userTime windows.Filetime
	r1, _, errSys := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idleTime)),
		uintptr(unsafe.Pointer(&kernelTime)),
		uintptr(unsafe.Pointer(&userTime)),
	)
	if r1 == 0 {
		return 0, 0, errSys
	}
	idleTicks := uint64(idleTime.HighDateTime)<<32 | uint64(idleTime.LowDateTime)
	kernelTicks := uint64(kernelTime.HighDateTime)<<32 | uint64(kernelTime.LowDateTime)
	userTicks := uint64(userTime.HighDateTime)<<32 | uint64(userTime.LowDateTime)
	return idleTicks, kernelTicks + userTicks, nil
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func readMemInfo() (map[string]int64, error) {
	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	r1, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	if r1 == 0 {
		return nil, err
	}
	return map[string]int64{
		"MemTotal":     int64(ms.TotalPhys / 1024),
		"MemAvailable": int64(ms.AvailPhys / 1024),
		"SwapTotal":    0,
		"SwapFree":     0,
	}, nil
}

// readLoadAvg returns 0 on Windows as Windows kernel does not track Unix load average.
func readLoadAvg() (float64, float64, float64, error) {
	return 0, 0, 0, nil
}

func readUptimeSeconds() (int64, error) {
	r1, _, _ := procGetTickCount64.Call()
	return int64(r1) / 1000, nil
}

// findNvidiaSmi returns the path to nvidia-smi.exe on Windows using only trusted absolute
// paths rooted at the OS-provided ProgramFiles/SystemRoot directories.
// exec.LookPath is intentionally avoided (CWE-426: PATH hijack).
//
// Canonical NVSMI locations on Windows:
//   - %ProgramFiles%\NVIDIA Corporation\NVSMI\   (standard NVSMI package)
//   - %SystemRoot%\System32\                     (driver-bundled copy, rare)
//   - %ProgramFiles%\NVIDIA Corporation\CUDA\bin\ (CUDA Toolkit co-install)
func nvidiaSmiCandidates() []string {
	programFiles := cmp.Or(os.Getenv("ProgramFiles"), `C:\Program Files`)
	systemRoot := cmp.Or(os.Getenv("SystemRoot"), `C:\Windows`)

	return []string{
		filepath.Join(programFiles, "NVIDIA Corporation", "NVSMI", "nvidia-smi.exe"),
		filepath.Join(systemRoot, "System32", "nvidia-smi.exe"),
		filepath.Join(programFiles, "NVIDIA Corporation", "CUDA", "bin", "nvidia-smi.exe"),
	}
}

func findNvidiaSmi() string {
	for _, path := range nvidiaSmiCandidates() {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
