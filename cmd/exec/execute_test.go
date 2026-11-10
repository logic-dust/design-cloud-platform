//go:build !windows

package main

import (
	"errors"
	"slices"
	"syscall"
	"testing"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
	"design-cloud-engine/internal/platform/osuser"
)

func TestPrepareExecutionProtectionFlags(t *testing.T) {
	tests := []struct {
		name           string
		PrivilegedExec execution.PrivilegedExecSpec
		wantNNP        bool
		wantSeccomp    bool
		wantCapClear   bool
	}{
		{
			name:           "zero value applies no_new_privs and seccomp",
			PrivilegedExec: execution.PrivilegedExecSpec{Privilege: execution.PrivilegeTargetUser},
			wantNNP:        true,
			wantSeccomp:    true,
			wantCapClear:   true,
		},
		{
			name:           "DisableSeccomp keeps no_new_privs",
			PrivilegedExec: execution.PrivilegedExecSpec{Privilege: execution.PrivilegeTargetUser, DisableSeccomp: true},
			wantNNP:        true,
			wantCapClear:   true,
		},
		{
			name:           "DisableSeccomp and DisableNoNewPrivs skip both",
			PrivilegedExec: execution.PrivilegedExecSpec{Privilege: execution.PrivilegeTargetUser, DisableSeccomp: true, DisableNoNewPrivs: true},
			wantCapClear:   true,
		},
		{
			name:           "root action applies protections without clearing capabilities",
			PrivilegedExec: execution.PrivilegedExecSpec{Privilege: execution.PrivilegeRoot},
			wantNNP:        true,
			wantSeccomp:    true,
		},
		{
			name:           "root action with both protections disabled",
			PrivilegedExec: execution.PrivilegedExecSpec{Privilege: execution.PrivilegeRoot, DisableSeccomp: true, DisableNoNewPrivs: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := stubExecutionPreparation(t)

			privilegedExec := tt.PrivilegedExec
			req := execution.NewExecuteRequest(execution.ActionVNCGenerateOTP, "req-1",
				execution.WithTargetUser("alice"),
			)
			_, cancel, err := prepareExecution(req, execution.ActionSpec{PrivilegedExec: &privilegedExec}, true)
			if cancel != nil {
				cancel()
			}
			if err != nil {
				t.Fatalf("prepareExecution returned error: %v", err)
			}

			if got := rec.count("nnp"); got != boolToInt(tt.wantNNP) {
				t.Fatalf("no_new_privs calls = %d, want %v", got, tt.wantNNP)
			}
			if got := rec.count("seccomp"); got != boolToInt(tt.wantSeccomp) {
				t.Fatalf("seccomp calls = %d, want %v", got, tt.wantSeccomp)
			}
			if got := rec.count("clearcaps"); got != boolToInt(tt.wantCapClear) {
				t.Fatalf("capability clear calls = %d, want %v", got, tt.wantCapClear)
			}
			if rec.count("lock") != 1 || rec.steps[0] != "lock" {
				t.Fatalf("OS thread must be locked exactly once before any other step, steps = %v", rec.steps)
			}
		})
	}
}

func TestPrepareExecutionTargetUserOrder(t *testing.T) {
	rec := stubExecutionPreparation(t)

	spec := execution.ActionSpec{PrivilegedExec: &execution.PrivilegedExecSpec{Privilege: execution.PrivilegeTargetUser}}
	req := execution.NewExecuteRequest(execution.ActionJobSubmit, "req-order", execution.WithTargetUser("alice"))
	_, cancel, err := prepareExecution(req, spec, true)
	if cancel != nil {
		cancel()
	}
	if err != nil {
		t.Fatalf("prepareExecution returned error: %v", err)
	}
	want := []string{"lock", "resolve", "setgroups", "setgid", "setuid", "clearcaps", "nnp", "seccomp"}
	if !slices.Equal(rec.steps, want) {
		t.Fatalf("steps = %v, want %v", rec.steps, want)
	}
}

