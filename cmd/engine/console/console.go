// Package console is the Engine CLI's stdout/stderr adapter.
//
// It deliberately lives under cmd/engine: its only consumers are CLI commands,
// so it must not become a repository-wide dependency.
package console

import (
	"fmt"
	"os"
)

// PrintStdout writes a formatted string to os.Stdout, ignoring errors.
func PrintStdout(format string, a ...any) {
	_, _ = fmt.Fprintf(os.Stdout, format, a...)
}

// PrintlnStdout writes a line to os.Stdout, ignoring errors.
func PrintlnStdout(a ...any) {
	_, _ = fmt.Fprintln(os.Stdout, a...)
}

// PrintStderr writes a formatted string to os.Stderr, ignoring errors.
func PrintStderr(format string, a ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format, a...)
}

// PrintlnStderr writes a line to os.Stderr, ignoring errors.
func PrintlnStderr(a ...any) {
	_, _ = fmt.Fprintln(os.Stderr, a...)
}
