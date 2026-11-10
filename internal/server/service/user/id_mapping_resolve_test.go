package user

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/server/service/settings"

	"design-cloud-engine/internal/contract"
	"github.com/stretchr/testify/require"
)

// updateAuthSettings writes a full valid auth domain payload (settings updates are PUT-like).
func updateAuthSettings(t *testing.T, settingsSvc *settings.Service, pos, neg int) error {
	t.Helper()
	body := fmt.Sprintf(`{
		"sessionTTL": 86400,
		"maxSessionsPerUser": 1,
		"maxUserLoginAttempts": 7,
		"maxIPLoginAttempts": 15,
		"userBanDuration": 300,
		"ipBanDuration": 86400,
		"idMappingFoundCacheTTL": %d,
		"idMappingNotFoundCacheTTL": %d
	}`, pos, neg)
	return settingsSvc.UpdateByDomain(context.Background(), schema.DomainAuth, []byte(body))
}

func idMappingFetchersFor(t *testing.T, core *Service, users bool) (local, ldapF func(context.Context, []uint32) (map[uint32]string, error)) {
	t.Helper()
	return idMappingFetchers(core.localProv, core.ldapProv, users)
}

// --- §3.1 合并语义：LDAP 覆盖本机；仅本机有的保留本机名；双源确认才写 notFound。

func TestResolveNamespace_LDAPOverridesLocalAndFillsGaps(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return map[uint32]string{1000: "local-alice", 1001: "local-only"}, nil
		},
	}
	core.ldapProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return map[uint32]string{1000: "ldap-bob"}, nil
		},
	}

	res, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: []uint32{1000, 1001, 1002}})
	require.NoError(t, err)
	require.Equal(t, map[uint32]string{1000: "ldap-bob", 1001: "local-only"}, res.Users)
	require.Equal(t, []uint32{1002}, res.NotFound.UIDs)
}

// --- 失败回退与 TTL 类别：正常的 merged 结果用正 TTL；LDAP 故障时的本机兜底用 degraded 短 TTL。

func TestResolveNamespace_DegradedUsesShortTTL(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return map[uint32]string{1000: "local-alice"}, nil
		},
	}
	core.ldapProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return nil, errors.New("ldap down")
		},
	}
	ttls := idMappingTTLs{positive: time.Hour, negative: time.Hour, degraded: 20 * time.Millisecond}
	local, ldapF := idMappingFetchersFor(t, core, true)

	out, err := core.resolveNamespace(context.Background(), namespaceUser, []uint32{1000}, ttls, local, ldapF)
	require.NoError(t, err)
	require.Equal(t, "local-alice", out[1000].Name)

	_, ok := core.idMappingCache.userCache.GetIfPresent(1000)
	require.True(t, ok, "degraded result must be written back")
	time.Sleep(30 * time.Millisecond)
	_, ok = core.idMappingCache.userCache.GetIfPresent(1000)
	require.False(t, ok, "degraded result must expire quickly (short TTL), not use the positive TTL")
}

func TestResolveNamespace_NormalResultUsesPositiveTTL(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return map[uint32]string{1000: "local-only"}, nil
		},
	}
	core.ldapProv = &mockProvider{} // LDAP 成功且无命中：本机名即最终结果（非降级）
	ttls := idMappingTTLs{positive: 20 * time.Millisecond, negative: time.Hour, degraded: time.Hour}
	local, ldapF := idMappingFetchersFor(t, core, true)

	out, err := core.resolveNamespace(context.Background(), namespaceUser, []uint32{1000}, ttls, local, ldapF)
	require.NoError(t, err)
	require.Equal(t, "local-only", out[1000].Name)

	time.Sleep(30 * time.Millisecond)
	_, ok := core.idMappingCache.userCache.GetIfPresent(1000)
	require.False(t, ok, "merged result should use the positive TTL (which is short here)")
}

// --- 失败回退优先级：stale 正缓存 > 本机命中。

func TestResolveNamespace_StalePositiveBeatsLocalHit(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.idMappingCache.userCache.SetWithTTL(1000, IDMapping{Name: "stale-name", Found: true}, time.Nanosecond)
	time.Sleep(time.Millisecond) // expire

	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return map[uint32]string{1000: "local-name"}, nil
		},
	}
	core.ldapProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return nil, errors.New("ldap down")
		},
	}
	ttls := idMappingTTLs{positive: time.Hour, negative: time.Hour, degraded: time.Hour}
	local, ldapF := idMappingFetchersFor(t, core, true)

	out, err := core.resolveNamespace(context.Background(), namespaceUser, []uint32{1000}, ttls, local, ldapF)
	require.NoError(t, err)
	require.Equal(t, "stale-name", out[1000].Name, "stale positive cache must win over the local fallback")
	if v, ok := core.idMappingCache.userCache.GetIfPresent(1000); ok {
		require.Equal(t, "stale-name", v.Name, "fallback result is rewritten with the chosen name")
	}
}

