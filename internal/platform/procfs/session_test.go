package procfs

import "testing"

func TestParseSessionIDFromCgroup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "cgroup v2 standard session",
			input:    "0::/user.slice/user-1000.slice/session-3.scope",
			expected: "3",
		},
		{
			name:     "cgroup v2 alphanumeric session",
			input:    "0::/user.slice/user-1000.slice/session-c12.scope",
			expected: "c12",
		},
		{
			name:     "cgroup v2 alphanumeric session c42",
			input:    "0::/user.slice/user-1000.slice/session-c42.scope",
			expected: "c42",
		},
		{
			name:     "cgroup v2 nested slice inside session",
			input:    "0::/user.slice/user-1000.slice/session-5.scope/app.slice/xorg.service",
			expected: "5",
		},
		{
			name:     "cgroup v1 named systemd hierarchy",
			input:    "1:name=systemd:/user.slice/user-1000.slice/session-42.scope\n2:cpu,cpuacct:/user.slice",
			expected: "42",
		},
		{
			name:     "user service scope without session",
			input:    "0::/user.slice/user-1000.slice/user@1000.service/init.scope",
			expected: "",
		},
		{
			name:     "empty or malformed",
			input:    "",
			expected: "",
		},
		{
			name:     "no controller delimiter",
			input:    "single-line-no-colons",
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseSessionIDFromCgroup(tc.input)
			if got != tc.expected {
				t.Errorf("ParseSessionIDFromCgroup() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestSessionIDFromPID_Invalid(t *testing.T) {
	t.Parallel()
	if got := SessionIDFromPID(0); got != "" {
		t.Fatalf("SessionIDFromPID(0) = %q, want empty", got)
	}
	if got := SessionIDFromPID(-1); got != "" {
		t.Fatalf("SessionIDFromPID(-1) = %q, want empty", got)
	}
}
