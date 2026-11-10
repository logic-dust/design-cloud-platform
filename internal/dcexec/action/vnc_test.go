//go:build !windows

package action

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "vnc-action-test-*")
	if err == nil {
		origSocket, origLock := x11SocketDir, x11LockDir
		x11SocketDir = filepath.Join(tmpDir, ".X11-unix")
		x11LockDir = tmpDir
		_ = os.MkdirAll(x11SocketDir, 0o755)
		code := m.Run()
		x11SocketDir, x11LockDir = origSocket, origLock
		_ = os.RemoveAll(tmpDir)
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// writeExecScript writes an executable script and flushes it to disk before
// returning. Parallel tests exec scripts immediately after writing; without
// fsync the kernel may briefly report ETXTBSY ("text file busy") on exec.
func writeExecScript(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o700)
	if err != nil {
		t.Fatalf("create script %s: %v", path, err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write script %s: %v", path, err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync script %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close script %s: %v", path, err)
	}
}

func TestParseFullControlOTP(t *testing.T) {
	t.Parallel()

	otp, err := parseFullControlOTP("Full control one-time password: 22225523\n")
	if err != nil {
		t.Fatalf("parse otp: %v", err)
	}
	if otp != "22225523" {
		t.Fatalf("otp = %q, want 22225523", otp)
	}
}

func TestParseFullControlOTPRejectsMissingValue(t *testing.T) {
	t.Parallel()

	if _, err := parseFullControlOTP("View-only one-time password: 11112222\n"); err == nil {
		t.Fatalf("parse succeeded, want error")
	}
}

func TestNormalizeVNCDisplay(t *testing.T) {
	t.Parallel()

	one := 1
	got, err := normalizeVNCDisplay(&one)
	if err != nil || got != ":1" {
		t.Fatalf("normalize 1 = %q, %v", got, err)
	}
	zero := 0
	got, err = normalizeVNCDisplay(&zero)
	if err != nil || got != ":0" {
		t.Fatalf("normalize 0 = %q, %v", got, err)
	}
	if _, err := normalizeVNCDisplay(nil); err == nil {
		t.Fatal("nil display was accepted")
	}
	neg := -1
	if _, err := normalizeVNCDisplay(&neg); err == nil {
		t.Fatal("negative display was accepted")
	}
}

func TestVNCControlDisplayJSONIsNumber(t *testing.T) {
	t.Parallel()

	var got payload.VNCControl
	if err := json.Unmarshal([]byte(`{"connectionId":1,"display":0,"operation":"start"}`), &got); err != nil {
		t.Fatalf("unmarshal 0: %v", err)
	}
	if got.Display == nil || *got.Display != 0 {
		t.Fatalf("display = %v, want 0", got.Display)
	}
	if err := json.Unmarshal([]byte(`{"display":":1"}`), &got); err == nil {
		t.Fatal("colon string decoded as display")
	}
	if err := json.Unmarshal([]byte(`{"display":null}`), &got); err != nil || got.Display != nil {
		t.Fatalf("null display = %v, err %v", got.Display, err)
	}
}

func TestHandleVNCGenerateOTP(t *testing.T) {

	dir := t.TempDir()
	fake := filepath.Join(dir, "vncpasswd")
	writeExecScript(t, fake, "#!/bin/sh\nprintf 'Full control one-time password: 22225523\\n'\n")

	req := execution.NewExecuteRequest(execution.ActionVNCGenerateOTP, "req-1",
		execution.WithPayload(&payload.VNCGenerateOTP{
			ConnectionID:  12,
			Display:       new(1),
			VNCPasswdPath: fake,
		}),
	)
	output, err := HandleVNCGenerateOTP(context.Background(), req)
	if err != nil {
		t.Fatalf("handle vnc otp: %v", err)
	}

	var result payload.VNCGenerateOTPResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if result.OTP != "22225523" {
		t.Fatalf("otp = %q, want 22225523", result.OTP)
	}
}

func TestHandleVNCGenerateOTPReadsStderr(t *testing.T) {

	dir := t.TempDir()
	fake := filepath.Join(dir, "vncpasswd")
	writeExecScript(t, fake, "#!/bin/sh\nprintf 'Full control one-time password: 22225523\\n' >&2\n")

	req := execution.NewExecuteRequest(execution.ActionVNCGenerateOTP, "req-1",
		execution.WithPayload(&payload.VNCGenerateOTP{
			ConnectionID:  12,
			Display:       new(1),
			VNCPasswdPath: fake,
		}),
	)
	output, err := HandleVNCGenerateOTP(context.Background(), req)
	if err != nil {
		t.Fatalf("handle vnc otp: %v", err)
	}

	var result payload.VNCGenerateOTPResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if result.OTP != "22225523" {
		t.Fatalf("otp = %q, want 22225523", result.OTP)
	}
}

