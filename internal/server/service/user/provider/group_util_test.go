package provider

import (
	"testing"
)

func TestNormalizeMembersAndMemberIndex(t *testing.T) {
	t.Parallel()
	members := normalizeMembers([]string{" bob ", "alice", "bob", "", "carol"})
	if len(members) != 3 || members[0] != "alice" || members[1] != "bob" || members[2] != "carol" {
		t.Fatalf("normalizeMembers = %#v", members)
	}

	groups := []GroupInfo{
		{Name: "eda", Members: []string{"alice", "bob"}},
		{Name: "cad", Members: []string{"bob"}},
	}
	idx := memberIndex(groups)
	if got := idx["bob"]; len(got) != 2 || got[0] != "cad" || got[1] != "eda" {
		t.Fatalf("bob groups = %#v", got)
	}
	if got := idx["alice"]; len(got) != 1 || got[0] != "eda" {
		t.Fatalf("alice groups = %#v", got)
	}
}

func TestParseOptionalGid(t *testing.T) {
	t.Parallel()
	if parseOptionalGid("x") != nil {
		t.Fatal("expected nil for invalid gid")
	}
	g := parseOptionalGid("1001")
	if g == nil || *g != 1001 {
		t.Fatalf("gid = %v", g)
	}
}
