package model

import "testing"

func TestValidateConnectionResourceSlug(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		value string
		ok    bool
	}{
		{"valid", "lab-vnc", true},
		{"underscore", "gpu_pool", true},
		{"uppercase", "Lab", false},
		{"dot", "lab.pool", false},
		{"cjk", "实验室", false},
		{"empty", "  ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConnectionResourceSlug(tc.value)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestValidateConnectionResourceName(t *testing.T) {
	t.Parallel()
	if err := ValidateConnectionResourceName("实验室 VNC"); err != nil {
		t.Fatalf("cjk name: %v", err)
	}
	if err := ValidateConnectionResourceName("   "); err != nil {
		t.Fatalf("empty name should be optional: %v", err)
	}
	if err := ValidateConnectionResourceName("123456789012345678901234567890123"); err == nil {
		t.Fatal("expected max length error")
	}
}