func TestPrepareExecutionFailsClosed(t *testing.T) {
	errBoom := errors.New("boom")
	tests := []struct {
		name       string
		inject     func()
		privilege  execution.PrivilegeMode
		notReached []string
	}{
		{
			name:       "capability clear failure",
			inject:     func() { clearCapsFn = func() error { return errBoom } },
			privilege:  execution.PrivilegeTargetUser,
			notReached: []string{"nnp", "seccomp"},
		},
		{
			name:       "no_new_privs failure",
			inject:     func() { applyNoNewPrivsFn = func(*execution.PrivilegedExecSpec) error { return errBoom } },
			privilege:  execution.PrivilegeTargetUser,
			notReached: []string{"seccomp"},
		},
		{
			name:      "seccomp failure",
			inject:    func() { applySeccompFn = func(*execution.PrivilegedExecSpec) error { return errBoom } },
			privilege: execution.PrivilegeTargetUser,
		},
		{
			name:       "root elevation failure",
			inject:     func() { setUIDFn = func(int) error { return syscall.EPERM } },
			privilege:  execution.PrivilegeRoot,
			notReached: []string{"nnp", "seccomp"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := stubExecutionPreparation(t)
			tt.inject()

			spec := execution.ActionSpec{PrivilegedExec: &execution.PrivilegedExecSpec{Privilege: tt.privilege}}
			req := execution.NewExecuteRequest(execution.ActionUserPasswordVerify, "req-fail",
				execution.WithTargetUser("alice"),
				execution.WithPayload(payload.UserPasswordVerify{Username: "alice", Password: "x"}),
			)
			_, cancel, err := prepareExecution(req, spec, true)
			if cancel != nil {
				cancel()
			}
			if err == nil {
				t.Fatal("prepareExecution should fail")
			}
			for _, step := range tt.notReached {
				if rec.count(step) != 0 {
					t.Fatalf("step %q ran after failure, steps = %v", step, rec.steps)
				}
			}
		})
	}
}

func TestPrepareExecutionRootElevationOrder(t *testing.T) {
	rec := stubExecutionPreparation(t)

	spec := execution.ActionSpec{PrivilegedExec: &execution.PrivilegedExecSpec{
		Privilege: execution.PrivilegeRoot, DisableSeccomp: true, DisableNoNewPrivs: true,
	}}
	req := execution.NewExecuteRequest(execution.ActionVNCCgroupControl, "req-cgroup",
		execution.WithTargetUser("alice"),
	)
	_, cancel, err := prepareExecution(req, spec, true)
	if cancel != nil {
		cancel()
	}
	if err != nil {
		t.Fatalf("prepareExecution returned error: %v", err)
	}
	want := []string{"lock", "setgroups", "setgid", "setuid"}
	if !slices.Equal(rec.steps, want) {
		t.Fatalf("steps = %v, want %v", rec.steps, want)
	}
}

type prepareRecorder struct {
	steps []string
}

func (r *prepareRecorder) add(step string) { r.steps = append(r.steps, step) }

func (r *prepareRecorder) count(step string) int {
	n := 0
	for _, s := range r.steps {
		if s == step {
			n++
		}
	}
	return n
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// stubExecutionPreparation replaces every privileged hook with a recorder so tests never
// touch the real process identity, capabilities, no_new_privs or seccomp state.
func stubExecutionPreparation(t *testing.T) *prepareRecorder {
	t.Helper()

	oldResolveIdentity := resolveIdentityFn
	oldSetGroups := setGroupsFn
	oldSetGID := setGIDFn
	oldSetUID := setUIDFn
	oldSetEnv := setEnvFn
	oldGeteuid := geteuidFn
	oldApplySeccomp := applySeccompFn
	oldApplyNoNewPrivs := applyNoNewPrivsFn
	oldClearCaps := clearCapsFn
	oldLockOSThread := lockOSThreadFn
	t.Cleanup(func() {
		resolveIdentityFn = oldResolveIdentity
		setGroupsFn = oldSetGroups
		setGIDFn = oldSetGID
		setUIDFn = oldSetUID
		setEnvFn = oldSetEnv
		geteuidFn = oldGeteuid
		applySeccompFn = oldApplySeccomp
		applyNoNewPrivsFn = oldApplyNoNewPrivs
		clearCapsFn = oldClearCaps
		lockOSThreadFn = oldLockOSThread
	})

	rec := &prepareRecorder{}
	resolveIdentityFn = func(string) (osuser.UserIdentity, error) {
		rec.add("resolve")
		return osuser.UserIdentity{
			UID:   1001,
			GID:   1001,
			GIDs:  []int{1001},
			Home:  "/home/alice",
			Shell: "/bin/bash",
		}, nil
	}
	setGroupsFn = func([]int) error { rec.add("setgroups"); return nil }
	setGIDFn = func(int) error { rec.add("setgid"); return nil }
	setUIDFn = func(int) error { rec.add("setuid"); return nil }
	setEnvFn = func(string, string) error { return nil }
	geteuidFn = func() int { return 0 }
	applySeccompFn = func(*execution.PrivilegedExecSpec) error { rec.add("seccomp"); return nil }
	applyNoNewPrivsFn = func(*execution.PrivilegedExecSpec) error { rec.add("nnp"); return nil }
	clearCapsFn = func() error { rec.add("clearcaps"); return nil }
	lockOSThreadFn = func() { rec.add("lock") }
	return rec
}
