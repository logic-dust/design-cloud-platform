package execution

import "testing"

func TestLinuxDcExecProtectionFlags(t *testing.T) {
	type flags struct{ disableSeccomp, disableNNP bool }
	exceptions := map[ActionName]flags{
		ActionUserPasswordChange: {disableSeccomp: true, disableNNP: true},
		ActionUserPasswordReset:  {disableSeccomp: true, disableNNP: true},
		ActionUserPasswordVerify: {disableSeccomp: true, disableNNP: true},
		ActionJobSubmit:          {disableSeccomp: true},
		ActionJobControl:         {disableSeccomp: true},
		ActionJobModify:          {disableSeccomp: true},
		ActionVNCGenerateOTP:     {disableSeccomp: true},
		ActionVNCControl:         {disableSeccomp: true, disableNNP: true},
		ActionVNCCgroupControl:   {disableSeccomp: true, disableNNP: true},
		ActionXRDPControl:        {disableSeccomp: true},
		ActionSessionControl:     {disableSeccomp: true},
	}
	for name, spec := range Actions {
		if spec.PrivilegedExec == nil {
			continue
		}
		want := exceptions[name]
		got := flags{spec.PrivilegedExec.DisableSeccomp, spec.PrivilegedExec.DisableNoNewPrivs}
		if got != want {
			t.Errorf("action %s: DisableSeccomp=%v DisableNoNewPrivs=%v, want %v/%v",
				name, got.disableSeccomp, got.disableNNP, want.disableSeccomp, want.disableNNP)
		}
	}
	for name := range exceptions {
		if _, ok := Actions[name]; !ok {
			t.Errorf("action %s is not registered", name)
		}
	}
}
