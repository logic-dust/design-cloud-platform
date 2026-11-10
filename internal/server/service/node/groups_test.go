package node

import (
	"context"
	"strings"
	"testing"

	"design-cloud-engine/internal/model"
)

func TestNodeGroupsViaSelector(t *testing.T) {
	svc, _, _ := newTestNodeService(t)
	ctx := context.Background()

	_, _, err := svc.RegisterNodeFromInput(ctx, ReqCreateNodeForm{NodeID: "n1"})
	if err != nil {
		t.Fatalf("register n1: %v", err)
	}
	_, _, err = svc.RegisterNodeFromInput(ctx, ReqCreateNodeForm{NodeID: "n2"})
	if err != nil {
		t.Fatalf("register n2: %v", err)
	}

	login := []string{"login"}
	if _, err := svc.Update(ctx, "n1", UpdateNodeRequest{Groups: login}); err != nil {
		t.Fatalf("assign group on n1: %v", err)
	}
	if ids := membersForGroup(t, svc, ctx, "login"); len(ids) != 1 || ids[0] != "n1" {
		t.Fatalf("selector after create: %+v", ids)
	}

	if _, err := svc.Update(ctx, "n2", UpdateNodeRequest{Groups: login}); err != nil {
		t.Fatalf("add n2 to group: %v", err)
	}
	if ids := membersForGroup(t, svc, ctx, "login"); len(ids) != 2 {
		t.Fatalf("want 2 members, got %+v", ids)
	}

	if _, err := svc.Update(ctx, "n1", UpdateNodeRequest{Groups: []string{}}); err != nil {
		t.Fatalf("clear groups on n1: %v", err)
	}
	if _, err := svc.Update(ctx, "n2", UpdateNodeRequest{Groups: []string{}}); err != nil {
		t.Fatalf("clear groups on n2: %v", err)
	}
	if ids := membersForGroup(t, svc, ctx, "login"); len(ids) != 0 {
		t.Fatalf("group should vanish when last members cleared, got %+v", ids)
	}
}

func TestSelectorRejectsNonRuntimeStatus(t *testing.T) {
	svc, _, _ := newTestNodeService(t)
	ctx := context.Background()

	_, err := svc.ResolveSelectorNodeIDs(ctx, &NodeSettingsBatchSelector{Status: []string{"unset"}})
	if err == nil {
		t.Fatal("expected validation error for unset status filter")
	}
	if !strings.Contains(err.Error(), "runtime filter") {
		t.Fatalf("error = %v, want runtime filter validation", err)
	}
}

func TestNodeAdminViewAlwaysIncludesGroupsKey(t *testing.T) {
	view := BuildNodeAdminView(model.Node{NodeID: "g-empty", Groups: nil})
	if view.Groups == nil {
		t.Fatal("groups must be non-nil empty slice in admin view")
	}
	if len(view.Groups) != 0 {
		t.Fatalf("groups = %v, want []", view.Groups)
	}
}

func membersForGroup(t *testing.T, svc *Service, ctx context.Context, name string) []string {
	t.Helper()
	ids, err := svc.ResolveSelectorNodeIDs(ctx, &NodeSettingsBatchSelector{Groups: []string{name}})
	if err != nil {
		t.Fatalf("resolve group %q: %v", name, err)
	}
	return ids
}
