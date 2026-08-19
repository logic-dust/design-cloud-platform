package settings

import (
	"context"
	"encoding/json"
	"io/fs"
	"testing"

	"design-cloud-engine/internal/server/service/settings/schema"

	"github.com/stretchr/testify/require"
)

// PUT 语义：叠加（局部更新）。
//   - 未携带的字段保持库中现值（而不是被解码成零值覆盖）；
//   - 携带的字段严格校验，越界 → 整次修改被拒绝，不产生部分生效。
func TestUpdateByDomain_PartialPayloadKeepsExistingValues(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	// 1. 先改一个字段
	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"ipBanDuration":7200}`)))
	got, err := svc.GetAuthSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, 7200, got.IPBanDuration)
	require.Equal(t, 86400, got.UserSyncInterval, "未携带字段应为 schema 默认值")

	// 2. 只改另一个字段：先前改过的值必须保留
	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"maxUserLoginAttempts":9}`)))
	got, err = svc.GetAuthSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, 9, got.MaxUserLoginAttempts)
	require.Equal(t, 7200, got.IPBanDuration, "未携带字段必须保持库中现值，不得被清零")

	// 3. 越界 → 拒绝，且不留副作用
	err = svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"ipBanDuration":1}`))
	require.ErrorIs(t, err, fs.ErrInvalid)
	got, err = svc.GetAuthSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, 7200, got.IPBanDuration, "被拒绝的修改不得部分生效")

	// 4. 跨字段规则同样在叠加后的整体上判定
	err = svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"idMappingNotFoundCacheTTL":999999}`))
	require.ErrorIs(t, err, fs.ErrInvalid)
}

// 显式给 0（allowzero 字段）仍然生效——叠加的判定依据是"键是否出现在 payload"。
func TestUpdateByDomain_ExplicitZeroIsApplied(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"userSyncInterval":3600}`)))
	got, err := svc.GetAuthSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, 3600, got.UserSyncInterval)

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"userSyncInterval":0}`)))
	got, err = svc.GetAuthSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, got.UserSyncInterval, "显式传 0 表示禁用，必须生效")
}

// 未知字段仍被拒绝（严格性不因叠加而放松）。
func TestUpdateByDomain_RejectsUnknownField(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	err := svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"identityFoundCacheTTL":86400}`))
	require.Error(t, err, "旧字段名/未知字段必须被拒绝")
}

// 带 omitempty + allowzero 的字段：显式 0 **能通过校验、能被基线保留**，但写库序列化
// 会因 omitempty 省略该键，所以端到端表现与"未携带"一致——两者都回退默认值。
//
// 锁定这个语义，是为了避免"以为 0 与未携带不同"的误解；对这类字段，消费方必须把 0 当
// 作"用默认"（例如 connectionListCacheTTL=0 → 回退缓存级默认 TTL）。
func TestUpdateByDomain_ExplicitZeroOnOmitemptyFieldEqualsAbsent(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	// 显式传 0：allowzero 字段合法，不得 422
	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainConnection,
		[]byte(`{"connectionListCacheTTL":0}`)))

	got, err := svc.GetConnectionSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, schema.ConnectionListCacheTTLDefault, got.ConnectionListCacheTTL,
		"omitempty 字段的显式 0 与未携带等效，均回退默认值")

	// 记录机制：库中不含该键（写库时的 struct Marshal 因 omitempty 省略）
	var stored json.RawMessage
	require.NoError(t, repo.Load(ctx, schema.DomainConnection.String(), &stored))
	require.NotContains(t, string(stored), "connectionListCacheTTL")

	// 与默认值等价：显式改成默认值也不该产生差异
	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainConnection,
		[]byte(`{"connectionListCacheTTL":60}`)))
	got, err = svc.GetConnectionSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, schema.ConnectionListCacheTTLDefault, got.ConnectionListCacheTTL)
}

func TestUpdateByDomain_TunnelPartialMergeAndRejectLegacy(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	// 1. Partial update: only turn off RDP
	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainTunnel, []byte(`{"rdpEnabled":false}`)))
	got, err := svc.GetTunnelSettings(ctx)
	require.NoError(t, err)
	require.False(t, got.RdpEnabled, "rdpEnabled should be set to false")
	require.True(t, got.VncEnabled, "vncEnabled should remain true from defaults")
	require.True(t, got.SshEnabled, "sshEnabled should remain true from defaults")
	require.True(t, got.Enabled, "enabled should remain true from defaults")

	// 2. Reject legacy field allowedProtocols
	err = svc.UpdateByDomain(ctx, schema.DomainTunnel, []byte(`{"allowedProtocols":["vnc"]}`))
	require.Error(t, err, "legacy allowedProtocols field must be rejected")
}

func TestUpdateByDomain_DesktopPartialMergeAndRejectLegacy(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSettingsServiceForTest(t)
	ctx := context.Background()

	// 1. Partial update: only turn off Linux
	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainDesktop, []byte(`{"linuxEnabled":false}`)))
	got, err := svc.GetDesktopSettings(ctx)
	require.NoError(t, err)
	require.False(t, got.LinuxEnabled, "linuxEnabled should be set to false")
	require.True(t, got.WindowsEnabled, "windowsEnabled should remain true from defaults")
	require.True(t, got.MacosEnabled, "macosEnabled should remain true from defaults")
	require.True(t, got.ReleasesEnabled, "releasesEnabled should remain true from defaults")

	// 2. Reject legacy field platforms
	err = svc.UpdateByDomain(ctx, schema.DomainDesktop, []byte(`{"platforms":["windows"]}`))
	require.Error(t, err, "legacy platforms field must be rejected")
}
