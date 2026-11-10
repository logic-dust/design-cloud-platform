package action

import "testing"

func TestPickLowestFreeDisplay(t *testing.T) {
	taken := map[int]struct{}{1: {}, 3: {}}
	d, ok := pickLowestFreeDisplay(taken, 1, 5)
	if !ok || d != 2 {
		t.Fatalf("pick = %d ok=%v, want 2 true", d, ok)
	}
	full := map[int]struct{}{1: {}, 2: {}, 3: {}}
	_, ok = pickLowestFreeDisplay(full, 1, 3)
	if ok {
		t.Fatal("expected exhausted in 1..3")
	}
}
