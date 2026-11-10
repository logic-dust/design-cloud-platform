//go:build !windows

package action

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
	"design-cloud-engine/internal/platform/osuser"
)

var fullControlOTPPattern = regexp.MustCompile(`(?m)^\s*Full control one-time password:\s*(\S+)\s*$`)
var vncOTPLinePattern = regexp.MustCompile(`(?m)^(\s*Full control one-time password:\s*)\S+(\s*)$`)

const (
	vncCredModeUser    = "user"
	vncCredModeManaged = "managed"
)

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
	// 直接以绝对路径执行 vncpasswd（不经 login shell）：用户 profile 无法篡改 PATH
	// 或伪造 OTP 输出。vncpasswd 经 X 协议连接目标 Xvnc，需要 HOME 定位
	// ~/.Xauthority；环境沿用固定 PATH + LC_ALL=C 的基础安全环境。
	cmd := exec.CommandContext(ctx, p.VNCPasswdPath, "-o", "-display", display)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = vncPasswdEnv()
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
	if !filepath.IsAbs(path) {
		return fmt.Errorf("vncpasswdPath must be an absolute path")
	}
	return nil
}

// vncPasswdEnv 构造 vncpasswd 的执行环境：基础安全环境（固定 PATH、LC_ALL=C）
// 加上降权后进程的 HOME/USER/LOGNAME，以及可选的 XAUTHORITY。
func vncPasswdEnv() []string {
	env := []string{
		"PATH=" + execSafePath,
		"LC_ALL=C",
	}
	for _, key := range []string{"HOME", "USER", "LOGNAME", "XAUTHORITY"} {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	return env
}

func vncPasswdEnvForUser(home, username string) []string {
	env := []string{
		"PATH=" + execSafePath,
		"LC_ALL=C",
	}
	if home != "" {
		env = append(env, "HOME="+home)
	}
	if username != "" {
		env = append(env, "USER="+username, "LOGNAME="+username)
	}
	if v := os.Getenv("XAUTHORITY"); v != "" {
		env = append(env, "XAUTHORITY="+v)
	}
	return env
}

func resolveVNCPasswdPath(serverPath, payloadPath string) string {
	if p := strings.TrimSpace(payloadPath); p != "" {
		return p
	}
	serverPath = strings.TrimSpace(serverPath)
	if serverPath == "" || !filepath.IsAbs(serverPath) {
		return ""
	}
	return filepath.Join(filepath.Dir(serverPath), "vncpasswd")
}

func vncPasswdFilePath(home string) string {
	return filepath.Join(home, ".vnc", "passwd")
}

func normalizeVNCPasswdMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", vncCredModeUser:
		return vncCredModeUser, nil
	case vncCredModeManaged:
		return vncCredModeManaged, nil
	default:
		return "", fmt.Errorf("unsupported vnc passwd mode %q", mode)
	}
}

// vncAuthTypesForArgs 解析 vncserver 的 -securitytypes；未显式指定时 TurboVNC
// 默认同时启用 VNC 密码与 OTP。返回最后一次出现的取值。
func vncAuthTypesForArgs(args []string) (hasVNC, hasOTP bool) {
	hasVNC, hasOTP = true, true
	for i := 0; i < len(args); i++ {
		lower := strings.ToLower(args[i])
		var value string
		switch {
		case lower == "-securitytypes" || lower == "--securitytypes":
			if i+1 >= len(args) {
				return false, false
			}
			value = args[i+1]
			i++
		case strings.HasPrefix(lower, "-securitytypes="):
			value = args[i][len("-securitytypes="):]
		case strings.HasPrefix(lower, "--securitytypes="):
			value = args[i][len("--securitytypes="):]
		default:
			continue
		}
		hasVNC, hasOTP = false, false
		for part := range strings.SplitSeq(value, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if strings.HasSuffix(part, "vnc") {
				hasVNC = true
			}
			if strings.HasSuffix(part, "otp") {
				hasOTP = true
			}
		}
	}
	return hasVNC, hasOTP
}

