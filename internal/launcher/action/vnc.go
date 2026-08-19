//go:build !windows

package action

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

var fullControlOTPPattern = regexp.MustCompile(`(?m)^\s*Full control one-time password:\s*(\S+)\s*$`)
var vncOTPLinePattern = regexp.MustCompile(`(?m)^(\s*Full control one-time password:\s*)\S+(\s*)$`)

// HandleVNCGenerateOTP runs TurboVNC vncpasswd as the already-dropped target
// user and returns the one-time password emitted for the requested display.
// TurboVNC may print the OTP line on stderr, so stdout and stderr are parsed
// together; error messages redact any OTP-like line before returning.
func HandleVNCGenerateOTP(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.VNCGenerateOTP](req)
	if err != nil {
		return "", fmt.Errorf("vnc generate otp: bad payload: %w", err)
	}
	if p.ConnectionID == 0 {
		return "", fmt.Errorf("vnc generate otp: connectionId is required")
	}
	display, err := normalizeVNCDisplay(p.Display)
	if err != nil {
		return "", fmt.Errorf("vnc generate otp: %w", err)
	}
	if err := validateVNCPasswdPath(p.VNCPasswdPath); err != nil {
		return "", fmt.Errorf("vnc generate otp: %w", err)
	}

	var combined bytes.Buffer
	cmd := exec.CommandContext(ctx, p.VNCPasswdPath, "-o", "-display", display)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LC_ALL=C",
	}
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		msg := strings.TrimSpace(redactVNCOTPOutput(combined.String()))
		if msg == "" {
			return "", fmt.Errorf("run vncpasswd -o: %w", err)
		}
		return "", fmt.Errorf("run vncpasswd -o: %w: %s", err, msg)
	}

	otp, err := parseFullControlOTP(combined.String())
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(payload.VNCGenerateOTPResult{OTP: otp})
	if err != nil {
		return "", fmt.Errorf("marshal vnc otp result: %w", err)
	}
	return string(out), nil
}

func validateVNCPasswdPath(path string) error {
	if path == "" {
		return fmt.Errorf("vncpasswdPath is required")
	}
	if strings.ContainsAny(path, "\r\n\t") {
		return fmt.Errorf("vncpasswdPath contains control characters")
	}
	if !filepath.IsAbs(path) && path != "vncpasswd" {
		return fmt.Errorf("vncpasswdPath must be 'vncpasswd' or an absolute path")
	}
	return nil
}

func normalizeVNCDisplay(raw string) (string, error) {
	display := strings.TrimSpace(raw)
	if display == "" {
		return "", fmt.Errorf("display is required")
	}
	display = strings.TrimPrefix(display, ":")
	if display == "" {
		return "", fmt.Errorf("display number is required")
	}
	for _, r := range display {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("display must be a numeric VNC display")
		}
	}
	return ":" + display, nil
}

func parseFullControlOTP(output string) (string, error) {
	matches := fullControlOTPPattern.FindStringSubmatch(output)
	if len(matches) != 2 {
		return "", fmt.Errorf("vncpasswd output did not contain a full-control one-time password")
	}
	otp := strings.TrimSpace(matches[1])
	if otp == "" || strings.ContainsAny(otp, "\r\n\t ") {
		return "", fmt.Errorf("vncpasswd output contained an invalid one-time password")
	}
	return otp, nil
}

func redactVNCOTPOutput(output string) string {
	return vncOTPLinePattern.ReplaceAllString(output, "${1}[redacted]${2}")
}

// HandleVNCControl runs vncserver to start or stop a VNC server as the
// already-dropped target user. Operation must be "start" or "stop".
func HandleVNCControl(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	return executeVNC(ctx, req, false)
}

// HandleVNCCgroupControl runs vncserver under a systemd-run transient scope
// inside user-<UID>.slice, combined with `runuser --login` to initialize a complete
// user login environment (home directory, login profile, D-Bus session, and environment variables)
// while ensuring all spawned processes are automatically tracked within the user's cgroup slice.
func HandleVNCCgroupControl(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	return executeVNC(ctx, req, true)
}

