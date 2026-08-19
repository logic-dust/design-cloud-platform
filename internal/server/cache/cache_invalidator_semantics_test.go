package cache

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInvalidator_RegisterAppendsCallbacks 锁定"同一主题多回调累加"的语义。
//
// 回归背景：Register 曾按主题名覆盖写入 map，导致同一主题的第二个注册者
// 悄悄顶掉第一个。典型受害者是 settings 主题——settings.Service 注册的
// "清空域缓存"被 service.go 注册的 "Security 派生缓存失效" 覆盖，
// 使跨节点 settings 变更实际不生效（只在多节点部署暴露）。
func TestInvalidator_RegisterAppendsCallbacks(t *testing.T) {
	t.Parallel()
	inv := NewInvalidator(nil, slog.Default())

	var first, second int
	inv.Register("settings", func() { first++ })
	inv.Register("settings", func() { second++ })

	reg, ok := inv.registered["settings"]
	require.True(t, ok, "topic must be registered")
	require.Len(t, reg.callbacks, 2, "same topic must accumulate callbacks")

	for _, cb := range reg.callbacks {
		cb()
	}
	require.Equal(t, 1, first)
	require.Equal(t, 1, second)
}

// TestInvalidator_RegisterSeparateTopics 不同主题各自独立。
func TestInvalidator_RegisterSeparateTopics(t *testing.T) {
	t.Parallel()
	inv := NewInvalidator(nil, slog.Default())

	inv.Register("session", func() {})
	inv.Register("ban", func() {})
	inv.Register("id_mappings", func() {})

	require.Len(t, inv.registered, 3)
	require.Len(t, inv.registered["session"].callbacks, 1)
	require.Len(t, inv.registered["ban"].callbacks, 1)
	require.Len(t, inv.registered["id_mappings"].callbacks, 1)
}