// stripVNCPasswdArgs 移除用户传入的 -rfbauth，避免 managed 模式被绕过或出现两份 -rfbauth。
// 其他疑似密码文件参数（如 -PasswordFile）不是 vncserver 的合法选项，保留让 vncserver 报错，
// 避免在 managed 模式下悄悄吞掉用户的配置错误。
func stripVNCPasswdArgs(args []string) []string {
	out := make([]string, 0, len(args))
	skipNext := false
	for _, token := range args {
		if skipNext {
			skipNext = false
			continue
		}
		lower := strings.ToLower(token)
		switch {
		case lower == "-rfbauth" || lower == "--rfbauth":
			skipNext = true
		case strings.HasPrefix(lower, "-rfbauth="), strings.HasPrefix(lower, "--rfbauth="):
		default:
			out = append(out, token)
		}
	}
	return out
}

func managedVNCPasswdPaths(home string, uid int, connectionID uint) []string {
	if connectionID == 0 {
		return nil
	}
	id := strconv.FormatUint(uint64(connectionID), 10)
	var paths []string
	if home != "" {
		paths = append(paths, filepath.Join(home, ".cache", "dc", "connection", id, "passwd"))
	}
	if uid > 0 {
		paths = append(paths, filepath.Join("/tmp", fmt.Sprintf(".dc-%d", uid), "connection", id, "passwd"))
	}
	return paths
}

func ensurePrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(dir)
		if err != nil {
			return err
		}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("managed vnc passwd dir %q is not a real directory", dir)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("managed vnc passwd dir %q is not owned by uid %d", dir, os.Getuid())
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func installVNCPasswdFile(ctx context.Context, passwdBin, home, username, passwdFile, plainPassword string) error {
	encrypted, err := runVNCPasswdF(ctx, passwdBin, home, username, false, plainPassword)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(passwdFile), 0o700); err != nil {
		return fmt.Errorf("create vnc passwd dir: %w", err)
	}
	tmp := passwdFile + ".tmp"
	if err := os.WriteFile(tmp, encrypted, 0o600); err != nil {
		return fmt.Errorf("write vnc passwd temp: %w", err)
	}
	if err := os.Rename(tmp, passwdFile); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install vnc passwd file: %w", err)
	}
	return nil
}

func prepareManagedVNCPasswd(ctx context.Context, passwdBin, home, username string, uid int, connectionID uint, asRunuser bool) (string, error) {
	if connectionID == 0 {
		return "", fmt.Errorf("connectionId is required for managed vnc passwd")
	}
	if err := validateVNCPasswdPath(passwdBin); err != nil {
		return "", err
	}
	paths := managedVNCPasswdPaths(home, uid, connectionID)
	if len(paths) == 0 {
		return "", fmt.Errorf("no writable location for managed vnc passwd")
	}
	var lastErr error
	for _, path := range paths {
		if !asRunuser {
			if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
				lastErr = err
				continue
			}
		}
		plain, err := randomVNCPassword()
		if err != nil {
			return "", err
		}
		if asRunuser {
			err = installVNCPasswdAsUser(ctx, passwdBin, username, path, plain)
		} else {
			err = installVNCPasswdFile(ctx, passwdBin, home, username, path, plain)
		}
		if err != nil {
			lastErr = err
			continue
		}
		return path, nil
	}
	return "", fmt.Errorf("install managed vnc passwd: %w", lastErr)
}

func removeManagedVNCPasswd(ctx context.Context, path, username string, asRunuser bool) error {
	if path == "" {
		return nil
	}
	if asRunuser {
		shellCmd := fmt.Sprintf("rm -f %s; rmdir %s 2>/dev/null || true",
			quoteShellArg(path), quoteShellArg(filepath.Dir(path)))
		cmd := exec.CommandContext(ctx, dcexecRunuserPath, "-u", username, "--", dcexecBashPath, "-c", shellCmd)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Env = vncPasswdEnvForUser("", username)
		cmd.WaitDelay = time.Second
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil && !errors.Is(err, exec.ErrWaitDelay) {
			return fmt.Errorf("remove managed vnc passwd as %q: %s", username, cmp.Or(strings.TrimSpace(stderr.String()), err.Error()))
		}
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(filepath.Dir(path)); err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTEMPTY) {
		return err
	}
	return nil
}

