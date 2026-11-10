//go:build !windows

package command

import "testing"

func TestCallerMatchesOwner(t *testing.T) {
	if !callerMatchesOwner(0, nil) {
		t.Fatal("expected root (euid=0) to be allowed")
	}
	if !callerMatchesOwner(1000, []uint32{999, 1000}) {
		t.Fatal("expected matching owner UID to be allowed")
	}
	if callerMatchesOwner(1001, []uint32{0, 1000}) {
		t.Fatal("expected non-matching non-root UID to be rejected")
	}
}
