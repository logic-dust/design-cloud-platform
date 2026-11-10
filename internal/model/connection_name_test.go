package model

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateConnectionName(t *testing.T) {
	exact := strings.Repeat("a", MaxConnectionNameLength)
	cjkExact := strings.Repeat("桌", MaxConnectionNameLength)
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "short", in: "vnc-node01", want: "vnc-node01"},
		{name: "trims surrounding space", in: "  vnc-node01 \t\n", want: "vnc-node01"},
		{name: "exactly 64 ascii", in: exact, want: exact},
		{name: "exactly 64 runes cjk", in: cjkExact, want: cjkExact},
		{name: "long ascii", in: strings.Repeat("b", 200), want: strings.Repeat("b", MaxConnectionNameLength)},
		{name: "long cjk cut on rune boundary", in: strings.Repeat("中", 100), want: strings.Repeat("中", MaxConnectionNameLength)},
		{name: "mixed width", in: strings.Repeat("a中", 40), want: strings.Repeat("a中", 32)},
		{name: "space at cut point trimmed", in: strings.Repeat("x", 63) + " tail", want: strings.Repeat("x", 63)},
		{name: "empty", in: "   ", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateConnectionName(tt.in)
			if got != tt.want {
				t.Fatalf("TruncateConnectionName() = %q, want %q", got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("result is not valid UTF-8: %q", got)
			}
			if n := utf8.RuneCountInString(got); n > MaxConnectionNameLength {
				t.Fatalf("result has %d runes, max %d", n, MaxConnectionNameLength)
			}
		})
	}
}

func TestConnectionName(t *testing.T) {
	if got := ConnectionName(" 设计桌面 ", " node01 "); got != "设计桌面-node01" {
		t.Fatalf("ConnectionName() = %q", got)
	}
	longResource := strings.Repeat("资", 128)
	node := strings.Repeat("n", 64)
	got := ConnectionName(longResource, node)
	if utf8.RuneCountInString(got) != MaxConnectionNameLength || !utf8.ValidString(got) {
		t.Fatalf("ConnectionName() = %q (%d runes)", got, utf8.RuneCountInString(got))
	}
	if got != strings.Repeat("资", MaxConnectionNameLength) {
		t.Fatalf("ConnectionName() should keep the resource prefix, got %q", got)
	}
}

func TestProbedConnectionName(t *testing.T) {
	tests := []struct {
		name, desktop, host string
		port                uint
		want                string
	}{
		{name: "desktop name", desktop: "  我的桌面  ", host: "h", port: 5901, want: "我的桌面"},
		{name: "fallback when empty", desktop: "", host: "node01", port: 5901, want: "node01:5901"},
		{name: "fallback when blank", desktop: " \t", host: "node01", port: 5902, want: "node01:5902"},
		{name: "long desktop", desktop: strings.Repeat("桌", 70), host: "h", port: 1, want: strings.Repeat("桌", MaxConnectionNameLength)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProbedConnectionName(tt.desktop, tt.host, tt.port); got != tt.want {
				t.Fatalf("ProbedConnectionName() = %q, want %q", got, tt.want)
			}
		})
	}
	long := ProbedConnectionName("", strings.Repeat("h", 255), 65535)
	if utf8.RuneCountInString(long) != MaxConnectionNameLength || !utf8.ValidString(long) {
		t.Fatalf("fallback name not truncated: %q", long)
	}
}