func TestHandleVNCGenerateOTPHandlesLingeringStdio(t *testing.T) {

	dir := t.TempDir()
	fake := filepath.Join(dir, "vncpasswd")
	writeExecScript(t, fake, "#!/bin/sh\nprintf 'Full control one-time password: 22225523\\n' >&2\nsleep 5 >&2 &\nexit 0\n")

	req := execution.NewExecuteRequest(execution.ActionVNCGenerateOTP, "req-1",
		execution.WithPayload(&payload.VNCGenerateOTP{
			ConnectionID:  12,
			Display:       new(1),
			VNCPasswdPath: fake,
		}),
	)
	start := time.Now()
	output, err := HandleVNCGenerateOTP(context.Background(), req)
	if err != nil {
		t.Fatalf("handle vnc otp: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("handle vnc otp took %s, want bounded by WaitDelay", elapsed)
	}

	var result payload.VNCGenerateOTPResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if result.OTP != "22225523" {
		t.Fatalf("otp = %q, want 22225523", result.OTP)
	}
}

func TestHandleVNCGenerateOTPRedactsOTPInFailureOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fake := filepath.Join(dir, "vncpasswd")
	writeExecScript(t, fake, "#!/bin/sh\nprintf 'Full control one-time password: 22225523\\n' >&2\nexit 1\n")

	req := execution.NewExecuteRequest(execution.ActionVNCGenerateOTP, "req-1",
		execution.WithPayload(&payload.VNCGenerateOTP{
			ConnectionID:  12,
			Display:       new(1),
			VNCPasswdPath: fake,
		}),
	)
	_, err := HandleVNCGenerateOTP(context.Background(), req)
	if err == nil {
		t.Fatalf("handle succeeded, want error")
	}
	if strings.Contains(err.Error(), "22225523") {
		t.Fatalf("error leaked otp: %v", err)
	}
	if !strings.Contains(err.Error(), "Full control one-time password: [redacted]") {
		t.Fatalf("error = %q, want redacted otp marker", err.Error())
	}
}

func TestHandleVNCGenerateOTPRejectsRelativePath(t *testing.T) {
	t.Parallel()

	req := execution.NewExecuteRequest(execution.ActionVNCGenerateOTP, "req-1",
		execution.WithPayload(&payload.VNCGenerateOTP{
			ConnectionID:  12,
			Display:       new(1),
			VNCPasswdPath: "./vncpasswd",
		}),
	)
	if _, err := HandleVNCGenerateOTP(context.Background(), req); err == nil {
		t.Fatalf("handle succeeded, want error")
	}
}

func TestValidateVNCPasswdPath(t *testing.T) {
	t.Parallel()

	// 1. Valid absolute path
	if err := validateVNCPasswdPath("/usr/bin/vncpasswd"); err != nil {
		t.Errorf("expected no error for /usr/bin/vncpasswd, got: %v", err)
	}

	// 2. Bare name is rejected (must be an absolute path)
	if err := validateVNCPasswdPath("vncpasswd"); err == nil {
		t.Error("expected error for bare vncpasswd, got nil")
	}

	// 3. Invalid relative path
	if err := validateVNCPasswdPath("./vncpasswd"); err == nil {
		t.Error("expected error for ./vncpasswd, got nil")
	}
	if err := validateVNCPasswdPath("vncpasswd2"); err == nil {
		t.Error("expected error for vncpasswd2, got nil")
	}
}

func writeFakeVNCPasswdF(t *testing.T, dir string) string {
	t.Helper()
	fake := filepath.Join(dir, "vncpasswd")
	script := "#!/bin/sh\nif [ \"$1\" = \"-f\" ]; then read -r p; printf 'enc:%s\\n' \"$p\"; exit 0; fi\nexit 1\n"
	writeExecScript(t, fake, script)
	return fake
}

