//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"

	"design-cloud-engine/internal/dcexec"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/platform/osuser"
)

const (
	minUID         = 1000 // Minimum UID for non-RunAs users
	defaultTimeout = 30   // Default: 30 seconds
	maxTimeout     = 60   // Maximum: 60 seconds (NOT for long-running tasks)
)

var (
	resolveIdentityFn = osuser.ResolveUserIdentity
	setGroupsFn       = syscall.Setgroups
	setGIDFn          = syscall.Setgid
	setUIDFn          = syscall.Setuid
	setEnvFn          = os.Setenv
	geteuidFn         = os.Geteuid
	applySeccompFn    = applySeccompFilter
	applyNoNewPrivsFn = applyNoNewPrivs
	clearCapsFn       = clearCapabilities
	lockOSThreadFn    = runtime.LockOSThread
)

// execute resolves the identity mode from execution.Actions and switches
// to the appropriate execution identity before invoking the PrivilegedExec handler.
func execute(req *execution.ExecuteRequest, handler dcexec.Handler, spec execution.ActionSpec) (string, error) {
	ctx, cancel, err := prepareExecution(req, spec, true)
	if err != nil {
		return "", err
	}
	defer cancel()

	return handler(ctx, req)
}

func prepareExecution(req *execution.ExecuteRequest, spec execution.ActionSpec, withTimeout bool) (context.Context, context.CancelFunc, error) {
	if spec.PrivilegedExec == nil {
		return nil, nil, fmt.Errorf("PrivilegedExec action %s has no PrivilegedExec spec", req.ExecutionID)
	}

	// 0. --- 锁定 OS 线程（不解锁）---
	// capability、no_new_privs 与 seccomp 都是内核 task（线程）属性。身份切换前锁定，
	// 使后续每一步与 handler 中 exec 子进程都发生在同一线程上；goroutine 退出前不解锁，
	// 该线程随进程结束。全线程同步由各步骤自身保证（见 clearCapabilities / applyNoNewPrivs /
	// applySeccompFilter）。
	lockOSThreadFn()

	// 1. --- 确定执行用户
	var execUser string

	switch spec.PrivilegedExec.Privilege {
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
		return nil, nil, fmt.Errorf("PrivilegedExec action %s has unsupported privilege mode %q", req.ExecutionID, spec.PrivilegedExec.Privilege)
	}

	// 2. --- 获取执行用户的身份（仅非特权 action）
	//
	// 非特权 action 需要降权到目标用户执行。
	// 通过 `id` 和 `getent` 命令查询身份（支持 LDAP/sssd 缓存）。
	// 特权 action 以 root 身份执行，无需切换。

	var identity osuser.UserIdentity
	switch spec.PrivilegedExec.Privilege {
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

		// 3. --- 切换执行身份（降权）并清空能力集 ---
		// 服务账号（非 0 UID）带 file capability 切换到另一个非 0 UID 时，内核不会清空
		// CAP_SETUID/CAP_SETGID，必须显式清空并校验，否则中止。
		if err := dropPrivileges(identity); err != nil {
			return nil, nil, err
		}
		if err := clearCapsFn(); err != nil {
			return nil, nil, fmt.Errorf("failed to clear capabilities for action %s: %w", req.ExecutionID, err)
		}
	case execution.PrivilegeRoot:
		if err := elevateToRoot(); err != nil {
			return nil, nil, err
		}
	}

	// 4. --- PR_SET_NO_NEW_PRIVS + seccomp（L4）---
	// 身份切换完成后按 PrivilegedExecSpec 设置：零值即启用保护，任一步失败都中止（fail-closed）。
	// PrivilegedExecSpec.Validate 保证关闭 NNP 的 action 同时关闭 seccomp。
	if !spec.PrivilegedExec.DisableNoNewPrivs {
		if err := applyNoNewPrivsFn(spec.PrivilegedExec); err != nil {
			return nil, nil, fmt.Errorf("failed to apply no-new-privs for action %s: %w", req.ExecutionID, err)
		}
	}
	if !spec.PrivilegedExec.DisableSeccomp {
		if err := applySeccompFn(spec.PrivilegedExec); err != nil {
			return nil, nil, fmt.Errorf("failed to apply seccomp for action %s: %w", req.ExecutionID, err)
		}
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

	if spec.PrivilegedExec.Privilege == execution.PrivilegeTargetUser {
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

// elevateToRoot switches the process to UID/GID 0 using file capabilities.
// Order: supplementary groups, GID, then UID. Any failure aborts the action.
func elevateToRoot() error {
	if err := setGroupsFn(nil); err != nil {
		return fmt.Errorf("failed to reset supplementary groups for root execution: %w", err)
	}
	if err := setGIDFn(0); err != nil {
		return fmt.Errorf("failed to set GID to root: %w", err)
	}
	if err := setUIDFn(0); err != nil {
		return fmt.Errorf("failed to set UID to root: %w", err)
	}
	if geteuidFn() != 0 {
		return fmt.Errorf("privilege elevation to root failed: effective UID is still %d", geteuidFn())
	}
	return nil
}

// dropPrivileges sets process GID, supplementary groups, and UID to the given identity.
// Must be called before performing any file I/O on behalf of the target user.
// Go's syscall.Setgroups/Setgid/Setuid apply to every thread of the process.
func dropPrivileges(identity osuser.UserIdentity) error {
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
