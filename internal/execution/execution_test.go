package execution

import (
	"strings"
	"testing"
)

func TestIsValidEnvKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		key  string
		want bool
	}{
		{name: "valid", key: "LSF_QUEUE", want: true},
		{name: "empty", key: "", want: false},
		{name: "equals", key: "BAD=KEY", want: false},
		{name: "space", key: "BAD KEY", want: false},
		{name: "newline", key: "BAD\nKEY", want: false},
		{name: "non ascii", key: "\u0080KEY", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := IsValidEnvKey(tc.key); got != tc.want {
				t.Fatalf("IsValidEnvKey(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

func TestMatchDcExecProtocolVersion(t *testing.T) {
	if err := MatchDcExecProtocolVersion("0.5.0", "0.5.0"); err != nil {
		t.Fatalf("same version must pass: %v", err)
	}
	if err := MatchDcExecProtocolVersion("", ""); err != nil {
		t.Fatalf("empty/empty (dev builds without ldflags) must pass: %v", err)
	}
	if err := MatchDcExecProtocolVersion("0.5.0", "0.4.0"); err == nil {
		t.Fatal("mismatch must fail")
	}
	if err := MatchDcExecProtocolVersion("0.5.0", ""); err == nil {
		t.Fatal("engine version without PrivilegedExec ldflags must fail")
	}
}

func TestPrivilegedExecSpecValidate(t *testing.T) {
	tests := []struct {
		name    string
		spec    *PrivilegedExecSpec
		wantErr bool
	}{
		{name: "nil spec", spec: nil},
		{name: "all protections", spec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser}},
		{name: "seccomp off, nnp on", spec: &PrivilegedExecSpec{DisableSeccomp: true}},
		{name: "both off", spec: &PrivilegedExecSpec{DisableSeccomp: true, DisableNoNewPrivs: true}},
		{name: "nnp off, seccomp on", spec: &PrivilegedExecSpec{DisableNoNewPrivs: true}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRegisterActionsPanicsOnInconsistentPrivilegedExecSpec(t *testing.T) {
	const name ActionName = "TEST_INCONSISTENT_EXEC_BIN_SPEC"
	defer delete(Actions, name)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("registerActions should panic when DisableNoNewPrivs is set without DisableSeccomp")
		}
		if msg, _ := r.(string); !strings.Contains(msg, string(name)) {
			t.Fatalf("panic message %q should name the action", r)
		}
		if _, ok := Actions[name]; ok {
			t.Fatal("inconsistent action must not be registered")
		}
	}()
	registerActions(map[ActionName]ActionSpec{
		name: {PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, DisableNoNewPrivs: true}},
	})
}

func TestRegisteredPrivilegedExecSpecsAreConsistent(t *testing.T) {
	for name, spec := range Actions {
		if err := spec.PrivilegedExec.Validate(); err != nil {
			t.Errorf("action %s: %v", name, err)
		}
	}
}
