//go:build !windows

package action

import (
	"testing"
)

func TestIsXRDPSeatCmdline(t *testing.T) {
	tests := []struct {
		cmdline string
		want    bool
	}{
		{"/usr/libexec/Xorg :10 -auth /tmp/.xrdp/auth -module xorgxrdp", true},
		{"/usr/bin/xorgxrdp :11", true},
		{"/usr/lib/xorg/Xorg :12 -config /etc/X11/xrdp/xorg.conf", true},
		{"/usr/bin/Xvnc :1 -auth /home/user/.Xauthority", false},
		{"/usr/bin/bash", false},
	}

	for _, tc := range tests {
		t.Run(tc.cmdline, func(t *testing.T) {
			got := isXRDPSeatCmdline(tc.cmdline)
			if got != tc.want {
				t.Errorf("isXRDPSeatCmdline(%q) = %v, want %v", tc.cmdline, got, tc.want)
			}
		})
	}
}

func TestCmdlineHasDisplay(t *testing.T) {
	tests := []struct {
		cmdline string
		want    string
		match   bool
	}{
		{"/usr/libexec/Xorg :10 -config xrdp.conf", ":10", true},
		{"/usr/libexec/Xorg :10 -config xrdp.conf", ":11", false},
		{"/usr/bin/xorgxrdp :20", ":20", true},
		{"/usr/bin/wrapper :30 --other", ":30", true},
	}

	for _, tc := range tests {
		t.Run(tc.cmdline+" want "+tc.want, func(t *testing.T) {
			got := cmdlineHasDisplay(tc.cmdline, tc.want)
			if got != tc.match {
				t.Errorf("cmdlineHasDisplay(%q, %q) = %v, want %v", tc.cmdline, tc.want, got, tc.match)
			}
		})
	}
}
