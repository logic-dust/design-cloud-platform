//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/launcher/verifier"
)

// Version is the launcher version string, injected at build time via ldflags.
// e.g.: -X 'main.Version=0.2.2'
var Version string

const (
	startupTimeout = 7 * time.Second
)

func main() {
	os.Exit(run())
}

// run executes the launcher flow end-to-end with 4-layer defense-in-depth security.
func run() (exitCode int) {
	// --- L0: Immediate environment security cleanup ---
	// Before doing anything else, strip dangerous variables (LD_PRELOAD, etc.)
	// that may have been inherited from the parent process.
	execution.CleanCurrentProcessEnv()

	// Ensure structured JSON output even in case of panic
	defer func() {
		if r := recover(); r != nil {
			respondError(fmt.Sprintf("Internal Launcher Panic: %v", r))
			exitCode = 2 // System error exit code
		}
	}()

	// --- L1: Runtime environment verification ---
	// Validates file permissions, caller identity, parent process, and environment safety.
	// Prevents unauthorized processes from invoking launcher.
	if err := verifier.RuntimeEnvironment(); err != nil {
		respondError(fmt.Sprintf("RuntimeEnvironment verification failed: %v", err))
		exitCode = 126
		return
	}

	// --- Read LauncherInput from stdin (newline-delimited JSON header line) ---
	// Protocol: [compact JSON ExecuteInput]\n followed by optional raw file bytes (FILE_UPLOAD)
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	input, err := readRequest(ctx)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			respondError(fmt.Sprintf("Request timeout: must send request within %v", startupTimeout))
		} else {
			respondError(fmt.Sprintf("Failed to parse request: %v", err))
		}
		exitCode = 1
		return
	}

	// Protocol version must match exactly (same RPM / same build). No negotiation.
	if err := execution.MatchLauncherProtocolVersion(input.Version, Version); err != nil {
		respondError(err.Error())
		exitCode = 126
		return
	}

	// --- L2: Token verification ---
	// Validates dynamic token to ensure request comes from legitimate Agent process.
	// Prevents forged requests from non-Engine processes that bypass L1,
	// and isolates multiple Engine instances on the same machine.
	if err := verifier.Token(input.Token); err != nil {
		respondError(fmt.Sprintf("Token verification failed: %v", err))
		exitCode = 126
		return
	}

	// --- L3: Action allowlist check ---
	// Validates action is in allowlist and performs parameter validation.
	// Prevents execution of unauthorized operations and parameter injection attacks.
	actionHandler, spec, err := verifier.Action(input.Request.ActionName)
	if err != nil {
		respondError(fmt.Sprintf("Action not allowed: %v", err))
		exitCode = 126
		return
	}

	// --- Route stream actions directly (bypass JSON-envelope execute path) ---
	// Stream actions carry the file body as raw bytes after the JSON header in
	// stdin; they must not go through execute() which calls the handler registry.
	if spec.IsStream() {
		streamCtx, streamCancel, err := prepareExecution(input.Request, spec, false)
		if err != nil {
			respondError(err.Error())
			exitCode = 126
			return
		}
		if streamCancel != nil {
			defer streamCancel()
		}
		switch input.Request.ActionName {
		case execution.ActionFileUpload:
			// stdinReader is positioned after the JSON header; remaining bytes = file body.
			exitCode = runStreamUpload(streamCtx, input.Request, stdinReader)
		case execution.ActionFileDownload:
			exitCode = runStreamDownload(streamCtx, input.Request)
		default:
			respondError(fmt.Sprintf("unhandled stream action: %s", input.Request.ActionName))
			exitCode = 126
		}
		return
	}

	// --- Execute with L4 protection ---
	// L4 (seccomp-BPF) is applied inside execute() after setuid/setgid.
	// It restricts system calls to prevent exploitation of handler vulnerabilities.
	output, err := execute(input.Request, actionHandler, spec)
	if err != nil {
		respondError(err.Error())
		exitCode = 126
		return
	}

	respondJSON(execution.ExecuteOutput{
		Success: true,
		Output:  output,
	})
	exitCode = 0
	return
}
