package payload

import (
	"errors"
	"path/filepath"
	"strings"
)

var (
	ErrEmptyCommandSpec   = errors.New("empty command spec")
	ErrInvalidCommandSpec = errors.New("invalid command spec")
)

// CommandSpec represents a command to be executed.
type CommandSpec struct {
	Program string   `json:"program"`
	Args    []string `json:"args,omitempty"`
	WorkDir string   `json:"workDir,omitempty"`
}

func NewCommand(program string, args ...string) CommandSpec {
	return CommandSpec{
		Program: program,
		Args:    append([]string(nil), args...),
	}
}

func (c CommandSpec) IsZero() bool {
	return c.Program == ""
}

func (c CommandSpec) Validate() error {
	if c.IsZero() {
		return ErrEmptyCommandSpec
	}
	if strings.ContainsAny(c.Program, "\r\n\t") {
		return ErrInvalidCommandSpec
	}
	for _, arg := range c.Args {
		if strings.ContainsAny(arg, "\r\n") {
			return ErrInvalidCommandSpec
		}
	}
	if c.WorkDir != "" && !filepath.IsAbs(c.WorkDir) {
		return ErrInvalidCommandSpec
	}
	return nil
}
