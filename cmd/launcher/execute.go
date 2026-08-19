//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/launcher"
)

const (
	minUID         = 1000 // Minimum UID for non-RunAs users
	defaultTimeout = 30   // Default: 30 seconds
	maxTimeout     = 60   // Maximum: 60 seconds (NOT for long-running tasks)
)

var (
	resolveIdentityFn = common.ResolveUserIdentity
	setGroupsFn       = syscall.Setgroups
	setGIDFn          = syscall.Setgid
	setUIDFn          = syscall.Setuid
	setEnvFn          = os.Setenv
	geteuidFn         = os.Geteuid
	applySeccompFn    = applySeccompFilter
	applyNoNewPrivsFn = applyNoNewPrivs
)

// execute resolves the identity mode from execution.Actions and switches
// to the appropriate execution identity before invoking the launcher handler.
func execute(req *execution.ExecuteRequest, handler launcher.Handler, spec execution.ActionSpec) (string, error) {
	ctx, cancel, err := prepareExecution(req, spec, true)
	if err != nil {
		return "", err
	}
	defer cancel()

	return handler(ctx, req)
}

func prepareExecution(req *execution.ExecuteRequest, spec execution.ActionSpec, withTimeout bool) (context.Context, context.CancelFunc, error) {
	if spec.Launcher == nil {
		return nil, nil, fmt.Errorf("launcher action %s has no launcher spec", req.ExecutionID)
	}

	// 1. --- 确定执行用户
	var execUser string

	switch spec.Launcher.Privilege {
	case execution.PrivilegeRoot:
		execUser = "root"
	case execution.PrivilegeTargetUser:
		// Non-privileged actions must execute as the requested target user
		if req.TargetUser == "" {
			return nil, nil, fmt.Errorf("execution user not specified for action %s", req.ExecutionID)
		}
		// Explicitly block root execution for non-privileged actions to prevent privilege escalation bypass
		if req.TargetUser == "root" || req.TargetUser == "nobody" {
			return nil, nil, fmt.Errorf("action %s is not authorized to run as %s", req.ExecutionID, req.TargetUser)
		}
		execUser = req.TargetUser
	default:
		return nil, nil, fmt.Errorf("launcher action %s has unsupported privilege mode %q", req.ExecutionID, spec.Launcher.Privilege)
	}

	// 2. --- 获取执行用户的身份（仅非特权 action）
	//
	// 非特权 action 需要降权到目标用户执行。
	// 通过 `id` 和 `getent` 命令查询身份（支持 LDAP/sssd 缓存）。
	// 特权 action 以 root 身份执行，无需切换。

	var identity common.UserIdentity
	switch spec.Launcher.Privilege {
	case execution.PrivilegeTargetUser:
		// 查询用户身份
		var err error
		identity, err = resolveIdentityFn(execUser)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve identity for user %s: %w", execUser, err)
		}

		// 安全验证：UID 必须 >= minUID
		if identity.UID < minUID {
			return nil, nil, fmt.Errorf("execution as user %s (uid %d < %d) is not permitted", execUser, identity.UID, minUID)
		}

		// 3. --- 切换执行身份（降权）---
		if err := dropPrivileges(identity); err != nil {
			return nil, nil, err
		}
	case execution.PrivilegeRoot:
		// 仅在当前不是 root 时尝试切换。在非 setuid 开发环境下，如果当前不是 root 且尝试切换会失败。
		if geteuidFn() != 0 {
			// 在测试/开发环境下（非 root 运行），我们无法切换到 root。
			// 如果当前是普通用户，直接执行可能会导致权限不足，但在集成测试中这是预期的。
			_, _ = fmt.Fprintf(os.Stderr, "Security warning: launcher is not running as root, skipping privilege elevation\n")
		} else {
			// 显式提升到 root。仅依赖 Privileged 语义而不实际切换 UID/GID 是不安全且不正确的。
			if err := setGroupsFn(nil); err != nil {
				return nil, nil, fmt.Errorf("failed to reset supplementary groups for root execution: %w", err)
			}
			if err := setGIDFn(0); err != nil {
				return nil, nil, fmt.Errorf("failed to set GID to root: %w", err)
			}
			if err := setUIDFn(0); err != nil {
				return nil, nil, fmt.Errorf("failed to set UID to root: %w", err)
			}
		}
	}

	// 4. --- 应用 seccomp 过滤器（L4：系统调用隔离）---
	// 必须在 setuid/setgid 完成之后应用，以便过滤器作用于降权后的进程。
	// L4 提供内核层防御，限制进程能执行的系统调用，防止：
	//   - 代码注入漏洞利用
	//   - 依赖库漏洞利用
	//   - Handler 逻辑漏洞利用
	//   - 特权操作滥用（如 ptrace、mount、reboot）
	// 对不支持的平台（非 Linux）为 no-op。
	// 失败时只打印警告日志，不中断执行（L1-L3 安全层仍然有效）。
	if spec.Launcher.EnableSeccomp {
		applySeccompFn(spec.Launcher)
	} else if !applyNoNewPrivsFn(spec.Launcher) {
		return nil, nil, fmt.Errorf("failed to apply no-new-privs for action %s", req.ExecutionID)
	}

	ctx := context.Background()
	cancel := func() {}

	// 5. --- 设置执行超时 ---
	// 本程序不应该设计为运行长时间任务！

	if withTimeout {
		timeout := defaultTimeout * time.Second
		// 如果 req.Timeout <= 0，直接使用默认值 (30s)
		// 如果 > 0，则使用请求值，但自动截断到最大值 (60s)
		if req.Timeout > 0 {
			requestedTimeout := min(req.Timeout,
				// 自动截断到最大值
				maxTimeout)
			timeout = time.Duration(requestedTimeout) * time.Second
		}
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	}

	// 6. --- 应用环境变量 ---
	// 优先应用由系统环境拉取到的基本环境，随后允许请求 env 覆盖补充。
	// 对 non-privileged action 应用用户相关 env。
	if req.TargetUser != "" {
		_ = setEnvFn("TARGET_USER", req.TargetUser)
	}

	if spec.Launcher.Privilege == execution.PrivilegeTargetUser {
		_ = setEnvFn("USER", execUser)
		_ = setEnvFn("LOGNAME", execUser)
		if identity.Home != "" {
			_ = setEnvFn("HOME", identity.Home)
		}
		if identity.Shell != "" {
			_ = setEnvFn("SHELL", identity.Shell)
		}

		for k, v := range req.Env {
			if execution.IsValidEnvKey(k) && !execution.IsBlockedEnvKey(k) {
				_ = setEnvFn(k, v)
			}
		}
	}

	return ctx, cancel, nil
}

// dropPrivileges sets process GID, supplementary groups, and UID to the given identity.
// Must be called before performing any file I/O on behalf of the target user.
func dropPrivileges(identity common.UserIdentity) error {
	if err := setGroupsFn(identity.GIDs); err != nil {
		return fmt.Errorf("failed to set supplementary groups: %w", err)
	}
	if err := setGIDFn(identity.GID); err != nil {
		return fmt.Errorf("failed to set GID to %d: %w", identity.GID, err)
	}
	if err := setUIDFn(identity.UID); err != nil {
		return fmt.Errorf("failed to set UID to %d: %w", identity.UID, err)
	}
	return nil
}
