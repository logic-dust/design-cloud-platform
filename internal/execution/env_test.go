package execution

import "testing"

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
