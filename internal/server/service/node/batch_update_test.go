package node

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

func TestAdminBatchUpdate_EmptyAndOverLimit(t *testing.T) {
	svc, _, _ := newTestNodeService(t)
	ctx := context.Background()

	_, err := svc.AdminBatchUpdate(ctx, nil)
	if err == nil {
		t.Fatal("nil items: want error")
	}
	domain, ok := errors.AsType[contract.DomainError](err)
	if !ok || domain.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("nil items err = %v, want 400 DomainError", err)
	}

	_, err = svc.AdminBatchUpdate(ctx, []BatchNodeUpdateItem{})
	if err == nil {
		t.Fatal("empty items: want error")
	}

	tooMany := make([]BatchNodeUpdateItem, maxNodeBatchItems+1)
	for i := range tooMany {
		host := "h"
		tooMany[i] = BatchNodeUpdateItem{NodeID: "n", DialHost: &host}
	}
	_, err = svc.AdminBatchUpdate(ctx, tooMany)
	if err == nil {
		t.Fatal("over limit: want error")
	}
	domain, ok = errors.AsType[contract.DomainError](err)
	if !ok || domain.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("over limit err = %v, want 400 DomainError", err)
	}
	if !strings.Contains(err.Error(), "100") {
		t.Fatalf("over limit message = %q, want mention of 100", err.Error())
	}
}

func TestAdminBatchUpdate_PartialSuccess(t *testing.T) {
	svc, repos, _ := newTestNodeService(t)
	ctx := context.Background()

	if err := repos.Node.Create(ctx, &model.Node{NodeID: "n-ok", DialHost: "old"}); err != nil {
		t.Fatalf("create n-ok: %v", err)
	}
	if err := repos.Node.Create(ctx, &model.Node{NodeID: "n-skip"}); err != nil {
		t.Fatalf("create n-skip: %v", err)
	}

	host := "new-host.example"
	badHost := "not a host!!!"
	out, err := svc.AdminBatchUpdate(ctx, []BatchNodeUpdateItem{
		{NodeID: "n-ok", DialHost: &host},
		{NodeID: "missing-node", DialHost: &host},
		{NodeID: "n-skip"}, // no fields → skipped
		{NodeID: "n-ok", DialHost: &badHost},
	})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if out.Updated != 1 {
		t.Fatalf("updated = %d, want 1", out.Updated)
	}
	if len(out.Failures) != 2 {
		t.Fatalf("failures = %d (%+v), want 2", len(out.Failures), out.Failures)
	}
	if out.Failures[0].Index != 1 || out.Failures[0].NodeID != "missing-node" {
		t.Fatalf("failure[0] = %+v, want index=1 missing-node", out.Failures[0])
	}
	if out.Failures[0].Message != ErrNodeNotFound.Error() {
		t.Fatalf("failure[0].Message = %q, want %q", out.Failures[0].Message, ErrNodeNotFound.Error())
	}
	if out.Failures[1].Index != 3 {
		t.Fatalf("failure[1].Index = %d, want 3", out.Failures[1].Index)
	}

	got, err := repos.Node.GetByNodeID(ctx, "n-ok")
	if err != nil {
		t.Fatalf("get n-ok: %v", err)
	}
	if got.DialHost != host {
		t.Fatalf("dialHost = %q, want %q", got.DialHost, host)
	}
}

func TestAdminBatchUpdate_AllSucceed(t *testing.T) {
	svc, repos, _ := newTestNodeService(t)
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		if err := repos.Node.Create(ctx, &model.Node{NodeID: id}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	h1, h2 := "host-a", "host-b"
	desc := "batch note"
	out, err := svc.AdminBatchUpdate(ctx, []BatchNodeUpdateItem{
		{NodeID: "a", DialHost: &h1},
		{NodeID: "b", DialHost: &h2, Description: &desc},
	})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if out.Updated != 2 || len(out.Failures) != 0 {
		t.Fatalf("updated=%d failures=%+v, want updated=2 no failures", out.Updated, out.Failures)
	}
	a, _ := repos.Node.GetByNodeID(ctx, "a")
	b, _ := repos.Node.GetByNodeID(ctx, "b")
	if a.DialHost != h1 || b.DialHost != h2 || b.Description != desc {
		t.Fatalf("a.host=%q b.host=%q b.desc=%q", a.DialHost, b.DialHost, b.Description)
	}
}
