package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

func newResourceRepoForTest(t *testing.T) (*Store, *ConnectionResourceRepo) {
	t.Helper()
	store, _ := newConnRepoForTest(t)
	return store, NewRepos(store).ConnectionResource
}

// Update 会整对象覆盖，但 created_at 是服务端字段，不能被客户端省略值清零。
func TestConnectionResourceUpdatePreservesCreatedAt(t *testing.T) {
	_, repo := newResourceRepoForTest(t)
	ctx := context.Background()

	res := &model.ConnectionResource{
		Name:       "res-created-at",
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{"node-1"},
		NodeGroups: []string{},
		MaxPerUser: 1,
	}
	if err := repo.Create(ctx, res); err != nil {
		t.Fatalf("create: %v", err)
	}
	before, err := repo.GetByID(ctx, res.ID)
	if err != nil {
		t.Fatalf("get before: %v", err)
	}

	// 模拟 PUT：客户端提交整对象，不携带 createdAt。
	update := &model.ConnectionResource{
		ID:         res.ID,
		Name:       res.Name,
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{"node-1"},
		NodeGroups: []string{},
		MaxPerUser: 2,
	}
	if err := repo.Update(ctx, update); err != nil {
		t.Fatalf("update: %v", err)
	}

	after, err := repo.GetByID(ctx, res.ID)
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	if after.CreatedAt.IsZero() || !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("createdAt not preserved: before=%v after=%v", before.CreatedAt, after.CreatedAt)
	}
	// handler 直接回显入参，因此传入的 struct 也要被回填。
	if update.CreatedAt.IsZero() || !update.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("update struct createdAt not backfilled: %v", update.CreatedAt)
	}
	// handler 直接回显入参，updatedAt 也必须反映本次更新。
	if update.UpdatedAt.IsZero() {
		t.Fatalf("update struct updatedAt not populated")
	}
	if after.MaxPerUser != 2 {
		t.Fatalf("maxPerUser not updated: %d", after.MaxPerUser)
	}
}

func TestConnectionResourceUpdateMissingID(t *testing.T) {
	_, repo := newResourceRepoForTest(t)
	err := repo.Update(context.Background(), &model.ConnectionResource{
		Name:       "missing",
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{"node-1"},
		MaxPerUser: 1,
	})
	if err == nil || !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
}

// 替换授权用户时重复 userID 应被去重，而不是触发复合主键冲突。
func TestConnectionResourceReplaceUsersDeduplicates(t *testing.T) {
	store, repo := newResourceRepoForTest(t)
	ctx := context.Background()

	res := &model.ConnectionResource{
		Name:       "res-replace-users",
		Protocol:   contract.SSHProtocol,
		Nodes:      []string{"node-1"},
		NodeGroups: []string{},
		MaxPerUser: 1,
	}
	if err := repo.Create(ctx, res); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.ReplaceUsers(ctx, res.ID, []uint{7, 7, 8}); err != nil {
		t.Fatalf("replace users with duplicate ids: %v", err)
	}
	var count int64
	if err := store.db.Model(&model.UserConnectionResource{}).
		Where("resource_id = ?", res.ID).Count(&count).Error; err != nil {
		t.Fatalf("count mappings: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 mappings after dedupe, got %d", count)
	}
}

// PatchUsers 增量授权：add 去重后批量插入，已存在映射由 ON CONFLICT DO NOTHING 跳过。
func TestConnectionResourcePatchUsersSkipsExisting(t *testing.T) {
	store, repo := newResourceRepoForTest(t)
	ctx := context.Background()

	res := &model.ConnectionResource{
		Name:       "res-patch-users",
		Protocol:   contract.SSHProtocol,
		Nodes:      []string{"node-1"},
		NodeGroups: []string{},
		MaxPerUser: 1,
	}
	if err := repo.Create(ctx, res); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.PatchUsers(ctx, res.ID, []uint{9, 9}, nil); err != nil {
		t.Fatalf("patch add: %v", err)
	}
	if err := repo.PatchUsers(ctx, res.ID, []uint{9}, nil); err != nil {
		t.Fatalf("patch add existing: %v", err)
	}
	var count int64
	if err := store.db.Model(&model.UserConnectionResource{}).
		Where("resource_id = ?", res.ID).Count(&count).Error; err != nil {
		t.Fatalf("count mappings: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 mapping after patch dedupe, got %d", count)
	}
}

// 复合主键 (user_id, resource_id) 无法高效支持仅按 resource_id 的查询，需单列索引。
func TestUserConnectionResourceHasResourceIDIndex(t *testing.T) {
	store, _ := newResourceRepoForTest(t)
	if !store.db.Migrator().HasIndex(&model.UserConnectionResource{}, "idx_ucr_resource_id") {
		t.Fatal("expected index idx_ucr_resource_id on user_connection_resources.resource_id")
	}
}

// 删除规格时，stopped/unknown 等历史实例也会阻止删除，避免实例失去 resource_id 归属。
func TestConnectionResourceDeleteCountsStoppedInstances(t *testing.T) {
	store, repo := newResourceRepoForTest(t)
	ctx := context.Background()

	res := &model.ConnectionResource{
		Name:       "res-delete-stopped",
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{"node-1"},
		NodeGroups: []string{},
		MaxPerUser: 1,
	}
	if err := repo.Create(ctx, res); err != nil {
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
	if err := store.db.WithContext(ctx).Create(&conn).Error; err != nil {
		t.Fatalf("create stopped connection: %v", err)
	}

	err := repo.Delete(ctx, res.ID)
	if !errors.Is(err, ErrResourceInUse) {
		t.Fatalf("err = %v, want ErrResourceInUse", err)
	}
	if !strings.Contains(err.Error(), "stopped and history") {
		t.Fatalf("err = %v, want message mentioning stopped/history instances", err)
	}
}