func TestResolveVNCPasswdPath(t *testing.T) {
	t.Parallel()
	if got := resolveVNCPasswdPath("/opt/TurboVNC/bin/vncserver", ""); got != "/opt/TurboVNC/bin/vncpasswd" {
		t.Fatalf("derived = %q", got)
	}
	if got := resolveVNCPasswdPath("/opt/TurboVNC/bin/vncserver", "/custom/vncpasswd"); got != "/custom/vncpasswd" {
		t.Fatalf("payload override = %q", got)
	}
}

func TestEnsureVNCPasswdFileCreatesAndSkips(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	passwdBin := writeFakeVNCPasswdF(t, dir)
	if err := ensureVNCPasswdFile(context.Background(), passwdBin, dir, "tester", false); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	passwdFile := filepath.Join(dir, ".vnc", "passwd")
	data, err := os.ReadFile(passwdFile)
	if err != nil {
		t.Fatalf("read passwd: %v", err)
	}
	if !strings.HasPrefix(string(data), "enc:") {
		t.Fatalf("passwd content = %q", data)
	}
	if err := ensureVNCPasswdFile(context.Background(), passwdBin, dir, "tester", false); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	data2, err := os.ReadFile(passwdFile)
	if err != nil {
		t.Fatalf("read passwd again: %v", err)
	}
	if string(data2) != string(data) {
		t.Fatal("passwd file was rewritten on second ensure")
	}
}

func TestHandleVNCControl_ServerArgs(t *testing.T) {
	prev := discoverVNCListenFn
	discoverVNCListenFn = func(display int) (int, int, error) {
		return 5901, 4242, nil
	}
	t.Cleanup(func() { discoverVNCListenFn = prev })

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	passwdBin := writeFakeVNCPasswdF(t, dir)
	fake := filepath.Join(dir, "vncserver")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"" + filepath.Join(dir, "args.txt") + "\"\nprintf '%s\\n' \"$HOME\" > \"" + filepath.Join(dir, "home.txt") + "\"\nexit 0\n"
	writeExecScript(t, fake, script)

	req := execution.NewExecuteRequest(execution.ActionVNCControl, "req-1",
		execution.WithPayload(&payload.VNCControl{
			ConnectionID:  10,
			Display:       new(1),
			Operation:     "start",
			VNCServerPath: fake,
			VNCPasswdPath: passwdBin,
			ServerArgs:    []string{"-wm", "2d", "-geometry", "1920x1080"},
		}),
	)
	if _, err := HandleVNCControl(context.Background(), req); err != nil {
		t.Fatalf("handle vnc control: %v", err)
	}

	gotArgs, err := os.ReadFile(filepath.Join(dir, "args.txt"))
	if err != nil {
		t.Fatalf("read args.txt: %v", err)
	}
	want := "-wm 2d -geometry 1920x1080 :1\n"
	if string(gotArgs) != want {
		t.Fatalf("got args = %q, want %q", string(gotArgs), want)
	}

	gotHome, err := os.ReadFile(filepath.Join(dir, "home.txt"))
	if err != nil {
		t.Fatalf("read home.txt: %v", err)
	}
	if strings.TrimSpace(string(gotHome)) == "" {
		t.Fatal("HOME environment variable was not set in launched VNC command")
	}
}

func TestHandleVNCControlManagedPasswd(t *testing.T) {
	prev := discoverVNCListenFn
	discoverVNCListenFn = func(display int) (int, int, error) {
		return 5901, 4242, nil
	}
	t.Cleanup(func() { discoverVNCListenFn = prev })

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USER", "tester")
	passwdBin := writeFakeVNCPasswdF(t, dir)
	fake := filepath.Join(dir, "vncserver")
	argsFile := filepath.Join(dir, "args.txt")
	rfbauthFile := filepath.Join(dir, "rfbauth.txt")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" > \"" + argsFile + "\"\n" +
		"rf=''; prev=''\n" +
		"for a in \"$@\"; do if [ \"$prev\" = \"-rfbauth\" ]; then rf=\"$a\"; fi; prev=\"$a\"; done\n" +
		"if [ -n \"$rf\" ] && [ -s \"$rf\" ]; then echo yes > \"" + rfbauthFile + "\"; else echo no > \"" + rfbauthFile + "\"; fi\n"
	writeExecScript(t, fake, script)

	req := execution.NewExecuteRequest(execution.ActionVNCControl, "req-1",
		execution.WithPayload(&payload.VNCControl{
			ConnectionID:  77,
			Display:       new(1),
			Operation:     "start",
			VNCServerPath: fake,
			VNCPasswdPath: passwdBin,
			PasswdMode:    vncCredModeManaged,
			ServerArgs:    []string{"-rfbauth", "/tmp/user-passwd", "-rfbauth=/tmp/other", "-wm", "2d"},
		}),
	)
	if _, err := HandleVNCControl(context.Background(), req); err != nil {
		t.Fatalf("handle vnc control: %v", err)
	}

	wantPath := filepath.Join(dir, ".cache", "dc", "connection", "77", "passwd")
	gotArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	want := "-rfbauth " + wantPath + " -wm 2d :1\n"
	if string(gotArgs) != want {
		t.Fatalf("args = %q, want %q", gotArgs, want)
	}
	flag, err := os.ReadFile(rfbauthFile)
	if err != nil || strings.TrimSpace(string(flag)) != "yes" {
		t.Fatalf("managed passwd was not present during vncserver start: %q, err %v", flag, err)
	}
	if _, err := os.Stat(wantPath); !os.IsNotExist(err) {
		t.Fatalf("managed passwd still exists after successful start: %v", err)
	}
}

