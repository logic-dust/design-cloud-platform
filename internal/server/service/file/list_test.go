package file

import (
	"errors"
	"io/fs"
	"testing"

	"design-cloud-engine/internal/execution"
)

func TestIsMissingDirectory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "fs not exist", err: fs.ErrNotExist, want: true},
		{name: "PrivilegedExec enoent", err: &execution.ActionError{Message: "failed to list directory: open /home/x/y: no such file or directory"}, want: true},
		{name: "other failure", err: &execution.ActionError{Message: "failed to list directory: permission denied"}, want: false},
		{name: "wrapped enoent", err: errors.Join(errors.New("file.list_files"), &execution.ActionError{Message: "open /tmp/missing: no such file or directory"}), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMissingDirectory(tt.err); got != tt.want {
				t.Fatalf("isMissingDirectory() = %v, want %v", got, tt.want)
			}
		})
	}
}
