//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	"design-cloud-engine/internal/execution"
)

// 线程模型：capability 与 no_new_privs 都是内核 task（线程）属性，prctl/capset 只作用于
// 调用线程。PrivilegedExec 以 CGO_ENABLED=0 构建，借助 syscall.AllThreadsSyscall 在 Go 运行时的
// 全部线程上执行同一系统调用；cgo 构建下该函数返回 ENOTSUP，此时直接报错中止（fail-closed）。
// seccomp 过滤器由 applySeccompFilter 以 SECCOMP_FILTER_FLAG_TSYNC 同步到全部线程。

var (
	allThreadsSyscallFn = syscall.AllThreadsSyscall
	clearAmbientCapsFn  = clearAmbientCapabilities
	readProcStatusFn    = os.ReadFile
	gettidFn            = unix.Gettid
)

// capset 参数放在包级变量中：全局变量不会随 goroutine 栈迁移，传给 AllThreadsSyscall 的地址
// 在各线程执行期间始终有效。PrivilegedExec 每个进程只执行一个 action，不存在并发调用。
var (
	capsetHeader = unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	capsetData   [2]unix.CapUserData
)

// applyNoNewPrivs 在全部线程上设置 PR_SET_NO_NEW_PRIVS。
func applyNoNewPrivs(_ *execution.PrivilegedExecSpec) error {
	if _, _, errno := allThreadsSyscallFn(unix.SYS_PRCTL, unix.PR_SET_NO_NEW_PRIVS, 1, 0); errno != 0 {
		return fmt.Errorf("prctl(PR_SET_NO_NEW_PRIVS) on all threads: %w", errno)
	}
	return nil
}

// clearCapabilities 在全部线程上清空 permitted/effective/inheritable 能力集与 ambient 集，
// 随后读取 /proc 校验进程主线程与当前线程的能力集均为空，否则返回错误。
func clearCapabilities() error {
	capsetHeader = unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	capsetData = [2]unix.CapUserData{}
	if _, _, errno := allThreadsSyscallFn(unix.SYS_CAPSET,
		uintptr(unsafe.Pointer(&capsetHeader)), uintptr(unsafe.Pointer(&capsetData[0])), 0); errno != 0 {
		return fmt.Errorf("capset on all threads: %w", errno)
	}
	if err := clearAmbientCapsFn(); err != nil {
		return err
	}
	return verifyNoCapabilities()
}

// clearAmbientCapabilities 清空 ambient 能力集。ambient 是 permitted∩inheritable 的子集，
// 上一步清空后已随之为空；不支持 ambient 的内核（< 4.3）返回 EINVAL，视为无 ambient 集。
func clearAmbientCapabilities() error {
	_, _, errno := allThreadsSyscallFn(unix.SYS_PRCTL, unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0)
	if errno != 0 && !errors.Is(errno, unix.EINVAL) {
		return fmt.Errorf("prctl(PR_CAP_AMBIENT_CLEAR_ALL) on all threads: %w", errno)
	}
	return nil
}

// verifyNoCapabilities 校验 /proc/self/status（主线程）与 /proc/self/task/<tid>/status（当前线程）
// 的 CapPrm/CapEff/CapInh/CapAmb 均为 0。不依赖 /proc/thread-self（Linux 3.17 起才有）。
func verifyNoCapabilities() error {
	paths := []string{
		"/proc/self/status",
		"/proc/self/task/" + strconv.Itoa(gettidFn()) + "/status",
	}
	for _, path := range paths {
		data, err := readProcStatusFn(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if err := checkCapabilityFieldsZero(string(data)); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}

// checkCapabilityFieldsZero 要求 CapPrm/CapEff/CapInh 存在且为 0；CapAmb 存在时也须为 0。
func checkCapabilityFieldsZero(status string) error {
	required := map[string]bool{"CapPrm": false, "CapEff": false, "CapInh": false}
	for line := range strings.SplitSeq(status, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		_, isRequired := required[name]
		if !isRequired && name != "CapAmb" {
			continue
		}
		mask, err := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		if err != nil {
			return fmt.Errorf("parse %s %q: %w", name, strings.TrimSpace(value), err)
		}
		if mask != 0 {
			return fmt.Errorf("%s=%#016x after clearing capabilities", name, mask)
		}
		if isRequired {
			required[name] = true
		}
	}
	for name, seen := range required {
		if !seen {
			return fmt.Errorf("%s missing from status", name)
		}
	}
	return nil
}
