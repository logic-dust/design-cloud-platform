package connection

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

func TestResolvedResourcePort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		res  model.ConnectionResource
		want uint
	}{
		{name: "vnc derives port from display", res: model.ConnectionResource{Protocol: contract.VNCProtocol, Port: uintPtr(5901)}, want: 0},
		{name: "rdp default", res: model.ConnectionResource{Protocol: contract.RDPProtocol}, want: 3389},
		{name: "xrdp default", res: model.ConnectionResource{Protocol: contract.XRDPProtocol}, want: 3389},
		{name: "ssh default", res: model.ConnectionResource{Protocol: contract.SSHProtocol}, want: 22},
		{name: "ssh explicit", res: model.ConnectionResource{Protocol: contract.SSHProtocol, Port: uintPtr(2222)}, want: 2222},
		{name: "unknown protocol", res: model.ConnectionResource{Protocol: contract.ProtocolUnknown}, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := resolvedResourcePort(tc.res); got != tc.want {
				t.Fatalf("resolvedResourcePort = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAdminCreateResource_RejectsUnknownNode(t *testing.T) {
	svc, _ := newTestLaunchService(t)
	svc.settingsSvc = stubSettingsReader{}
	ctx := context.Background()

	// sampleResource 指向 node-1，但未登记任何节点。
	res := sampleResource("unknown-node")
	err := svc.AdminCreateResource(ctx, &res)
	if err == nil {
		t.Fatal("expected unknown node to be rejected")
	}
	assertBadRequest(t, err)
}

func TestAdminDeleteResource_InUseIncludesHistory(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	svc.settingsSvc = stubSettingsReader{}
	seedBatchNode(t, repos)
	ctx := context.Background()

	res := sampleResource("delete-in-use")
	res.Public = true
	if err := svc.AdminCreateResource(ctx, &res); err != nil {
		t.Fatalf("create resource: %v", err)
	}

	display := 1
	conn := model.Connection{
		ResourceID: &res.ID,
		Name:       "stopped-conn",
		Username:   "alice",
		Protocol:   contract.VNCProtocol,
		NodeID:     "node-1",
		Port:       5901,
		Display:    &display,
		Status:     model.ConnectionStopped,
	}
	if err := repos.Connection.Create(ctx, &conn); err != nil {
		t.Fatalf("create stopped connection: %v", err)
	}

	err := svc.AdminDeleteResource(ctx, res.ID)
	if !errors.Is(err, ErrResourceInUse) {
		t.Fatalf("err = %v, want ErrResourceInUse", err)
	}
	domain, ok := errors.AsType[contract.DomainError](err)
	if !ok || domain.HTTPStatus() != http.StatusConflict {
		t.Fatalf("err = %v, want HTTP 409", err)
	}
	if domain.Message() != ErrResourceInUse.Error() {
		t.Fatalf("public message = %q, want %q", domain.Message(), ErrResourceInUse.Error())
	}
}

func TestAdminResourceUsers_Guards(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	svc.settingsSvc = stubSettingsReader{}
	seedBatchNode(t, repos)
	ctx := context.Background()

	createUser := func(name string) model.User {
		t.Helper()
		user := model.User{Username: name, Role: model.RoleNormal}
		if err := repos.User.Create(ctx, &user); err != nil {
			t.Fatalf("create user %s: %v", name, err)
		}
		return user
	}

	public := sampleResource("public-pool")
	public.Public = true
	if err := svc.AdminCreateResource(ctx, &public); err != nil {
		t.Fatalf("create public resource: %v", err)
	}

	// public 规格不接受用户映射，列表也应为空。
	if err := svc.AdminReplaceResourceUsers(ctx, public.ID, []uint{1}); err == nil {
		t.Fatal("expected public resource mapping to be rejected")
	} else {
		assertBadRequest(t, err)
	}
	users, err := svc.AdminListResourceUsers(ctx, public.ID)
	if err != nil {
		t.Fatalf("list public resource users: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("public resource users = %d, want 0", len(users))
	}

	private := sampleResource("private-pool")
	if err := svc.AdminCreateResource(ctx, &private); err != nil {
		t.Fatalf("create private resource: %v", err)
	}

	// add 侧校验用户存在性，避免悬挂映射。
	if err := svc.AdminReplaceResourceUsers(ctx, private.ID, []uint{999999}); err == nil {
		t.Fatal("expected nonexistent user to be rejected")
	} else {
		assertBadRequest(t, err)
	}

	alice := createUser("alice")
	bob := createUser("bob")
	// 重复 userId 应去重而不是报冲突。
	if err := svc.AdminReplaceResourceUsers(ctx, private.ID, []uint{alice.ID, alice.ID, bob.ID}); err != nil {
		t.Fatalf("replace users with duplicates: %v", err)
	}
	users, err = svc.AdminListResourceUsers(ctx, private.ID)
	if err != nil {
		t.Fatalf("list private resource users: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("private resource users = %d, want 2", len(users))
	}

	// 同一用户不能同时 add 和 remove。
	overlap := svc.AdminPatchResourceUsers(ctx, private.ID, []uint{alice.ID}, []uint{alice.ID})
	if overlap == nil {
		t.Fatal("expected add/remove overlap to be rejected")
	}
	assertBadRequest(t, overlap)

	// remove 不校验存在性：允许清理指向已删除用户的悬挂映射。
	if err := svc.AdminPatchResourceUsers(ctx, private.ID, nil, []uint{999999}); err != nil {
		t.Fatalf("remove dangling mapping: %v", err)
	}
}

func assertBadRequest(t *testing.T, err error) {
	t.Helper()
	domain, ok := errors.AsType[contract.DomainError](err)
	if !ok || domain.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("err = %v, want HTTP 400", err)
	}
}
