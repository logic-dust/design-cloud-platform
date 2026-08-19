package execution

import (
	"testing"
)

func TestWindowsActionsRegistration(t *testing.T) {
	// 验证在 Windows 下注册的 Actions 必须包含 License 动作
	expectedActions := []ActionName{
		ActionLicenseStatus,
		ActionLicenseFeatures,
	}

	for _, name := range expectedActions {
		spec, ok := Actions[name]
		if !ok {
			t.Errorf("expected action %s to be registered on Windows", name)
			continue
		}
		if !spec.IsDirect() {
			t.Errorf("action %s must be Direct on Windows, but requires launcher", name)
		}
		if spec.NewPayload == nil {
			t.Errorf("action %s must define NewPayload", name)
		}
	}

	// 关键架构红线：Windows Phase 1 严禁注册任何需要 Launcher 的动作
	for name, spec := range Actions {
		if !spec.IsDirect() {
			t.Errorf("Windows must NOT register launcher action: %s", name)
		}
	}
}