// --- stale 负缓存绝不能充当 notFound（契约：notFound 仅表示"已确认不存在"）。

func TestResolveNamespace_StaleNegativeNeverBecomesNotFound(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.idMappingCache.userCache.SetWithTTL(1000, IDMapping{Found: false}, time.Nanosecond)
	time.Sleep(time.Millisecond) // expire

	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return nil, errors.New("local exec failed")
		},
	}
	ttls := idMappingTTLs{positive: time.Hour, negative: time.Hour, degraded: time.Hour}
	local, ldapF := idMappingFetchersFor(t, core, true)

	_, err := core.resolveNamespace(context.Background(), namespaceUser, []uint32{1000}, ttls, local, ldapF)
	require.Error(t, err)
	var domainErr contract.DomainError
	require.ErrorAs(t, err, &domainErr)
	require.Equal(t, http.StatusServiceUnavailable, domainErr.HTTPStatus())
}

// --- 代次守卫：查询开始后发生 Clear → 丢弃回填，但本次响应仍自洽。

func TestResolveNamespace_GenerationGuardDiscardsFill(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)

	entered := make(chan struct{})
	release := make(chan struct{})
	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			close(entered)
			<-release
			return map[uint32]string{1000: "levin"}, nil
		},
	}
	ttls := idMappingTTLs{positive: time.Hour, negative: time.Hour, degraded: time.Hour}
	local, _ := idMappingFetchersFor(t, core, true)

	type outcome struct {
		out map[uint32]IDMapping
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		out, err := core.resolveNamespace(context.Background(), namespaceUser, []uint32{1000}, ttls, local, nil)
		done <- outcome{out, err}
	}()

	<-entered
	core.idMappingCache.Clear() // 模拟 refresh / TTL 配置变更
	close(release)

	oc := <-done
	require.NoError(t, oc.err)
	require.Equal(t, "levin", oc.out[1000].Name, "response stays consistent even when the fill is discarded")
	_, ok := core.idMappingCache.userCache.GetIfPresent(1000)
	require.False(t, ok, "fill must be discarded: query started before Clear")
}

// --- 容量：写入路径自生效（无需调用方预留），超限采样淘汰，且不因淘汰失败。

func TestResolveNamespace_CapacityEviction(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, uids []uint32) (map[uint32]string, error) {
			out := make(map[uint32]string, len(uids))
			for _, id := range uids {
				out[id] = fmt.Sprintf("u%d", id)
			}
			return out, nil
		},
	}
	core.idMappingCache.userCache.SetMaxEntries(10)
	ttls := idMappingTTLs{positive: time.Hour, negative: time.Hour, degraded: time.Hour}
	local, _ := idMappingFetchersFor(t, core, true)

	first := make([]uint32, 10)
	for i := range first {
		first[i] = uint32(i)
	}
	_, err := core.resolveNamespace(context.Background(), namespaceUser, first, ttls, local, nil)
	require.NoError(t, err)
	require.Equal(t, 10, core.idMappingCache.userCache.Len())

	second := []uint32{10, 11, 12}
	_, err = core.resolveNamespace(context.Background(), namespaceUser, second, ttls, local, nil)
	require.NoError(t, err)
	require.Equal(t, 10, core.idMappingCache.userCache.Len(), "cache must stay within max entries")

	_, ok := core.idMappingCache.userCache.GetIfPresent(12)
	require.True(t, ok, "latest batch must be present after eviction")
}

// --- 平台静态不可用（无本机 passwd/group）：LDAP-only 时缺 ID 即可写负缓存。

func TestResolveNamespace_StaticLocalUnavailableAllowsNegativeCache(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.localProv = nil
	var ldapFetches int
	core.ldapProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			ldapFetches++
			return map[uint32]string{1000: "ldap-user"}, nil
		},
	}

	res, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: []uint32{1000, 1002}})
	require.NoError(t, err)
	require.Equal(t, "ldap-user", res.Users[1000])
	require.Equal(t, []uint32{1002}, res.NotFound.UIDs)

	// 第二次：命中缓存（含负缓存），不再查 LDAP。
	res2, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: []uint32{1000, 1002}})
	require.NoError(t, err)
	require.Equal(t, 1, ldapFetches)
	require.Equal(t, []uint32{1002}, res2.NotFound.UIDs)
}

// --- 两个身份源都不存在（非 POSIX 平台 + Local 模式）→ 明确 503。

func TestResolveIDMappings_UnavailableWhenNoSources(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.localProv = nil
	core.ldapProv = nil

	_, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: []uint32{1000}})
	require.Error(t, err)
	var domainErr contract.DomainError
	require.ErrorAs(t, err, &domainErr)
	require.Equal(t, http.StatusServiceUnavailable, domainErr.HTTPStatus())
}

// --- TTL 配置：变更检测触发本机清空；跨字段写入校验。