func TestHandleVNCControlManagedRemoveFailureKillsServer(t *testing.T) {
	prev := discoverVNCListenFn
	discoverVNCListenFn = func(display int) (int, int, error) {
		return 5901, 4242, nil
	}
	t.Cleanup(func() { discoverVNCListenFn = prev })

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USER", "tester")
	passwdBin := writeFakeVNCPasswdF(t, dir)
	fake := filepath.Join(dir, "vncserver")
	invocations := filepath.Join(dir, "invocations.txt")
	// 启动调用把 -rfbauth 指向的密码文件替换成非空目录，
	// 使启动成功后的 os.Remove 以 ENOTEMPTY 失败，触发兜底 kill。
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"" + invocations + "\"\n" +
		"rf=''; prev=''\n" +
		"for a in \"$@\"; do if [ \"$prev\" = \"-rfbauth\" ]; then rf=\"$a\"; fi; prev=\"$a\"; done\n" +
		"if [ -n \"$rf\" ]; then rm -f \"$rf\"; mkdir -p \"$rf\"; touch \"$rf\"/keep; fi\n" +
		"exit 0\n"
	writeExecScript(t, fake, script)

	req := execution.NewExecuteRequest(execution.ActionVNCControl, "req-1",
		execution.WithPayload(&payload.VNCControl{
			ConnectionID:  21,
			Display:       new(1),
			Operation:     "start",
			VNCServerPath: fake,
			VNCPasswdPath: passwdBin,
			PasswdMode:    vncCredModeManaged,
		}),
	)
	_, err := HandleVNCControl(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "remove managed vnc passwd") {
		t.Fatalf("error = %v, want managed passwd removal failure", err)
	}

	got, err := os.ReadFile(invocations)
	if err != nil {
		t.Fatalf("read invocations: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 2 {
		t.Fatalf("invocations = %q, want exactly start + kill", string(got))
	}
	if !strings.Contains(lines[0], "-rfbauth") || !strings.Contains(lines[0], ":1") {
		t.Fatalf("first invocation = %q, want start with -rfbauth", lines[0])
	}
	if !strings.Contains(lines[1], "-kill :1") {
		t.Fatalf("second invocation = %q, want kill", lines[1])
	}
}

func TestHandleVNCControlManagedRejectsNonOTPSecurityTypes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	fake := filepath.Join(dir, "vncserver")
	writeExecScript(t, fake, "#!/bin/sh\nexit 0\n")

	req := execution.NewExecuteRequest(execution.ActionVNCControl, "req-1",
		execution.WithPayload(&payload.VNCControl{
			ConnectionID:  10,
			Display:       new(1),
			Operation:     "start",
			VNCServerPath: fake,
			PasswdMode:    vncCredModeManaged,
			ServerArgs:    []string{"-securitytypes", "vnc"},
		}),
	)
	_, err := HandleVNCControl(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "requires an OTP-enabled security type") {
		t.Fatalf("error = %v, want OTP security type rejection", err)
	}
}

