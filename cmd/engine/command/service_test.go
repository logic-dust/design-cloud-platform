package command

import (
	"strings"
	"testing"
)

func TestRunServiceCommand_RequiresSubcommand(t *testing.T) {
	t.Parallel()
	err := runServiceCommand(&Flags{}, nil)
	if err == nil {
		t.Fatal("expected error for missing subcommand")
	}
	if !strings.Contains(err.Error(), "service sub-command required") {
		t.Fatalf("err = %q, want 'service sub-command required'", err)
	}
}

func TestRunServiceCommand_UnknownSubcommand(t *testing.T) {
	t.Parallel()
	err := runServiceCommand(&Flags{}, []string{"bogus"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown service command: bogus") {
		t.Fatalf("err = %q, want 'unknown service command: bogus'", err)
	}
}

func TestRunServiceCommand_EmptyArgsIsUnknown(t *testing.T) {
	t.Parallel()
	err := runServiceCommand(&Flags{}, []string{""})
	if err == nil {
		t.Fatal("expected error for empty subcommand name")
	}
	if !strings.Contains(err.Error(), "unknown service command") {
		t.Fatalf("err = %q, want 'unknown service command'", err)
	}
}
