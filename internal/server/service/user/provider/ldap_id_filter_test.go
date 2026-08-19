package provider

import (
	"strings"
	"testing"
)

func TestBuildIDNumberFilter(t *testing.T) {
	t.Parallel()

	base := "(objectClass=posixAccount)"

	t.Run("empty", func(t *testing.T) {
		if got := buildIDNumberFilter("uidNumber", nil, base); got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
	})

	t.Run("single_no_or", func(t *testing.T) {
		got := buildIDNumberFilter("uidNumber", []uint32{1000}, base)
		want := "(&(uidNumber=1000)(objectClass=posixAccount))"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if strings.Contains(got, "|") {
			t.Fatalf("single-id filter must not contain |: %q", got)
		}
	})

	t.Run("multi_or", func(t *testing.T) {
		got := buildIDNumberFilter("uidNumber", []uint32{10001, 10002}, base)
		want := "(&(|(uidNumber=10001)(uidNumber=10002))(objectClass=posixAccount))"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("chunk_boundary_sizes", func(t *testing.T) {
		for _, n := range []int{1, 49, 50, 51, 200} {
			ids := make([]uint32, n)
			for i := range ids {
				ids[i] = uint32(1000 + i)
			}
			// Filter builder only sees one chunk; chunking is caller's job.
			chunk := ids
			if n > idLookupChunk {
				chunk = ids[:idLookupChunk]
			}
			got := buildIDNumberFilter("uidNumber", chunk, base)
			if len(chunk) == 1 && strings.Contains(got, "|") {
				t.Fatalf("n=%d single chunk must not use |", n)
			}
			if len(chunk) > 1 && !strings.Contains(got, "|") {
				t.Fatalf("n=%d multi chunk must use |", n)
			}
		}
	})

	t.Run("group_attr", func(t *testing.T) {
		got := buildIDNumberFilter("gidNumber", []uint32{9000}, "(objectClass=posixGroup)")
		want := "(&(gidNumber=9000)(objectClass=posixGroup))"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}