func TestHandleVNCControlManagedOTPOnlySkipsPasswdFile(t *testing.T) {
	prev := discoverVNCListenFn
	discoverVNCListenFn = func(display int) (int, int, error) {
		return 5901, 4242, nil
	}
	t.Cleanup(func() { discoverVNCListenFn = prev })

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	fake := filepath.Join(dir, "vncserver")
	argsFile := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"" + argsFile + "\"\n"
	writeExecScript(t, fake, script)

	req := execution.NewExecuteRequest(execution.ActionVNCControl, "req-1",
		execution.WithPayload(&payload.VNCControl{
			ConnectionID:  11,
			Display:       new(1),
			Operation:     "start",
			VNCServerPath: fake,
			PasswdMode:    vncCredModeManaged,
			ServerArgs:    []string{"-securitytypes", "otp"},
		}),
	)
	if _, err := HandleVNCControl(context.Background(), req); err != nil {
		t.Fatalf("handle vnc control: %v", err)
	}
	gotArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	if want := "-securitytypes otp :1\n"; string(gotArgs) != want {
		t.Fatalf("args = %q, want %q", gotArgs, want)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cache", "dc", "connection", "11", "passwd")); !os.IsNotExist(err) {
		t.Fatalf("managed passwd was created for OTP-only server: %v", err)
	}
}

func TestHandleVNCControlManagedStopRemovesPasswd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USER", "tester")
	fake := filepath.Join(dir, "vncserver")
	argsFile := filepath.Join(dir, "args.txt")
	writeExecScript(t, fake, "#!/bin/sh\nprintf '%s\\n' \"$*\" > \""+argsFile+"\"\n")

	passwdFile := filepath.Join(dir, ".cache", "dc", "connection", "88", "passwd")
	if err := os.MkdirAll(filepath.Dir(passwdFile), 0o700); err != nil {
		t.Fatalf("mkdir managed dir: %v", err)
	}
	if err := os.WriteFile(passwdFile, []byte("enc"), 0o600); err != nil {
		t.Fatalf("write managed passwd: %v", err)
	}

	req := execution.NewExecuteRequest(execution.ActionVNCControl, "req-1",
		execution.WithPayload(&payload.VNCControl{
			ConnectionID:  88,
			Display:       new(1),
			Operation:     "stop",
			VNCServerPath: fake,
			PasswdMode:    vncCredModeManaged,
		}),
	)
	if _, err := HandleVNCControl(context.Background(), req); err != nil {
		t.Fatalf("handle vnc control: %v", err)
	}
	if _, err := os.Stat(passwdFile); !os.IsNotExist(err) {
		t.Fatalf("managed passwd still exists after stop: %v", err)
	}

	// 节点设置改回 user 后，stop 仍应清掉此前残留的 managed 密码文件。
	if err := os.MkdirAll(filepath.Dir(passwdFile), 0o700); err != nil {
		t.Fatalf("recreate managed dir: %v", err)
	}
	if err := os.WriteFile(passwdFile, []byte("enc"), 0o600); err != nil {
		t.Fatalf("recreate managed passwd: %v", err)
	}
	userModeReq := execution.NewExecuteRequest(execution.ActionVNCControl, "req-2",
		execution.WithPayload(&payload.VNCControl{
			ConnectionID:  88,
			Display:       new(1),
			Operation:     "stop",
			VNCServerPath: fake,
		}),
	)
	if _, err := HandleVNCControl(context.Background(), userModeReq); err != nil {
		t.Fatalf("handle vnc control in user mode: %v", err)
	}
	if _, err := os.Stat(passwdFile); !os.IsNotExist(err) {
		t.Fatalf("managed passwd still exists after user-mode stop: %v", err)
	}
}

func TestVNCAuthTypesForArgs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		args    []string
		wantVNC bool
		wantOTP bool
	}{
		{"default", nil, true, true},
		{"both", []string{"-securitytypes", "vnc,otp"}, true, true},
		{"vnc only", []string{"-securitytypes", "vnc"}, true, false},
		{"otp only", []string{"-securitytypes", "otp"}, false, true},
		{"equals form", []string{"-securitytypes=otp"}, false, true},
		{"tlsvnc", []string{"-securitytypes", "tlsvnc,otp"}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotVNC, gotOTP := vncAuthTypesForArgs(tc.args)
			if gotVNC != tc.wantVNC || gotOTP != tc.wantOTP {
				t.Fatalf("vncAuthTypesForArgs(%v) = %v,%v want %v,%v", tc.args, gotVNC, gotOTP, tc.wantVNC, tc.wantOTP)
			}
		})
	}
}

