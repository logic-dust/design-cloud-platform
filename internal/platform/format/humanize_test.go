package format

import (
	"testing"
)

func TestAutoScaleKB(t *testing.T) {
	tests := []struct {
		name      string
		kb        int64
		wantValue float64
		wantUnit  string
	}{
		{"less than MB", 500, 0.49, "MB"},
		{"exact MB", 1 * 1024, 1.0, "MB"},
		{"exact GB", 1 * 1024 * 1024, 1.0, "GB"},
		{"fractional GB", int64(1.5 * 1024 * 1024), 1.5, "GB"},
		{"exact TB", 1 * 1024 * 1024 * 1024, 1.0, "TB"},
		{"exact PB", 1 * 1024 * 1024 * 1024 * 1024, 1.0, "PB"},
		{"rounding", int64(1635778), 1.56, "GB"}, // 1.56 * 1024^2
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotValue, gotUnit := AutoScaleKB(tt.kb)
			if gotValue != tt.wantValue || gotUnit != tt.wantUnit {
				t.Errorf("AutoScaleKB(%d) = %v %v, want %v %v", tt.kb, gotValue, gotUnit, tt.wantValue, tt.wantUnit)
			}
		})
	}
}

func TestScaleKBTo(t *testing.T) {
	kb := int64(256 * 1024 * 1024) // 256 GB

	if got := ScaleKBTo(kb, "GB"); got != 256.0 {
		t.Errorf("ScaleKBTo(GB) = %v, want 256.0", got)
	}
	if got := ScaleKBTo(kb, "TB"); got != 0.25 {
		t.Errorf("ScaleKBTo(TB) = %v, want 0.25", got)
	}
}
