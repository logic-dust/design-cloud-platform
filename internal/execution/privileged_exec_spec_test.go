package execution

import (
	"strings"
	"testing"
)

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