func TestStripVNCPasswdArgs(t *testing.T) {
	t.Parallel()

	got := stripVNCPasswdArgs([]string{
		"-rfbauth", "/tmp/a", "-rfbauth=/tmp/b", "--rfbauth=/tmp/x",
		"-PasswordFile", "/tmp/c", "-passwd=/tmp/d", "-wm", "2d",
	})
	// 非 vncserver 合法选项的密码文件参数保留，交由 vncserver 报错。
	want := []string{"-PasswordFile", "/tmp/c", "-passwd=/tmp/d", "-wm", "2d"}
	if !slices.Equal(got, want) {
		t.Fatalf("strip = %v, want %v", got, want)
	}
}

func TestManagedVNCPasswdPaths(t *testing.T) {
	t.Parallel()

	paths := managedVNCPasswdPaths("/home/u", 1001, 42)
	wantPrimary := filepath.Join("/home/u", ".cache", "dc", "connection", "42", "passwd")
	wantFallback := filepath.Join("/tmp", ".dc-1001", "connection", "42", "passwd")
	if !slices.Equal(paths, []string{wantPrimary, wantFallback}) {
		t.Fatalf("paths = %v", paths)
	}
	if got := managedVNCPasswdPaths("", 1001, 42); !slices.Equal(got, []string{wantFallback}) {
		t.Fatalf("fallback paths = %v", got)
	}
	if got := managedVNCPasswdPaths("/home/u", 1001, 0); len(got) != 0 {
		t.Fatalf("zero connection id produced %v", got)
	}
}

func TestHandleVNCCgroupControl_TargetUserValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		targetUser string
		wantErr    string
	}{
		{"empty target user", "", "targetUser is required"},
		{"root user not permitted", "root", "not permitted"},
		{"nobody user not permitted", "nobody", "not permitted"},
		{"invalid user prefix", "-invalid", "not permitted"},
		{"non-existent user", "non_existent_test_user_xyz_123", "lookup targetUser"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := execution.NewExecuteRequest(execution.ActionVNCCgroupControl, "req-1",
				execution.WithTargetUser(tc.targetUser),
				execution.WithPayload(&payload.VNCControl{
					ConnectionID: 10,
					Display:      new(1),
					Operation:    "start",
				}),
			)
			_, err := HandleVNCCgroupControl(context.Background(), req)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want containing %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestQuoteShellArg(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		want  string
	}{
		{"normal", "'normal'"},
		{"has space", "'has space'"},
		{"has'quote", `'has'\''quote'`},
		{"", "''"},
		{"$VAR", `'$VAR'`},
		{"`echo 1`", "'`echo 1`'"},
	}

	for _, tc := range cases {
		got := quoteShellArg(tc.input)
		if got != tc.want {
			t.Errorf("quoteShellArg(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

//go:fix inline
func vncDisplay(n int) *int {
	return new(n)
}

func TestHandleVNCGenerateOTPRejectsBareCommandName(t *testing.T) {
	req := execution.NewExecuteRequest(execution.ActionVNCGenerateOTP, "req-1",
		execution.WithPayload(&payload.VNCGenerateOTP{
			ConnectionID:  12,
			Display:       new(1),
			VNCPasswdPath: "vncpasswd",
		}),
	)
	if _, err := HandleVNCGenerateOTP(context.Background(), req); err == nil {
		t.Fatal("expected bare vncpasswd name to be rejected")
	}
}

func TestHandleVNCGenerateOTPRunsWithoutLoginShell(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "vncpasswd")
	// 输出自身环境中的 PATH 与参数，验证未经 login shell 且参数原样传递
	writeExecScript(t, fake, "#!/bin/sh\necho \"args=$*\" >&2\necho \"path=$PATH\" >&2\nprintf 'Full control one-time password: 11112222\\n'\n")
	t.Setenv("HOME", dir)

	req := execution.NewExecuteRequest(execution.ActionVNCGenerateOTP, "req-1",
		execution.WithPayload(&payload.VNCGenerateOTP{
			ConnectionID:  12,
			Display:       new(3),
			VNCPasswdPath: fake,
		}),
	)
	output, err := HandleVNCGenerateOTP(context.Background(), req)
	if err != nil {
		t.Fatalf("handle vnc otp: %v", err)
	}
	var result payload.VNCGenerateOTPResult
	if err := json.Unmarshal([]byte(output), &result); err != nil || result.OTP != "11112222" {
		t.Fatalf("otp = %q, err %v", result.OTP, err)
	}
	env := vncPasswdEnv()
	if !slices.Contains(env, "PATH="+execSafePath) || !slices.Contains(env, "HOME="+dir) {
		t.Fatalf("unexpected env: %v", env)
	}
}
