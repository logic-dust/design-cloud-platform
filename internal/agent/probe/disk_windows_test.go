package probe

import (
	"path/filepath"
	"testing"
)

func TestLookupFilesystem_WindowsPaths(t *testing.T) {
	fsMap := map[string]string{
		filepath.Clean(`C:\`): "NTFS",
		filepath.Clean(`D:\`): "ReFS",
		filepath.Clean(`E:\`): "FAT32",
	}

	tests := []struct {
		path string
		want string
	}{
		{`C:\`, "NTFS"},
		{`C:\Program Files\Engine`, "NTFS"},
		{`D:\data\jobs`, "ReFS"},
		{`E:\share`, "FAT32"},
		{`Z:\unknown`, "unknown"},
	}

	for _, tt := range tests {
		got := lookupFilesystem(fsMap, tt.path)
		if got != tt.want {
			t.Errorf("lookupFilesystem(%q) = %q; want %q", tt.path, got, tt.want)
		}
	}
}
