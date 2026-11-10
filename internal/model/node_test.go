package model

import (
	"testing"
)

func TestValidateNodeGroupName(t *testing.T) {
	t.Parallel()
	if err := ValidateNodeGroupName("Login"); err == nil {
		t.Fatal("expected invalid uppercase group name")
	}
	if err := ValidateNodeGroupName("login"); err != nil {
		t.Fatalf("valid name rejected: %v", err)
	}
}

func TestNodeHasGroups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		groups   []string
		selector []string
		want     bool
	}{
		{name: "empty selector matches any", groups: []string{"login"}, selector: nil, want: true},
		{name: "single group match", groups: []string{"login", "viz"}, selector: []string{"login"}, want: true},
		{name: "missing group", groups: []string{"login"}, selector: []string{"viz"}, want: false},
		{name: "AND all required", groups: []string{"login", "viz"}, selector: []string{"login", "viz"}, want: true},
		{name: "AND partial", groups: []string{"login"}, selector: []string{"login", "viz"}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NodeHasGroups(tc.groups, tc.selector)
			if got != tc.want {
				t.Errorf("NodeHasGroups(%v, %v) = %v, want %v", tc.groups, tc.selector, got, tc.want)
			}
		})
	}
}
