//go:build !windows

package action

import (
	"cmp"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"design-cloud-engine/internal/common"
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

	// 解析符号链接：若路径存在则取其真实路径再做黑名单检查，防止符号链接绕过（如 /proc/self/root/etc → /etc）。
	if resolved, err := filepath.EvalSymlinks(cleanPath); err == nil {
		cleanPath = resolved
	}

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

// runCommand 在安全的 PATH 和极简的环境变量下执行命令。
// 它返回修剪后的 stdout 输出。stderr 会被捕获用于错误报告。
// envs 参数允许指定额外的环境变量（格式为 "KEY=VALUE"），调用者使用 validatePath 对传入参数中的文件路径进行校验。
// 如果 input 不为空，它将被通过 stdin 管道传递给命令。
func runCommand(ctx context.Context, envs []string, name string, input string, args ...string) (string, error) {
	return runCommandWithDir(ctx, envs, "", name, input, args...)
}

func runCommandWithDir(ctx context.Context, envs []string, dir string, name string, input string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// 基础安全环境：强制锁定 PATH 和 LC_ALL
	baseEnv := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LC_ALL=C",
	}

	cmd.Env = append(baseEnv, envs...)
	if dir != "" {
		cmd.Dir = dir
	}

	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}

	stdout := common.NewBoundedBuffer(common.DefaultMaxCommandOutputBytes)
	stderr := common.NewBoundedBuffer(common.DefaultMaxCommandOutputBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		errMsg := cmp.Or(strings.TrimSpace(stderr.String()), err.Error())
		return "", fmt.Errorf("%s failed: %s", name, errMsg)
	}

	return strings.TrimSpace(stdout.String()), nil
}
