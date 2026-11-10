//go:build !windows

package file

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestListReadCap(t *testing.T) {
	t.Parallel()

	if got := listReadCap(999); got != 1099 {
		t.Fatalf("cap(999) = %d, want 1099", got)
	}
	if got := listReadCap(50); got != 150 {
		t.Fatalf("cap(50) = %d, want 150", got)
	}
}

func TestReadDirNamesExactFitIsNotTruncated(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	names, truncated, err := readDirNames(dir, 2)
	if err != nil {
		t.Fatalf("readDirNames: %v", err)
	}
	if truncated {
		t.Fatal("truncated = true, want false")
	}
	if len(names) != 2 {
		t.Fatalf("len = %d, want 2", len(names))
	}
}

func TestReadDirNamesCapBoundary(t *testing.T) {
	t.Parallel()

	const cap = 5
	dir := t.TempDir()
	for i := range cap {
		name := fmt.Sprintf("n%02d", i)
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	names, truncated, err := readDirNames(dir, cap)
	if err != nil {
		t.Fatalf("exact cap: %v", err)
	}
	if truncated || len(names) != cap {
		t.Fatalf("exact cap: len=%d truncated=%v, want %d and false", len(names), truncated, cap)
	}

	over := t.TempDir()
	for i := range cap + 1 {
		name := fmt.Sprintf("n%02d", i)
		if err := os.WriteFile(filepath.Join(over, name), nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	names, truncated, err = readDirNames(over, cap)
	if err != nil {
		t.Fatalf("over cap: %v", err)
	}
	if !truncated || len(names) != cap {
		t.Fatalf("over cap: len=%d truncated=%v, want %d and true", len(names), truncated, cap)
	}
}

func TestReadDirNamesEmpty(t *testing.T) {
	t.Parallel()

	names, truncated, err := readDirNames(t.TempDir(), 100)
	if err != nil {
		t.Fatalf("readDirNames: %v", err)
	}
	if truncated || len(names) != 0 {
		t.Fatalf("names=%v truncated=%v, want empty and not truncated", names, truncated)
	}
}

func TestForEachDirBatchVisitsEveryName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	var got []string
	if err := forEachDirBatch(dir, func(names []string) error {
		got = append(got, names...)
		return nil
	}); err != nil {
		t.Fatalf("forEachDirBatch: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (%v)", len(got), got)
	}
}