func cleanupManagedVNCPasswdPaths(ctx context.Context, paths []string, username string, asRunuser bool) {
	for _, path := range paths {
		_ = removeManagedVNCPasswd(ctx, path, username, asRunuser)
	}
}

func vncPasswdFileReady(ctx context.Context, path, username string, asRunuser bool) (bool, error) {
	if asRunuser {
		// Cgroup PrivilegedExec stays root without DAC_OVERRIDE; cannot stat 0700 $HOME/.vnc.
		cmd := exec.CommandContext(ctx, dcexecRunuserPath, "-u", username, "--", "/usr/bin/test", "-s", path)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Run(); err != nil {
			if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() == 1 {
				return false, nil
			}
			return false, fmt.Errorf("check vnc passwd as %q: %w", username, err)
		}
		return true, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("vnc passwd path %q is not a regular file", path)
	}
	return info.Size() > 0, nil
}

func randomVNCPassword() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 16
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate vnc password: %w", err)
	}
	for i, b := range buf {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf), nil
}

// ensureVNCPasswdFile creates ~/.vnc/passwd when missing so vncserver can start non-interactively.
// The password is not returned; clients use TurboVNC OTP or Proxy after the session is running.
func ensureVNCPasswdFile(ctx context.Context, passwdBin, home, username string, asRunuser bool) error {
	if home == "" {
		return fmt.Errorf("ensure vnc passwd: home directory is required")
	}
	if err := validateVNCPasswdPath(passwdBin); err != nil {
		return fmt.Errorf("ensure vnc passwd: %w", err)
	}
	passwdFile := vncPasswdFilePath(home)
	ready, err := vncPasswdFileReady(ctx, passwdFile, username, asRunuser)
	if err != nil {
		return err
	}
	if ready {
		return nil
	}

	plain, err := randomVNCPassword()
	if err != nil {
		return err
	}
	if asRunuser {
		return installVNCPasswdAsUser(ctx, passwdBin, username, passwdFile, plain)
	}
	return installVNCPasswdFile(ctx, passwdBin, home, username, passwdFile, plain)
}

// installVNCPasswdAsUser writes a passwd file entirely as the target user (cgroup / root PrivilegedExec).
func installVNCPasswdAsUser(ctx context.Context, passwdBin, username, passwdFile, plainPassword string) error {
	quotedBin := quoteShellArg(passwdBin)
	quotedDir := quoteShellArg(filepath.Dir(passwdFile))
	quotedFile := quoteShellArg(passwdFile)
	quotedTmp := quoteShellArg(passwdFile + ".tmp")
	shellCmd := fmt.Sprintf(
		"umask 077; d=%s; if [ -L \"$d\" ]; then echo 'refusing symlink vnc passwd dir' >&2; exit 1; fi; "+
			"mkdir -p \"$d\" && chmod 700 \"$d\" && %s -f > %s && mv %s %s",
		quotedDir, quotedBin, quotedTmp, quotedTmp, quotedFile,
	)
	cmd := exec.CommandContext(ctx, dcexecRunuserPath, "-u", username, "--", dcexecBashPath, "-c", shellCmd)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = vncPasswdEnvForUser("", username)
	cmd.Stdin = strings.NewReader(plainPassword + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		msg := cmp.Or(strings.TrimSpace(stderr.String()), err.Error())
		return fmt.Errorf("install vnc passwd as %q: %s", username, msg)
	}
	return nil
}

