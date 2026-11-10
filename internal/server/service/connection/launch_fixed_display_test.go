package connection

import (
	"context"
	"errors"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

// 固定 display 直接选中目标节点，跳过 Placer，因此必须自行做可用性/平台校验。
func TestLaunchVNC_FixedDisplayRejectsNonLinuxNode(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	ctx := context.Background()

	alice := model.User{Username: "alice", Role: model.RoleNormal}
	if err := repos.User.Create(ctx, &alice); err != nil {
		t.Fatalf("create user: %v", err)
	}

	node := model.Node{
		NodeID:           "node-win",
		DialHost:         "10.0.0.20",
		Status:           model.NodeStatusOnline,
		AllowConnections: true,
		OS:               "windows",
	}
	if err := repos.Node.Create(ctx, &node); err != nil {
		t.Fatalf("create node: %v", err)
	}

	res := model.ConnectionResource{
		Name:       "fixed-console",
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{"node-win"},
		Display:    new(1),
		MaxPerUser: 1,
		Public:     true,
	}
	if err := repos.ConnectionResource.Create(ctx, &res); err != nil {
		t.Fatalf("create resource: %v", err)
	}

	_, err := svc.Launch(ctx, "alice", res.ID)
	if !errors.Is(err, ErrTargetNodeUnavailable) {
		t.Fatalf("err = %v, want ErrTargetNodeUnavailable", err)
	}
}
