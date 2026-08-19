//go:build !windows

package verifier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// statusWithCapEff renders a minimal /proc/self/status carrying the given CapEff mask.
func statusWithCapEff(mask string) string {
	return "Name:\tdc-launcher\n" +
		"CapInh:\t0000000000000000\n" +
		"CapPrm:\t" + mask + "\n" +
		"CapEff:\t" + mask + "\n" +
		"CapBnd:\t000001ffffffffff\n"
}

func TestParseEffectiveCapabilities(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		want    uint64
		wantErr bool
	}{
		{
			name:   "full mask",
			status: statusWithCapEff("000001ffffffffff"),
			want:   0x1ffffffffff,
		},
		{
			name:   "zero mask",
			status: statusWithCapEff("0000000000000000"),
			want:   0,
		},
		{
			name:   "only setuid+setgid (bits 6,7) plus unrelated bit",
			status: statusWithCapEff("00000000000000c1"), // bit0 + bit6 + bit7
			want:   0xc1,
		},
		{
			name:    "CapEff absent",
			status:  "Name:\tdc-launcher\nCapBnd:\t000001ffffffffff\n",
			wantErr: true,
		},
		{
			name:    "CapEff malformed",
			status:  statusWithCapEff("not-a-hex"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseEffectiveCapabilities(tc.status)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got mask %#x", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("mask = %#x, want %#x", got, tc.want)
			}
		})
	}
}

// TestCapabilities asserts the L0 gate: both CAP_SETUID and CAP_SETGID must be effective.
func TestCapabilities(t *testing.T) {
	tests := []struct {
		name       string
		mask       string
		wantErr    bool
		wantInErrs []string
	}{
		{
			name: "both capabilities present",
			mask: "000001ffffffffff",
		},
		{
			name:       "no capabilities at all (e.g. installed on NFS)",
			mask:       "0000000000000000",
			wantErr:    true,
			wantInErrs: []string{"CAP_SETUID", "CAP_SETGID", "setcap"},
		},
		{
			name:       "only CAP_SETGID present",
			mask:       "0000000000000040", // bit 6
			wantErr:    true,
			wantInErrs: []string{"CAP_SETUID"},
		},
		{
			name:       "only CAP_SETUID present",
			mask:       "0000000000000080", // bit 7
			wantErr:    true,
			wantInErrs: []string{"CAP_SETGID"},
		},
	}

	dir := t.TempDir()
	statusPath := filepath.Join(dir, "status")
	original := capabilityStatusPath
	capabilityStatusPath = statusPath
	t.Cleanup(func() { capabilityStatusPath = original })

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(statusPath, []byte(statusWithCapEff(tc.mask)), 0o600); err != nil {
				t.Fatalf("write synthetic status: %v", err)
			}

			err := Capabilities()
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			for _, want := range tc.wantInErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err.Error(), want)
				}
			}
		})
	}

	t.Run("status file unreadable", func(t *testing.T) {
		capabilityStatusPath = filepath.Join(dir, "does-not-exist")
		if err := Capabilities(); err == nil {
			t.Fatal("expected error for missing status file, got nil")
		}
	})
}
