//go:build !windows

package action

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"design-cloud-engine/internal/platform/process"
)

var (
	// recursiveBlock: 强力拦截模式（优先级最高）
	// 该列表中的目录及其下属所有子文件/子孙目录均严禁操作。
	// 主要用于保护核心系统地带（如 /etc/passwd, /bin/ls 等）。
	recursiveBlock = []string{
		"/boot", "/dev", "/etc", "/proc", "/sys", "/root",
		"/bin", "/sbin", "/lib", "/lib64", "/usr", "/run",
		"/lost+found", "/swap",
	}

	// exactBlock: 精确拦截模式（优先级第二）
	// 仅禁止操作目录本身（如防止 rm -rf /），但允许在其子目录下进行正常操作。
	// 这里的 "/" 即使开放子目录，也会被上面的 recursiveBlock (如 /etc) 进一步拦截敏感路径。
	exactBlock = []string{
		"/", "/media", "/srv", "/mnt",
		"/home", "/opt", "/var", "/tmp",
	}
)

// ValidatePath ensures a path is absolute, has no control chars, and is not a blacklisted system path.
// Note: This serves as an application-level "Defense-in-Depth" layer. While the OS-level
// permission system (DAC) is the primary defense, this check prevents sensitive system
// path operations regardless of the OS user context.
func ValidatePath(label, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", label)
	}
	if !filepath.IsAbs(value) {
		return fmt.Errorf("%s must be an absolute path", label)
	}
	if strings.ContainsAny(value, "\r\n\t") {
		return fmt.Errorf("%s cannot contain control characters", label)
	}

	cleanPath := filepath.Clean(value)

	// Resolve existing parent symlinks too: a create target itself does not exist,
	// but one of its ancestors may still redirect it into a protected tree.
	resolvedPath, err := resolveExistingPathPrefix(cleanPath)
	if err != nil {
		return fmt.Errorf("%s cannot be safely resolved: %w", label, err)
	}
	cleanPath = resolvedPath

	// 1. 检查递归黑名单 (目录及其子文件均被拦截)
	for _, blocked := range recursiveBlock {
		// 使用 strings.HasPrefix 配合路径分隔符，高效判断子路径关系
		// cleanPath == blocked 处理精确匹配
		// strings.HasPrefix(...) 处理子目录匹配
		if cleanPath == blocked || strings.HasPrefix(cleanPath, blocked+string(filepath.Separator)) {
			return fmt.Errorf("%s is a protected system path or subpath: %s", label, blocked)
		}
	}

	// 2. 检查精确黑名单 (仅拦截根目录，允许子文件操作)
	if slices.Contains(exactBlock, cleanPath) {
		return fmt.Errorf("%s is a protected system directory: %s", label, cleanPath)
	}

	return nil
}

func resolveExistingPathPrefix(path string) (string, error) {
	candidate := path
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			for _, s := range slices.Backward(suffix) {
				resolved = filepath.Join(resolved, s)
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}

		// A dangling symlink cannot be canonicalized. Reject it instead of
		// validating its lexical location and then following it during the action.
		if info, lstatErr := os.Lstat(candidate); lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("unresolved symbolic link %q", candidate)
		} else if lstatErr != nil && !errors.Is(lstatErr, os.ErrNotExist) {
			return "", lstatErr
		}

		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", err
		}
		suffix = append(suffix, filepath.Base(candidate))
		candidate = parent
	}
}

// runCommand 在安全的 PATH 和极简的环境变量下执行命令。
// 它返回修剪后的 stdout 输出。stderr 会被捕获用于错误报告。
// envs 参数允许指定额外的环境变量（格式为 "KEY=VALUE"），调用者使用 validatePath 对传入参数中的文件路径进行校验。
// 如果 input 不为空，它将被通过 stdin 管道传递给命令。
// execSafePath 是 PrivilegedExec 子进程使用的固定 PATH。
const execSafePath = "/opt/TurboVNC/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// bashPath is the absolute bash used for login-shell wrappers.
// Hard-coded so PATH cannot redirect the interpreter.
const dcexecBashPath = "/bin/bash"

// Privileged helper binaries (RHEL/CentOS FHS layout).
const (
	dcexecChpasswdPath   = "/usr/sbin/chpasswd"
	dcexecSystemdRunPath = "/usr/bin/systemd-run"
	dcexecRunuserPath    = "/usr/sbin/runuser"
)

func runCommand(ctx context.Context, envs []string, name string, input string, args ...string) (string, error) {
	return runCommandWithDir(ctx, envs, "", name, input, args...)
}

func runCommandWithDir(ctx context.Context, envs []string, dir string, name string, input string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}

	// 基础安全环境：强制锁定 PATH 和 LC_ALL
	baseEnv := []string{
		"PATH=" + execSafePath,
		"LC_ALL=C",
	}

	cmd.Env = append(baseEnv, envs...)
	if dir != "" {
		cmd.Dir = dir
	}

	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}

	stdout := process.NewBoundedBuffer(process.DefaultMaxCommandOutputBytes)
	stderr := process.NewBoundedBuffer(process.DefaultMaxCommandOutputBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		errMsg := cmp.Or(strings.TrimSpace(stderr.String()), err.Error())
		return "", fmt.Errorf("%s failed: %s", name, errMsg)
	}

	return strings.TrimSpace(stdout.String()), nil
}