func TestIDMappingCacheTTLs_ChangeDetection(t *testing.T) {
	t.Parallel()
	core, _, settingsSvc, _ := setupTestCore(t)
	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return map[uint32]string{1000: "levin"}, nil
		},
	}

	res, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: []uint32{1000}})
	require.NoError(t, err)
	require.Equal(t, schema.IDMappingFoundCacheTTLDefault, res.TTL)
	require.Equal(t, schema.IDMappingNotFoundCacheTTLDefault, res.NegativeTTL)
	_, cached := core.idMappingCache.userCache.GetIfPresent(1000)
	require.True(t, cached, "本次解析应已回填缓存")

	// 相同配置再次解析：不触发清空（条目仍在）。
	_, err = core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: []uint32{1000}})
	require.NoError(t, err)
	_, cached = core.idMappingCache.userCache.GetIfPresent(1000)
	require.True(t, cached, "配置未变时不得清空缓存")

	// 正 TTL 变更 → 本机清空（代次递增，条目被清） + 生效值更新。
	require.NoError(t, updateAuthSettings(t, settingsSvc, 700, 600))
	res, err = core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: []uint32{1000}})
	require.NoError(t, err)
	require.Equal(t, 700, res.TTL)
	require.Equal(t, 600, res.NegativeTTL)

	// 正 TTL 的最小值为 600，负 TTL 不能超过正 TTL。
	require.NoError(t, updateAuthSettings(t, settingsSvc, 600, 600))
	res, err = core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: []uint32{1000}})
	require.NoError(t, err)
	require.Equal(t, 600, res.TTL)
	require.Equal(t, 600, res.NegativeTTL)

	// 跨字段写入校验：负 TTL 显式大于正 TTL → 拒绝。
	require.Error(t, updateAuthSettings(t, settingsSvc, 700, 800))
}

// TTL 变更触发的本机清空必须真的丢掉旧条目（而不是只递增某个内部计数器）。
func TestIDMappingCacheTTLs_ChangeClearsCachedEntries(t *testing.T) {
	t.Parallel()
	core, _, settingsSvc, _ := setupTestCore(t)
	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return map[uint32]string{1000: "before"}, nil
		},
	}

	_, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: []uint32{1000}})
	require.NoError(t, err)
	before, ok := core.idMappingCache.userCache.GetIfPresent(1000)
	require.True(t, ok)
	require.Equal(t, "before", before.Name)

	require.NoError(t, updateAuthSettings(t, settingsSvc, 700, 600))
	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return map[uint32]string{1000: "after"}, nil
		},
	}
	res, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: []uint32{1000}})
	require.NoError(t, err)
	require.Equal(t, "after", res.Users[1000], "TTL 变更必须让旧条目失效，本次解析应回到源")
}

// --- TTL 配置变更只清本机，不得写 cache_versions。identity 广播来自 admin refresh 与成功的用户同步。

func TestIDMappingCacheTTLs_TTLChangeDoesNotBroadcast(t *testing.T) {
	t.Parallel()
	core, _, settingsSvc, _ := setupTestCore(t)
	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return map[uint32]string{1000: "levin"}, nil
		},
	}
	ctx := context.Background()

	before, err := core.cacheVersion.GetAll(ctx)
	require.NoError(t, err)

	// 触发 settings 驱动的 TTL 变更（各节点靠 settings 主题自行传播，无需广播）。
	require.NoError(t, updateAuthSettings(t, settingsSvc, 700, 600))
	res, err := core.ResolveIDMappings(ctx, IDMappingsRequest{UIDs: []uint32{1000}})
	require.NoError(t, err)
	require.Equal(t, 700, res.TTL, "new TTL must take effect on the local node")

	after, err := core.cacheVersion.GetAll(ctx)
	require.NoError(t, err)
	require.Equal(t, before[db.CacheVersionTopicIdentity], after[db.CacheVersionTopicIdentity],
		"TTL settings change must not bump identity")

	// 对照：管理员 refresh 必须广播。
	require.NoError(t, core.RefreshIDMappingCache(ctx, "admin"))
	refreshed, err := core.cacheVersion.GetAll(ctx)
	require.NoError(t, err)
	require.Equal(t, after[db.CacheVersionTopicIdentity]+1, refreshed[db.CacheVersionTopicIdentity],
		"admin refresh must bump the identity topic")
}

// --- 组命名空间与用户命名空间彼此独立。

func TestResolveIDMappings_NamespacesAreIndependent(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return map[uint32]string{998: "uid-998-name"}, nil
		},
		groupsByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return map[uint32]string{998: "wheel"}, nil
		},
	}

	res, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: []uint32{998}, GIDs: []uint32{998}})
	require.NoError(t, err)
	require.Equal(t, map[uint32]string{998: "uid-998-name"}, res.Users)
	require.Equal(t, map[uint32]string{998: "wheel"}, res.Groups)
}
