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
		{name: "vnc derives port from display", res: model.ConnectionResource{Protocol: contract.VNCProtocol, Port: new(uint(5901))}, want: 0},
		{name: "rdp default", res: model.ConnectionResource{Protocol: contract.RDPProtocol}, want: 3389},
		{name: "xrdp default", res: model.ConnectionResource{Protocol: contract.XRDPProtocol}, want: 3389},
		{name: "ssh default", res: model.ConnectionResource{Protocol: contract.SSHProtocol}, want: 22},
		{name: "ssh explicit", res: model.ConnectionResource{Protocol: contract.SSHProtocol, Port: new(uint(2222))}, want: 2222},
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

func TestAdminDeleteResource_DeletesStoppedAndUnbindsRunning(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	svc.settingsSvc = stubSettingsReader{}
	seedBatchNode(t, repos)
	ctx := context.Background()

	res := sampleResource("delete-in-use")
	res.Public = true
	if err := svc.AdminCreateResource(ctx, &res); err != nil {
		t.Fatalf("create resource: %v", err)
	}

	displayStopped := 1
	stoppedConn := model.Connection{
		ResourceID: &res.ID,
		Name:       "stopped-conn",
		Username:   "alice",
		Protocol:   contract.VNCProtocol,
		NodeID:     "node-1",
		Port:       5901,
		Display:    &displayStopped,
		Status:     model.ConnectionStopped,
	}
	if err := repos.Connection.Create(ctx, &stoppedConn); err != nil {
		t.Fatalf("create stopped connection: %v", err)
	}

	displayRunning := 2
	runningConn := model.Connection{
		ResourceID: &res.ID,
		Name:       "running-conn",
		Username:   "bob",
		Protocol:   contract.VNCProtocol,
		NodeID:     "node-1",
		Port:       5902,
		Display:    &displayRunning,
		Status:     model.ConnectionRunning,
	}
	if err := repos.Connection.Create(ctx, &runningConn); err != nil {
		t.Fatalf("create running connection: %v", err)
	}

	if err := svc.AdminDeleteResource(ctx, res.ID); err != nil {
		t.Fatalf("delete resource: %v", err)
	}

	if _, err := repos.Connection.GetByID(ctx, stoppedConn.ID); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("stopped connection err = %v, want contract.ErrNotFound", err)
	}

	gotRunning, err := repos.Connection.GetByID(ctx, runningConn.ID)
	if err != nil {
		t.Fatalf("get running connection: %v", err)
	}
	if gotRunning.Status != model.ConnectionRunning {
		t.Fatalf("running connection status = %v, want running", gotRunning.Status)
	}
	if gotRunning.ResourceID != nil {
		t.Fatalf("running connection resource_id = %v, want nil", *gotRunning.ResourceID)
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
	if err := svc.AdminReplaceResourceUsers(ctx, public.ID, []string{"anyone"}); err == nil {
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
	if err := svc.AdminReplaceResourceUsers(ctx, private.ID, []string{"missing-user"}); err == nil {
		t.Fatal("expected nonexistent user to be rejected")
	} else {
		assertBadRequest(t, err)
	}

	alice := createUser("alice")
	bob := createUser("bob")
	// 重复 username 应去重而不是报冲突。
	if err := svc.AdminReplaceResourceUsers(ctx, private.ID, []string{alice.Username, alice.Username, bob.Username}); err != nil {
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
	overlap := svc.AdminPatchResourceUsers(ctx, private.ID, []string{alice.Username}, []string{alice.Username})
	if overlap == nil {
		t.Fatal("expected add/remove overlap to be rejected")
	}
	assertBadRequest(t, overlap)

	// remove 不校验存在性：允许清理指向已删除用户的悬挂映射。
	if err := svc.AdminPatchResourceUsers(ctx, private.ID, nil, []string{"ghost-user"}); err != nil {
		t.Fatalf("remove dangling mapping: %v", err)
	}
}

func TestAdminResourceUsers_RejectsAppUser(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	svc.settingsSvc = stubSettingsReader{}
	seedBatchNode(t, repos)
	ctx := context.Background()

	// 等保分离：三员 / 超管均为 UserTypeApp，不可能成为连接 OS 属主。
	appUser := model.User{Username: model.UsernameBuiltinSuperAdmin, Role: model.RoleSuperAdmin, Type: model.UserTypeApp}
	if err := repos.User.Create(ctx, &appUser); err != nil {
		t.Fatalf("create app user: %v", err)
	}

	private := sampleResource("app-user-guard")
	if err := svc.AdminCreateResource(ctx, &private); err != nil {
		t.Fatalf("create private resource: %v", err)
	}

	if err := svc.AdminReplaceResourceUsers(ctx, private.ID, []string{appUser.Username}); err == nil {
		t.Fatal("expected app user mapping to be rejected")
	} else {
		assertBadRequest(t, err)
	}

	if err := repos.ConnectionResource.ReplaceUsers(ctx, private.ID, []string{appUser.Username}); err != nil {
		t.Fatalf("seed app user mapping: %v", err)
	}
	users, err := svc.AdminListResourceUsers(ctx, private.ID)
	if err != nil {
		t.Fatalf("list resource users: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("listed users = %d, want 0 (app user omitted)", len(users))
	}
}

func assertBadRequest(t *testing.T, err error) {
	t.Helper()
	domain, ok := errors.AsType[contract.DomainError](err)
	if !ok || domain.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("err = %v, want HTTP 400", err)
	}
}