func runVNCPasswdF(ctx context.Context, passwdBin, home, username string, asRunuser bool, plainPassword string) ([]byte, error) {
	var cmd *exec.Cmd
	if asRunuser {
		cmd = exec.CommandContext(ctx, dcexecRunuserPath, "-u", username, "--", passwdBin, "-f")
	} else {
		cmd = exec.CommandContext(ctx, passwdBin, "-f")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = vncPasswdEnvForUser(home, username)
	cmd.Stdin = strings.NewReader(plainPassword + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		msg := cmp.Or(strings.TrimSpace(stderr.String()), err.Error())
		return nil, fmt.Errorf("vncpasswd -f: %s", msg)
	}
	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) == 0 {
		return nil, fmt.Errorf("vncpasswd -f: empty output")
	}
	return out, nil
}

func normalizeVNCDisplay(display *int) (string, error) {
	if display == nil {
		return "", fmt.Errorf("display is required")
	}
	if *display < 0 {
		return "", fmt.Errorf("display must be a non-negative integer")
	}
	return fmt.Sprintf(":%d", *display), nil
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

// buildVNCServerCommand 按当前权限上下文包装 vncserver 调用：
// Cgroup 模式经 systemd-run + runuser --login 执行；直接降权模式经 bash --login 执行。
func buildVNCServerCommand(serverPath string, vncArgs []string, targetUser, sliceName string, useCgroup bool) (prog string, args []string, envs []string, workDir string) {
	if useCgroup {
		// 外层 systemd-run 建立 Cgroup Slice 容器约束；
		// 内层 runuser --login 完整模拟用户登录（加载 ~/.bashrc、/etc/profile.d、家目录与 D-Bus 会话），
		// 彻底杜绝纯 systemd-run 缺少 $HOME 导致的桌面黑屏与权限错误。
		cmdParts := append([]string{serverPath}, vncArgs...)
		quotedParts := make([]string, len(cmdParts))
		for i, part := range cmdParts {
			quotedParts[i] = quoteShellArg(part)
		}
		shellCmd := strings.Join(quotedParts, " ")

		// Cgroup 模式由 runuser --login 自动切换用户环境并进入家目录；
		// PrivilegedExec 虽具有 CAP_SETUID 提升为 UID 0，但不具有 CAP_DAC_OVERRIDE，
		// 因此 systemd-run 保持在默认目录执行，严禁在外部预先 chdir 进入普通用户 0700 私有家目录。
		return dcexecSystemdRunPath, []string{
			"--scope",
			fmt.Sprintf("--slice=%s", sliceName),
			dcexecRunuserPath,
			"--login",
			targetUser,
			"--session-command",
			shellCmd,
		}, nil, ""
	}

	// 直接降权模式：此时 PrivilegedExec 已经通过 setuid/setgid 彻底成为 targetUser。
	// 通过 bash --login 执行：
	// 注入基本种子 HOME 与 USER，使 bash --login 能正确定位 ~/.bashrc 与执行 cd ~；
	// 随后 bash --login 会自动按序加载系统与用户的全套 profile 和模块环境，
	// 并通过 cd && exec 自动切入家目录并用 vncserver 替换自身（零中间 bash 进程残留）。
	if home := os.Getenv("HOME"); home != "" {
		workDir = home
		envs = append(envs, "HOME="+home)
	}
	if u := os.Getenv("USER"); u != "" {
		envs = append(envs, "USER="+u, "LOGNAME="+u)
	}

	cmdParts := append([]string{serverPath}, vncArgs...)
	quotedParts := make([]string, len(cmdParts))
	for i, part := range cmdParts {
		quotedParts[i] = quoteShellArg(part)
	}
	shellCmd := "cd && exec " + strings.Join(quotedParts, " ")

	return dcexecBashPath, []string{"--login", "-c", shellCmd}, envs, workDir
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
	targetUID := os.Getuid()
	if useCgroup {
		if targetUser == "" {
			return "", fmt.Errorf("%s: targetUser is required", label)
		}
		if targetUser == "root" || targetUser == "nobody" || strings.HasPrefix(targetUser, "-") || strings.ContainsRune(targetUser, 0) {
			return "", fmt.Errorf("%s: targetUser %q is not permitted", label, targetUser)
		}
		identity, err := osuser.ResolveUserIdentity(targetUser)
		if err != nil {
			return "", fmt.Errorf("%s: lookup targetUser %q: %w", label, targetUser, err)
		}
		if identity.UID < 1000 {
			return "", fmt.Errorf("%s: targetUser %q (uid %d) is not permitted", label, targetUser, identity.UID)
		}
		targetUID = identity.UID
		targetHome = identity.Home
		sliceName = fmt.Sprintf("user-%d.slice", identity.UID)
	} else {
		targetHome = os.Getenv("HOME")
		targetUser = cmp.Or(targetUser, os.Getenv("USER"))
	}

	p, err := execution.GetPayload[payload.VNCControl](req)
	if err != nil {
		return "", fmt.Errorf("%s: bad payload: %w", label, err)
	}
	passwdMode, err := normalizeVNCPasswdMode(p.PasswdMode)
	if err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}
	serverPath := cmp.Or(strings.TrimSpace(p.VNCServerPath), "vncserver")

	var managedPasswdFile string
	var args []string
	var chosenDisplay int
	var haveChosen bool

	switch p.Operation {
	case "start":
		startArgs := p.ServerArgs
		if len(startArgs) == 0 {
			if opts := strings.TrimSpace(p.StartOptions); opts != "" {
				startArgs = strings.Fields(opts)
			}
		}
		passwdBin := resolveVNCPasswdPath(serverPath, p.VNCPasswdPath)
		if passwdMode == vncCredModeManaged {
			hasVNC, hasOTP := vncAuthTypesForArgs(startArgs)
			if !hasOTP {
				return "", fmt.Errorf("%s: managed passwd mode requires an OTP-enabled security type (default is vnc+otp)", label)
			}
			startArgs = stripVNCPasswdArgs(startArgs)
			if hasVNC {
				path, err := prepareManagedVNCPasswd(ctx, passwdBin, targetHome, targetUser, targetUID, p.ConnectionID, useCgroup)
				if err != nil {
					return "", fmt.Errorf("%s: %w", label, err)
				}
				managedPasswdFile = path
				startArgs = append([]string{"-rfbauth", path}, startArgs...)
			}
		} else if err := ensureVNCPasswdFile(ctx, passwdBin, targetHome, targetUser, useCgroup); err != nil {
			return "", fmt.Errorf("%s: %w", label, err)
		}

		minD, maxD := p.DisplayMin, p.DisplayMax
		if minD <= 0 {
			minD = defaultDisplayMin
		}
		if maxD <= 0 {
			maxD = defaultDisplayMax
		}

		if p.Display == nil {
			vncDisplayPickMu.Lock()
			defer vncDisplayPickMu.Unlock()
			var startErr error
			for range maxDynamicDisplayTries {
				live := scanXDisplays()
				taken := mergeAvoidSets(live, p.AvoidDisplays)
				d, ok := pickLowestFreeDisplay(taken, minD, maxD)
				if !ok {
					return "", fmt.Errorf("display_exhausted: no free display in range %d..%d", minD, maxD)
				}
				displayStr := fmt.Sprintf(":%d", d)
				tryArgs := append(slices.Clone(startArgs), displayStr)
				prog, tryArgs, envs, workDir := buildVNCServerCommand(serverPath, tryArgs, targetUser, sliceName, useCgroup)
				_, startErr = runCommandWithDir(ctx, envs, workDir, prog, "", tryArgs...)
				if startErr == nil {
					chosenDisplay = d
					haveChosen = true
					break
				}
				if !vncStartErrAlreadyRunning(startErr) {
					prefix := "vnc server"
					if useCgroup {
						prefix = "vnc cgroup server"
					}
					return "", fmt.Errorf("%s start: %w", prefix, startErr)
				}
			}
			if !haveChosen {
				if startErr != nil && vncStartErrAlreadyRunning(startErr) {
					return "", fmt.Errorf("display_exhausted: displays busy after retries")
				}
				return "", fmt.Errorf("display_exhausted: no free display in range %d..%d", minD, maxD)
			}
		} else {
			displayStr, err := normalizeVNCDisplay(p.Display)
			if err != nil {
				return "", fmt.Errorf("%s: %w", label, err)
			}
			chosenDisplay = *p.Display
			haveChosen = true
			live := scanXDisplays()
			if displayTaken(live, chosenDisplay) {
				return "", fmt.Errorf("display_conflict: display %d already in use on node", chosenDisplay)
			}
			args = append(slices.Clone(startArgs), displayStr)
		}
	case "stop":
		display, err := normalizeVNCDisplay(p.Display)
		if err != nil {
			return "", fmt.Errorf("%s: %w", label, err)
		}
		args = []string{"-kill", display}
		cleanupManagedVNCPasswdPaths(ctx, managedVNCPasswdPaths(targetHome, targetUID, p.ConnectionID), targetUser, useCgroup)
	default:
		return "", fmt.Errorf("%s: unsupported operation %q", label, p.Operation)
	}
	cleanupManagedPasswd := func() {
		if managedPasswdFile != "" {
			_ = removeManagedVNCPasswd(ctx, managedPasswdFile, targetUser, useCgroup)
		}
	}
	defer cleanupManagedPasswd()

	if p.Operation == "start" && p.Display != nil {
		prog, args, envs, workDir := buildVNCServerCommand(serverPath, args, targetUser, sliceName, useCgroup)
		_, err = runCommandWithDir(ctx, envs, workDir, prog, "", args...)
		if err != nil {
			prefix := "vnc server"
			if useCgroup {
				prefix = "vnc cgroup server"
			}
			return "", fmt.Errorf("%s start: %w", prefix, err)
		}
	} else if p.Operation == "stop" {
		prog, args, envs, workDir := buildVNCServerCommand(serverPath, args, targetUser, sliceName, useCgroup)
		_, err = runCommandWithDir(ctx, envs, workDir, prog, "", args...)
		if err != nil {
			prefix := "vnc server"
			if useCgroup {
				prefix = "vnc cgroup server"
			}
			return "", fmt.Errorf("%s stop: %w", prefix, err)
		}
	}

	result := payload.VNCControlResult{}
	if p.Operation == "start" {
		if !haveChosen {
			return "", fmt.Errorf("%s: internal error: display not chosen", label)
		}
		port, pid, err := discoverVNCListenFn(chosenDisplay)
		if err != nil || port <= 0 {
			if err == nil {
				err = fmt.Errorf("empty listen port")
			}
			return "", fmt.Errorf("%s: vnc listen port unavailable: %w", label, err)
		}
		result.Display = chosenDisplay
		result.Port = port
		result.PID = pid
		if managedPasswdFile != "" {
			if err := removeManagedVNCPasswd(ctx, managedPasswdFile, targetUser, useCgroup); err != nil {
				killDisplay := fmt.Sprintf(":%d", chosenDisplay)
				killProg, killArgs, killEnvs, killWorkDir := buildVNCServerCommand(serverPath, []string{"-kill", killDisplay}, targetUser, sliceName, useCgroup)
				_, _ = runCommandWithDir(ctx, killEnvs, killWorkDir, killProg, "", killArgs...)
				return "", fmt.Errorf("%s: remove managed vnc passwd: %w", label, err)
			}
		}
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("marshal %s result: %w", label, err)
	}
	return string(resultJSON), nil
}

