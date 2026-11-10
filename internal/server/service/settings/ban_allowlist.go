package settings

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"log/slog"
	"slices"

	"design-cloud-engine/internal/contract/schema"
)

// 封禁放行名单的局部变更入口。
//
// AuthSettings.BanAllowlist 的通用更新走 UpdateByDomain（整域 JSON PUT），
// 但安全运维场景需要"追加/移除单个条目"的原子操作，避免整域读-改-写在
// 服务层之外进行（并发 PUT 相互覆盖）。本文件的读改写与 UpdateByDomain
// 共用 auth 域互斥锁，校验/持久化/失效/广播复用同一条写路径。

// AddBanAllowlistEntries 向放行名单追加条目（已存在则跳过，幂等）。
// 条目合法性由域写路径统一校验：非法条目返回包装 contract.NewValidationError
// 的错误，且整次变更不生效。返回更新后的 auth settings。
func (s *Service) AddBanAllowlistEntries(ctx context.Context, entries []string) (schema.AuthSettings, error) {
	return s.mutateBanAllowlist(ctx, "BAN_ALLOWLIST_ADD", entries, func(next []string) []string {
		for _, e := range entries {
			if !slices.Contains(next, e) {
				next = append(next, e)
			}
		}
		return next
	})
}

// RemoveBanAllowlistEntries 从放行名单移除条目（不存在则跳过，幂等）。
// 返回更新后的 auth settings。
func (s *Service) RemoveBanAllowlistEntries(ctx context.Context, entries []string) (schema.AuthSettings, error) {
	return s.mutateBanAllowlist(ctx, "BAN_ALLOWLIST_REMOVE", entries, func(next []string) []string {
		for _, e := range entries {
			next = slices.DeleteFunc(next, func(v string) bool { return v == e })
		}
		return next
	})
}

// mutateBanAllowlist 在持有 auth 域互斥锁的前提下完成读-改-写：
// apply 基于放行名单当前条目计算新列表；放行名单未变化时直接短路返回，
// 不触发持久化与缓存版本递增。
func (s *Service) mutateBanAllowlist(ctx context.Context, auditAction string, entries []string, apply func(next []string) []string) (schema.AuthSettings, error) {
	mu := s.getDomainMutex(schema.DomainAuth)
	mu.Lock()
	defer mu.Unlock()

	current, err := s.GetAuthSettings(ctx)
	if err != nil {
		return schema.AuthSettings{}, fmt.Errorf("load auth settings: %w", err)
	}

	// Clone 避免 append/DeleteFunc 触碰缓存对象的底层数组。
	next := apply(slices.Clone(current.BanAllowlist))
	if next == nil {
		next = []string{}
	}
	if slices.Equal(current.BanAllowlist, next) {
		return current, nil
	}

	patch, err := json.Marshal(map[string]any{"banAllowlist": next})
	if err != nil {
		return schema.AuthSettings{}, fmt.Errorf("marshal ban allowlist patch: %w", err)
	}
	if err := s.updateByDomainLocked(ctx, schema.DomainAuth, patch); err != nil {
		return schema.AuthSettings{}, err
	}

	s.audit.LogSecurity(ctx, auditAction, "success", slog.LevelInfo, map[string]any{
		"domain":   schema.DomainAuth.String(),
		"previous": current.BanAllowlist,
		"current":  next,
		"entries":  entries,
	})

	// 域写已精确失效本域缓存，重读拿到库中权威值。
	updated, err := s.GetAuthSettings(ctx)
	if err != nil {
		return schema.AuthSettings{}, fmt.Errorf("reload auth settings: %w", err)
	}
	return updated, nil
}
