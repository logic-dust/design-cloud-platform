package user

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/server/service/settings/schema"
	userprovider "design-cloud-engine/internal/server/service/user/provider"

	"github.com/stretchr/testify/require"
)

func TestResolveIDMappings_FoundAndNotFound(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)

	var userFetches atomic.Int32
	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, uids []uint32) (map[uint32]string, error) {
			userFetches.Add(1)
			out := make(map[uint32]string)
			for _, uid := range uids {
				if uid == 1000 {
					out[uid] = "levin"
				}
			}
			return out, nil
		},
		groupsByIDsFunc: func(_ context.Context, gids []uint32) (map[uint32]string, error) {
			out := make(map[uint32]string)
			for _, gid := range gids {
				if gid == 998 {
					out[gid] = "wheel"
				}
			}
			return out, nil
		},
	}

	res, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{
		UIDs: []uint32{1000, 10002},
		GIDs: []uint32{998, 9000},
	})
	require.NoError(t, err)
	require.Equal(t, map[uint32]string{1000: "levin"}, res.Users)
	require.Equal(t, map[uint32]string{998: "wheel"}, res.Groups)
	require.Equal(t, []uint32{10002}, res.NotFound.UIDs)
	require.Equal(t, []uint32{9000}, res.NotFound.GIDs)
	require.Equal(t, schema.IDMappingFoundCacheTTLDefault, res.TTL)
	require.Equal(t, schema.IDMappingNotFoundCacheTTLDefault, res.NegativeTTL)

	// Second call should hit cache (no extra provider fetch for users).
	before := userFetches.Load()
	res2, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{
		UIDs: []uint32{1000, 10002},
	})
	require.NoError(t, err)
	require.Equal(t, before, userFetches.Load())
	require.Equal(t, "levin", res2.Users[1000])
	require.Equal(t, []uint32{10002}, res2.NotFound.UIDs)
}

func TestResolveIDMappings_EmptyRequestSerializesEmptyCollections(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.localProv = &mockProvider{}

	res, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{})
	require.NoError(t, err)
	require.NotNil(t, res.Users, "empty maps must serialize as {} (not null)")
	require.NotNil(t, res.Groups)
	require.NotNil(t, res.NotFound.UIDs, "empty notFound lists must serialize as [] (not null)")
	require.NotNil(t, res.NotFound.GIDs)
	require.Empty(t, res.NotFound.UIDs)
	require.Empty(t, res.NotFound.GIDs)
}

// 空请求在平台检查之前短路：双源都没有时也是 200，不得因"无身份源"变成 503。
func TestResolveIDMappings_EmptyRequestDoesNotRequireSources(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.localProv = nil
	core.ldapProv = nil

	res, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Empty(t, res.Users)
	require.Empty(t, res.Groups)
}

func TestResolveIDMappings_RejectsTooMany(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.localProv = &mockProvider{}

	// 去重后超限（= MaxIDMappingsPerRequest + 1 个唯一 ID）→ 400
	uids := make([]uint32, MaxIDMappingsPerRequest+1)
	for i := range uids {
		uids[i] = uint32(10_000 + i)
	}
	_, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: uids})
	require.Error(t, err)

	// 去重前超限（= MaxRawIDMappingsPerArray + 1 个重复项）→ 400
	dupes := make([]uint32, MaxRawIDMappingsPerArray+1)
	_, err = core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: dupes})
	require.Error(t, err)

	// 原始刚好等于上限但去重后不超限（= MaxRawIDMappingsPerArray 个相同 ID）→ 允许
	dupes = make([]uint32, MaxRawIDMappingsPerArray)
	_, err = core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: dupes})
	require.NoError(t, err)
}

func TestResolveIDMappings_FailClosedOnProviderError(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)

	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return nil, errors.New("local exec failed")
		},
	}

	_, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: []uint32{1000}})
	require.Error(t, err)
	var domainErr common.DomainError
	require.ErrorAs(t, err, &domainErr)
	require.Equal(t, http.StatusServiceUnavailable, domainErr.HTTPStatus())
	// Must not have cached a notFound result.
	_, ok := core.idMappingCache.userCache.GetIfPresent(1000)
	require.False(t, ok)
}

func TestResolveIDMappings_StaleOnError(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)

	core.idMappingCache.userCache.SetWithTTL(1000, IDMapping{Name: "levin", Found: true}, time.Nanosecond)
	time.Sleep(time.Millisecond) // expire

	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, _ []uint32) (map[uint32]string, error) {
			return nil, errors.New("local exec failed")
		},
	}

	res, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: []uint32{1000}})
	require.NoError(t, err)
	require.Equal(t, "levin", res.Users[1000])
}

func TestRefreshIDMappingCache_Clears(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.localProv = &mockProvider{
		usersByIDsFunc: func(_ context.Context, uids []uint32) (map[uint32]string, error) {
			return map[uint32]string{1000: "levin"}, nil
		},
	}

	_, err := core.ResolveIDMappings(context.Background(), IDMappingsRequest{UIDs: []uint32{1000}})
	require.NoError(t, err)
	_, ok := core.idMappingCache.userCache.GetIfPresent(1000)
	require.True(t, ok)

	require.NoError(t, core.RefreshIDMappingCache(context.Background(), "admin"))
	_, ok = core.idMappingCache.userCache.GetIfPresent(1000)
	require.False(t, ok)
}

func TestInvalidateIDMappingFromSystemUsers(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.idMappingCache.userCache.SetWithTTL(1000, IDMapping{Name: "a", Found: true}, time.Hour)
	core.idMappingCache.groupCache.SetWithTTL(2000, IDMapping{Name: "g", Found: true}, time.Hour)

	core.invalidateIDMappingsFromSystemUsers([]userprovider.SystemUser{
		{Uid: "1000", Gid: "2000"},
	})
	_, ok := core.idMappingCache.userCache.GetIfPresent(1000)
	require.False(t, ok)
	_, ok = core.idMappingCache.groupCache.GetIfPresent(2000)
	require.False(t, ok)
}

// 设置读取失败不得被当成"TTL 改了"：基线不动、缓存不清、本次解析沿用上次成功
// 读到的 TTL 对（而不是代码默认值 86400s）。
//
// settingsSvc == nil 与"GetAuthSettings 返回 error"走的是同一条失败路径
// （都拿不到新配置），用它做确定性注入。修复前这里会：清空缓存、把基线改写成
// 默认值、并让本次回填按 86400s 存条目。
func TestIDMappingCacheTTLs_ReadFailureKeepsBaselineAndCache(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	core.settingsSvc = nil

	// 上次成功读取到的配置：700/600（均不同于 schema 默认值）
	require.False(t, core.idMappingCache.noteTTLs(700, 600), "首次观察不算变化")
	core.idMappingCache.userCache.SetWithTTL(1000, IDMapping{Name: "levin", Found: true}, time.Hour)

	ttls := core.idMappingCacheTTLs(context.Background())

	_, ok := core.idMappingCache.userCache.GetIfPresent(1000)
	require.True(t, ok, "settings read failure must not wipe the cache")

	pos, neg, known := core.idMappingCache.lastKnownTTLs()
	require.True(t, known)
	require.Equal(t, 700, pos, "baseline must not be overwritten by defaults")
	require.Equal(t, 600, neg, "baseline must not be overwritten by defaults")

	require.Equal(t, 700*time.Second, ttls.positive, "must reuse last known TTL, not the 86400s default")
	require.Equal(t, 600*time.Second, ttls.negative)
}