func vncStartErrAlreadyRunning(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already running")
}

var discoverVNCListenFn = discoverVNCListen

type vncProc struct {
	pid  int
	args string
}

func discoverVNCListen(display int) (int, int, error) {
	if display < 0 {
		return 0, 0, fmt.Errorf("display %d is negative", display)
	}
	procs, err := findVNCProcesses(display)
	if err != nil {
		return 0, 0, err
	}
	if len(procs) == 0 {
		return 0, 0, fmt.Errorf("no Xvnc process for display %d", display)
	}
	for _, proc := range procs {
		if port := parseDcExecRFBPort(proc.args); port > 0 {
			return port, proc.pid, nil
		}
	}
	x11Port := 6000 + display
	var found []vncProc
	var foundPort int
	for _, proc := range procs {
		ports, err := listenPortsForPID(proc.pid)
		if err != nil {
			continue
		}
		var kept []int
		for _, port := range ports {
			if port <= 0 || port == x11Port {
				continue
			}
			kept = append(kept, port)
		}
		if len(kept) > 1 {
			return 0, 0, fmt.Errorf("multiple listen ports for pid %d", proc.pid)
		}
		if len(kept) == 1 {
			found = append(found, proc)
			foundPort = kept[0]
		}
	}
	if len(found) != 1 {
		return 0, 0, fmt.Errorf("listen port not found for display %d", display)
	}
	return foundPort, found[0].pid, nil
}

