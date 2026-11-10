package schema

import "testing"

func TestValidateMinSupportedVersion(t *testing.T) {
	t.Parallel()

	for _, v := range []string{"", "0.0.0", "0.0.1", "1.2.3", "10.20.30"} {
		if err := ValidateMinSupportedVersion(v); err != nil {
			t.Errorf("ValidateMinSupportedVersion(%q) = %v, want nil", v, err)
		}
	}

	for _, v := range []string{
		"v1.2.3",
		"V1.2.3",
		"1.2",
		"1.2.3.4",
		"01.2.3",
		"1.02.3",
		"1.2.03",
		"1.2.3-beta",
		"1.2.3+build",
		"1.2.3-rc.1+ex",
		" 1.2.3",
		"1.2.3 ",
		"-1.2.3",
		"1.2.x",
		"latest",
		"1.2.3\n",
	} {
		if err := ValidateMinSupportedVersion(v); err == nil {
			t.Errorf("ValidateMinSupportedVersion(%q) = nil, want error", v)
		}
	}

	tooLong := "1234567890.1234567890.1234567890" // 32 chars exactly should pass
	if len(tooLong) != 32 {
		t.Fatalf("setup: %q length = %d, want 32", tooLong, len(tooLong))
	}
	if err := ValidateMinSupportedVersion(tooLong); err != nil {
		t.Errorf("32-char version rejected: %v", err)
	}
	if err := ValidateMinSupportedVersion(tooLong + "0"); err == nil {
		t.Error("33-char version accepted")
	}
}
