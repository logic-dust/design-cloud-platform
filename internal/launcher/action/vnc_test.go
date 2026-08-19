//go:build !windows

package action

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

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

	for _, raw := range []string{"1", ":1", "  :1  "} {
		got, err := normalizeVNCDisplay(raw)
		if err != nil {
			t.Fatalf("normalize %q: %v", raw, err)
		}
		if got != ":1" {
			t.Fatalf("normalize %q = %q, want :1", raw, got)
		}
	}
}

func TestHandleVNCGenerateOTP(t *testing.T) {

	dir := t.TempDir()
	fake := filepath.Join(dir, "vncpasswd")
	writeExecScript(t, fake, "#!/bin/sh\nprintf 'Full control one-time password: 22225523\\n'\n")

	req := execution.NewExecuteRequest(execution.ActionVNCGenerateOTP, "req-1",
		execution.WithPayload(&payload.VNCGenerateOTP{
			ConnectionID:  12,
			Display:       ":1",
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
			Display:       ":1",
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
			Display:       ":1",
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
			Display:       ":1",
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
			Display:       ":1",
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

	// 2. Valid name "vncpasswd" (for PATH lookup)
	if err := validateVNCPasswdPath("vncpasswd"); err != nil {
		t.Errorf("expected no error for vncpasswd, got: %v", err)
	}

	// 3. Invalid relative path
	if err := validateVNCPasswdPath("./vncpasswd"); err == nil {
		t.Error("expected error for ./vncpasswd, got nil")
	}
	if err := validateVNCPasswdPath("vncpasswd2"); err == nil {
		t.Error("expected error for vncpasswd2, got nil")
	}
}

func TestHandleVNCControl_ServerArgs(t *testing.T) {

	dir := t.TempDir()
	fake := filepath.Join(dir, "vncserver")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"" + filepath.Join(dir, "args.txt") + "\"\nexit 0\n"
	writeExecScript(t, fake, script)

	req := execution.NewExecuteRequest(execution.ActionVNCControl, "req-1",
		execution.WithPayload(&payload.VNCControl{
			ConnectionID:  10,
			Display:       ":1",
			Operation:     "start",
			VNCServerPath: fake,
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
					Display:      ":1",
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