func findVNCProcesses(display int) ([]vncProc, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	want := ":" + strconv.Itoa(display)
	var out []vncProc
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		parts := strings.Split(string(raw), "\x00")
		if strings.Contains(string(raw), "xorgxrdp") {
			continue
		}
		if !cmdlineHasVNCDisplay(parts, want) {
			continue
		}
		out = append(out, vncProc{pid: pid, args: strings.Join(parts, " ")})
	}
	return out, nil
}

func cmdlineHasVNCDisplay(parts []string, want string) bool {
	for i, part := range parts {
		if part == "" {
			continue
		}
		base := filepath.Base(part)
		if base != "Xvnc" && base != "Xtigervnc" && !strings.HasSuffix(part, "Xvnc") && !strings.HasSuffix(part, "Xtigervnc") {
			continue
		}
		if i+1 < len(parts) && parts[i+1] == want {
			return true
		}
	}
	return false
}

func parseDcExecRFBPort(args string) int {
	fields := strings.Fields(args)
	for idx, token := range fields {
		if token == "-rfbport" && idx+1 < len(fields) {
			port, err := strconv.Atoi(fields[idx+1])
			if err == nil && port > 0 && port <= 65535 {
				return port
			}
		}
		if after, ok := strings.CutPrefix(token, "-rfbport="); ok {
			port, err := strconv.Atoi(after)
			if err == nil && port > 0 && port <= 65535 {
				return port
			}
		}
	}
	return 0
}

