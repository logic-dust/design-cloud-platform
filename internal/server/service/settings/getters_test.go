package settings

import (
	"context"
	"testing"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/model"

	"github.com/stretchr/testify/require"
)

// 读取侧校验：DB 中的值必须满足与写入路径同一套约束（含跨字段不变量）；
// 不满足则整域回退默认值，脏数据不得静默改变运行时行为。
//
// 校验放在 settings 读取层是唯一收口——消费方（ID 映射缓存 / Security /
// 用户同步 / 管理控制台）拿到的值必然合法，无需各自防御。
func TestGetAuthSettings_StoredValueValidation(t *testing.T) {
	t.Parallel()
	svc, _, _, store := newSettingsServiceForTest(t)
	ctx := context.Background()

	defaults, ok := schema.DefaultSettings(schema.DomainAuth)
	require.True(t, ok)

	setStored := func(value string) {
		t.Helper()
		require.NoError(t, store.DB().Model(&model.Setting{}).
			Where("domain = ?", schema.DomainAuth.String()).
			Update("value", value).Error)
		svc.cache.Clear() // 强制下次读取重新从 DB 加载
	}

	t.Run("合法值被保留", func(t *testing.T) {
		setStored(`{"sessionTTL":7200,"maxSessionsPerUser":3,"maxUserLoginAttempts":7,` +
			`"maxIPLoginAttempts":30,"userBanDuration":600,"ipBanDuration":7200,` +
			`"idMappingFoundCacheTTL":3600,"idMappingNotFoundCacheTTL":1200}`)

		got, err := svc.GetAuthSettings(ctx)
		require.NoError(t, err)
		require.Equal(t, 7200, got.SessionTTL)
		require.Equal(t, 3600, got.IDMappingFoundCacheTTL)
		require.Equal(t, 1200, got.IDMappingNotFoundCacheTTL)
	})

	t.Run("越过 min 约束", func(t *testing.T) {
		setStored(`{"sessionTTL":7200,"maxSessionsPerUser":3,"maxUserLoginAttempts":7,` +
			`"maxIPLoginAttempts":30,"userBanDuration":600,"ipBanDuration":7200,` +
			`"idMappingFoundCacheTTL":0,"idMappingNotFoundCacheTTL":600}`)

		got, err := svc.GetAuthSettings(ctx)
		require.NoError(t, err)
		require.Equal(t, defaults, got, "out-of-range stored value must fall back to defaults")
	})

	t.Run("越过 max 约束", func(t *testing.T) {
		setStored(`{"sessionTTL":7200,"maxSessionsPerUser":3,"maxUserLoginAttempts":7,` +
			`"maxIPLoginAttempts":30,"userBanDuration":600,"ipBanDuration":7200,` +
			`"idMappingFoundCacheTTL":99999999,"idMappingNotFoundCacheTTL":600}`)

		got, err := svc.GetAuthSettings(ctx)
		require.NoError(t, err)
		require.Equal(t, defaults, got, "out-of-range stored value must fall back to defaults")
	})

	t.Run("跨字段不变量被破坏", func(t *testing.T) {
		setStored(`{"sessionTTL":7200,"maxSessionsPerUser":3,"maxUserLoginAttempts":7,` +
			`"maxIPLoginAttempts":30,"userBanDuration":600,"ipBanDuration":7200,` +
			`"idMappingFoundCacheTTL":700,"idMappingNotFoundCacheTTL":900}`)

		got, err := svc.GetAuthSettings(ctx)
		require.NoError(t, err)
		require.Equal(t, defaults, got, "cross-field violation must fall back to defaults")
	})
}
