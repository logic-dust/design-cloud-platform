//go:build !windows

package main

import (
	"testing"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/execution"
)

func TestPrepareExecutionCanDisableSeccomp(t *testing.T) {
	tests := []struct {
		name            string
		spec            execution.ActionSpec
		wantSeccompCall bool
	}{
		{
			name: "default user action applies seccomp",
			spec: execution.ActionSpec{
				Launcher: &execution.LauncherSpec{
					Privilege:     execution.PrivilegeTargetUser,
					IOModel:       execution.IOModelJSON,
					EnableSeccomp: true,
				},
			},
			wantSeccompCall: true,
		},
		{
			name: "explicitly disabled action skips seccomp",
			spec: execution.ActionSpec{
				Launcher: &execution.LauncherSpec{
					Privilege:     execution.PrivilegeTargetUser,
					IOModel:       execution.IOModelJSON,
					EnableSeccomp: false,
				},
			},
			wantSeccompCall: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := stubExecutionPreparation(t)
			defer restore()

			var seccompCalls int
			applySeccompFn = func(*execution.LauncherSpec) {
				seccompCalls++
			}
			var noNewPrivsCalls int
			applyNoNewPrivsFn = func(*execution.LauncherSpec) bool {
				noNewPrivsCalls++
				return true
			}

			req := execution.NewExecuteRequest(execution.ActionVNCGenerateOTP, "req-1",
				execution.WithTargetUser("alice"),
			)
			_, cancel, err := prepareExecution(req, tt.spec, true)
			if cancel != nil {
				cancel()
			}
			if err != nil {
				t.Fatalf("prepareExecution returned error: %v", err)
			}

			if got := seccompCalls > 0; got != tt.wantSeccompCall {
				t.Fatalf("seccomp called = %v, want %v", got, tt.wantSeccompCall)
			}
			if !tt.wantSeccompCall && noNewPrivsCalls != 1 {
				t.Fatalf("no-new-privs calls = %d, want 1", noNewPrivsCalls)
			}
		})
	}
}

func TestVNCGenerateOTPDisablesSeccomp(t *testing.T) {
	spec, ok := execution.Actions[execution.ActionVNCGenerateOTP]
	if !ok {
		t.Fatal("VNC_GENERATE_OTP action is not registered")
	}
	if spec.Launcher == nil || spec.Launcher.Privilege != execution.PrivilegeTargetUser {
		t.Fatalf("launcher privilege = %v, want %v", spec.Launcher, execution.PrivilegeTargetUser)
	}
	if spec.Launcher.EnableSeccomp {
		t.Fatal("VNC_GENERATE_OTP should disable seccomp because it execs TurboVNC tooling")
	}
}

func TestVNCControlDisablesSeccomp(t *testing.T) {
	spec, ok := execution.Actions[execution.ActionVNCControl]
	if !ok {
		t.Fatal("VNC_CONTROL action is not registered")
	}
	if spec.Launcher == nil || spec.Launcher.Privilege != execution.PrivilegeTargetUser {
		t.Fatalf("launcher privilege = %v, want %v", spec.Launcher, execution.PrivilegeTargetUser)
	}
	if spec.Launcher.EnableSeccomp {
		t.Fatal("VNC_CONTROL should disable seccomp because it execs vncserver tooling")
	}
}

func TestVNCCgroupControlDisablesSeccomp(t *testing.T) {
	spec, ok := execution.Actions[execution.ActionVNCCgroupControl]
	if !ok {
		t.Fatal("VNC_CGROUP_CONTROL action is not registered")
	}
	if spec.Launcher == nil || spec.Launcher.Privilege != execution.PrivilegeRoot {
		t.Fatalf("launcher privilege = %v, want %v", spec.Launcher, execution.PrivilegeRoot)
	}
	if spec.Launcher.EnableSeccomp {
		t.Fatal("VNC_CGROUP_CONTROL should disable seccomp because it execs systemd-run tooling")
	}
}

func stubExecutionPreparation(t *testing.T) func() {
	t.Helper()

	oldResolveIdentity := resolveIdentityFn
	oldSetGroups := setGroupsFn
	oldSetGID := setGIDFn
	oldSetUID := setUIDFn
	oldSetEnv := setEnvFn
	oldApplySeccomp := applySeccompFn
	oldApplyNoNewPrivs := applyNoNewPrivsFn

	resolveIdentityFn = func(string) (common.UserIdentity, error) {
		return common.UserIdentity{
			UID:   1001,
			GID:   1001,
			GIDs:  []int{1001},
			Home:  "/home/alice",
			Shell: "/bin/bash",
		}, nil
	}
	setGroupsFn = func([]int) error { return nil }
	setGIDFn = func(int) error { return nil }
	setUIDFn = func(int) error { return nil }
	setEnvFn = func(string, string) error { return nil }

	return func() {
		resolveIdentityFn = oldResolveIdentity
		setGroupsFn = oldSetGroups
		setGIDFn = oldSetGID
		setUIDFn = oldSetUID
		setEnvFn = oldSetEnv
		applySeccompFn = oldApplySeccomp
		applyNoNewPrivsFn = oldApplyNoNewPrivs
	}
}