func executeVNC(ctx context.Context, req *execution.ExecuteRequest, useCgroup bool) (string, error) {
	label := "vnc control"
	if useCgroup {
		label = "vnc cgroup control"
	}

	targetUser := strings.TrimSpace(req.TargetUser)
	var sliceName string
	var targetHome string
	if useCgroup {
		if targetUser == "" {
			return "", fmt.Errorf("%s: targetUser is required", label)
		}
		if targetUser == "root" || targetUser == "nobody" || strings.HasPrefix(targetUser, "-") || strings.ContainsRune(targetUser, 0) {
			return "", fmt.Errorf("%s: targetUser %q is not permitted", label, targetUser)
		}
		u, err := user.Lookup(targetUser)
		if err != nil {
			return "", fmt.Errorf("%s: lookup targetUser %q: %w", label, targetUser, err)
		}
		uid, err := strconv.Atoi(u.Uid)
		if err != nil || uid < 1000 {
			return "", fmt.Errorf("%s: targetUser %q (uid %s) is not permitted", label, targetUser, u.Uid)
		}
		sliceName = fmt.Sprintf("user-%s.slice", u.Uid)
		targetHome = u.HomeDir
	}

	p, err := execution.GetPayload[payload.VNCControl](req)
	if err != nil {
		return "", fmt.Errorf("%s: bad payload: %w", label, err)
	}
	if p.ConnectionID == 0 {
		return "", fmt.Errorf("%s: connectionId is required", label)
	}
	display, err := normalizeVNCDisplay(p.Display)
	if err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}
	serverPath := cmp.Or(strings.TrimSpace(p.VNCServerPath), "vncserver")

	var args []string
	switch p.Operation {
	case "start":
		// startOptions 是原始命令行字符串（服务端已同步按空白拆分为 serverArgs）。
		// 优先消费已拆分的 serverArgs；为空时回退按空白拆分 startOptions，
		// 使 startOptions 字段本身成为可用的命令行参数来源。
		startArgs := p.ServerArgs
		if len(startArgs) == 0 {
			if opts := strings.TrimSpace(p.StartOptions); opts != "" {
				startArgs = strings.Fields(opts)
			}
		}
		args = append(args, startArgs...)
		args = append(args, display)
	case "stop":
		args = []string{"-kill", display}
	default:
		return "", fmt.Errorf("%s: unsupported operation %q", label, p.Operation)
	}

	prog := serverPath
	workDir := ""
	if useCgroup {
		// 外层 systemd-run 建立 Cgroup Slice 容器约束；
		// 内层 runuser --login 完整模拟用户登录（加载 ~/.bashrc、/etc/profile.d、家目录与 D-Bus 会话），
		// 彻底杜绝纯 systemd-run 缺少 $HOME 导致的桌面黑屏与权限错误。
		cmdParts := append([]string{serverPath}, args...)
		quotedParts := make([]string, len(cmdParts))
		for i, part := range cmdParts {
			quotedParts[i] = quoteShellArg(part)
		}
		shellCmd := strings.Join(quotedParts, " ")

		prog = "systemd-run"
		args = []string{
			"--scope",
			fmt.Sprintf("--slice=%s", sliceName),
			"runuser",
			"--login",
			targetUser,
			"--session-command",
			shellCmd,
		}
		if targetHome != "" {
			workDir = targetHome
		}
	}

	_, err = runCommandWithDir(ctx, nil, workDir, prog, "", args...)
	if err != nil {
		prefix := "vnc server"
		if useCgroup {
			prefix = "vnc cgroup server"
		}
		return "", fmt.Errorf("%s %s: %w", prefix, p.Operation, err)
	}

	resultJSON, err := json.Marshal(payload.VNCControlResult{})
	if err != nil {
		return "", fmt.Errorf("marshal %s result: %w", label, err)
	}
	return string(resultJSON), nil
}

// quoteShellArg wraps an argument in single quotes, safely escaping any internal single quotes.
func quoteShellArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
