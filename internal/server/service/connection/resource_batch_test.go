package connection

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

type stubSettingsReader struct {
	cap int
}

func (s stubSettingsReader) MaxPerUserCap(ctx context.Context) (int, error) {
	if s.cap == 0 {
		return 16, nil
	}
	return s.cap, nil
}

func (s stubSettingsReader) GetConnectionSettings(ctx context.Context) (schema.ConnectionSettings, error) {
	c := s.cap
	if c == 0 {
		c = 16
	}
	return schema.ConnectionSettings{MaxPerUserCap: c}, nil
}

func (s stubSettingsReader) GetEffectiveAgentSettingsForNode(ctx context.Context, nodeID string) (schema.AgentSettings, error) {
	return schema.AgentSettings{}, nil
}

func sampleResource(name string) model.ConnectionResource {
	return model.ConnectionResource{
		Name:       name,
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{"node-1"},
		NodeGroups: []string{},
		MaxPerUser: 1,
	}
}

// seedBatchNode 登记 sampleResource 引用的节点；创建/更新规格会校验节点存在性。
func seedBatchNode(t *testing.T, repos *db.Repos) {
	t.Helper()
	ctx := context.Background()
	node := model.Node{
		NodeID:           "node-1",
		DialHost:         "10.0.0.10",
		Status:           model.NodeStatusOnline,
		AllowConnections: true,
		OS:               "linux",
	}
	if err := repos.Node.Create(ctx, &node); err != nil {
		t.Fatalf("seed node: %v", err)
	}
}

func TestAdminCreateResource_NameConflict(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	svc.settingsSvc = stubSettingsReader{}
	seedBatchNode(t, repos)
	ctx := context.Background()

	res := sampleResource("desk-a")
	if err := svc.AdminCreateResource(ctx, &res); err != nil {
		t.Fatalf("create: %v", err)
	}
	dup := sampleResource("desk-a")
	err := svc.AdminCreateResource(ctx, &dup)
	if err == nil {
		t.Fatal("expected name conflict")
	}
	if !errors.Is(err, ErrResourceNameConflict) {
		t.Fatalf("err = %v, want ErrResourceNameConflict", err)
	}
	domain, ok := errors.AsType[contract.DomainError](err)
	if !ok || domain.HTTPStatus() != http.StatusConflict {
		t.Fatalf("err = %v, want HTTP 409", err)
	}
}

func TestAdminUpdateResource_NameConflictAndExists(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	svc.settingsSvc = stubSettingsReader{}
	seedBatchNode(t, repos)
	ctx := context.Background()

	a := sampleResource("alpha")
	b := sampleResource("beta")
	if err := svc.AdminCreateResource(ctx, &a); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if err := svc.AdminCreateResource(ctx, &b); err != nil {
		t.Fatalf("create b: %v", err)
	}

	b.Name = "alpha"
	err := svc.AdminUpdateResource(ctx, &b)
	if !errors.Is(err, ErrResourceNameConflict) {
		t.Fatalf("rename conflict err = %v", err)
	}

	missing := sampleResource("ghost")
	missing.ID = 99999
	err = svc.AdminUpdateResource(ctx, &missing)
	if !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("missing id err = %v, want not found", err)
	}

	a.MaxPerUser = 2
	if err := svc.AdminUpdateResource(ctx, &a); err != nil {
		t.Fatalf("same-name update: %v", err)
	}
}

func TestAdminBatchUpsertResources_PartialSuccess(t *testing.T) {
	svc, repos := newTestLaunchService(t)
	svc.settingsSvc = stubSettingsReader{}
	seedBatchNode(t, repos)
	ctx := context.Background()

	existing := sampleResource("keep")
	if err := svc.AdminCreateResource(ctx, &existing); err != nil {
		t.Fatalf("seed: %v", err)
	}

	keepOverwrite := sampleResource("keep")
	keepOverwrite.MaxPerUser = 2
	dupFirst := sampleResource("dup-in-batch")
	dupSecond := sampleResource("dup-in-batch")
	dupSecond.MaxPerUser = 3
	items := []model.ConnectionResource{
		keepOverwrite,             // id==0 + existing name → update by name
		sampleResource("new-one"), // create
		{ID: existing.ID, Name: "keep-renamed", Protocol: contract.VNCProtocol, Nodes: []string{"node-1"}, NodeGroups: []string{}, MaxPerUser: 1}, // update by id
		{ID: 99999, Name: "nope", Protocol: contract.VNCProtocol, Nodes: []string{"node-1"}, NodeGroups: []string{}, MaxPerUser: 1},               // missing id
		dupFirst,  // create
		dupSecond, // same name in batch → update (last wins)
	}
	out, err := svc.AdminBatchUpsertResources(ctx, items)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if out.Created != 2 { // new-one + first dup-in-batch
		t.Fatalf("created = %d, want 2", out.Created)
	}
	if out.Updated != 3 { // keep by name + keep-renamed by id + second dup-in-batch
		t.Fatalf("updated = %d, want 3", out.Updated)
	}
	if len(out.Failures) != 1 {
		t.Fatalf("failures = %d (%+v), want 1", len(out.Failures), out.Failures)
	}

	got, err := svc.AdminGetResourceByID(ctx, items[5].ID)
	if err != nil {
		t.Fatalf("get dup: %v", err)
	}
	if got.MaxPerUser != 3 {
		t.Fatalf("dup-in-batch MaxPerUser = %d, want 3 (last write)", got.MaxPerUser)
	}
}

func TestAdminBatchUpsertResources_EmptyRejected(t *testing.T) {
	svc, _ := newTestLaunchService(t)
	svc.settingsSvc = stubSettingsReader{}
	_, err := svc.AdminBatchUpsertResources(context.Background(), nil)
	if err == nil {
		t.Fatal("expected empty items error")
	}
	domain, ok := errors.AsType[contract.DomainError](err)
	if !ok || domain.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("err = %v, want HTTP 400", err)
	}
}
