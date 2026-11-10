package settings

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
)

// TestBanAllowlistMutations 覆盖放行名单独立读改写：追加/幂等/非法条目整次拒绝/移除/清空，
// 以及局部变更不触碰 auth 域其他字段（merge 正确性）。
func TestBanAllowlistMutations(t *testing.T) {
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	// 前置：修改 sessionTTL，用于验证后续放行名单变更不覆盖域内其他字段。
	if err := svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"sessionTTL":7200}`)); err != nil {
		t.Fatalf("seed sessionTTL: %v", err)
	}

	// 1. 追加两个条目
	updated, err := svc.AddBanAllowlistEntries(ctx, []string{"192.0.2.10", "10.0.0.0/8"})
	if err != nil {
		t.Fatalf("add entries: %v", err)
	}
	if len(updated.BanAllowlist) != 2 {
		t.Fatalf("expected 2 entries, got %v", updated.BanAllowlist)
	}
	if updated.SessionTTL != 7200 {
		t.Errorf("sessionTTL clobbered by allowlist mutation: %d", updated.SessionTTL)
	}

	// 2. 重读一致性（持久化生效，非仅内存返回值）
	current, err := svc.GetAuthSettings(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(current.BanAllowlist) != 2 {
		t.Fatalf("expected persisted 2 entries, got %v", current.BanAllowlist)
	}

	// 3. 重复追加幂等：列表不变
	updated, err = svc.AddBanAllowlistEntries(ctx, []string{"192.0.2.10"})
	if err != nil {
		t.Fatalf("idempotent add: %v", err)
	}
	if len(updated.BanAllowlist) != 2 {
		t.Errorf("expected duplicate add to be a no-op, got %v", updated.BanAllowlist)
	}

	// 4. 非法条目：整次变更被拒绝（contract.NewValidationError, 422），合法条目也不生效
	_, err = svc.AddBanAllowlistEntries(ctx, []string{"198.51.100.1", "not-an-ip"})
	if err == nil {
		t.Errorf("expected error for invalid entry, got nil")
	} else {
		if contractErr, ok := errors.AsType[contract.DomainError](err); !ok {
			t.Errorf("expected contract.DomainError, got %T: %v", err, err)
		} else if contractErr.HTTPStatus() != http.StatusUnprocessableEntity {
			t.Errorf("expected 422 for invalid entry, got %d: %v", contractErr.HTTPStatus(), err)
		}
	}
	current, err = svc.GetAuthSettings(ctx)
	if err != nil {
		t.Fatalf("reload after rejected add: %v", err)
	}
	if len(current.BanAllowlist) != 2 {
		t.Errorf("expected rejected add to leave allowlist untouched, got %v", current.BanAllowlist)
	}

	// 5. 移除存在的条目
	updated, err = svc.RemoveBanAllowlistEntries(ctx, []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("remove entry: %v", err)
	}
	if len(updated.BanAllowlist) != 1 || updated.BanAllowlist[0] != "192.0.2.10" {
		t.Errorf("expected [192.0.2.10], got %v", updated.BanAllowlist)
	}

	// 6. 移除不存在的条目：幂等 no-op
	updated, err = svc.RemoveBanAllowlistEntries(ctx, []string{"203.0.113.9"})
	if err != nil {
		t.Fatalf("idempotent remove: %v", err)
	}
	if len(updated.BanAllowlist) != 1 {
		t.Errorf("expected no-op remove to keep 1 entry, got %v", updated.BanAllowlist)
	}

	// 7. 清空：剩余条目全部移除后得到空列表（而非 nil）
	updated, err = svc.RemoveBanAllowlistEntries(ctx, []string{"192.0.2.10"})
	if err != nil {
		t.Fatalf("remove last entry: %v", err)
	}
	if updated.BanAllowlist == nil || len(updated.BanAllowlist) != 0 {
		t.Errorf("expected empty (non-nil) allowlist, got %#v", updated.BanAllowlist)
	}
}