func listenPortsForPID(pid int) ([]int, error) {
	fdDir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return nil, err
	}
	inodes := make(map[string]struct{})
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
		if err != nil {
			continue
		}
		if s, ok := strings.CutPrefix(target, "socket:["); ok {
			if inode, ok := strings.CutSuffix(s, "]"); ok {
				inodes[inode] = struct{}{}
			}
		}
	}
	if len(inodes) == 0 {
		return nil, nil
	}
	var ports []int
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		ports = append(ports, tcpListenPorts(path, inodes)...)
	}
	return ports, nil
}

func tcpListenPorts(path string, inodes map[string]struct{}) []int {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var ports []int
	skippedHeader := false
	for line := range strings.Lines(string(data)) {
		if !skippedHeader {
			skippedHeader = true
			continue
		}
		fields := strings.Fields(strings.TrimRight(line, "\r\n"))
		if len(fields) < 10 || !strings.EqualFold(fields[3], "0A") {
			continue
		}
		if _, ok := inodes[fields[9]]; !ok {
			continue
		}
		_, hexPort, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(hexPort, 16, 16)
		if err != nil || port == 0 {
			continue
		}
		ports = append(ports, int(port))
	}
	return ports
}

// quoteShellArg wraps an argument in single quotes, safely escaping any internal single quotes.
func quoteShellArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
