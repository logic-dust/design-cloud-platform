package schema

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeDomainPayload_TopLevelPartial(t *testing.T) {
	t.Parallel()
	base := []byte(`{"a":1,"b":2,"c":"keep"}`)
	patch := []byte(`{"b":20,"d":40}`)

	got, err := MergeDomainPayload(base, patch)
	require.NoError(t, err)

	var obj map[string]any
	require.NoError(t, json.Unmarshal(got, &obj))
	require.Equal(t, float64(1), obj["a"], "未携带字段保持 base 原值")
	require.Equal(t, float64(20), obj["b"])
	require.Equal(t, "keep", obj["c"])
	require.Equal(t, float64(40), obj["d"], "新增字段被加入")
}

func TestMergeDomainPayload_NestedObjectsRecurse(t *testing.T) {
	t.Parallel()
	base := []byte(`{"website":{"title":"T","footer":"F"},"vnc":{"binDir":"/opt/bin","port":5900}}`)
	patch := []byte(`{"website":{"title":"T2"}}`)

	got, err := MergeDomainPayload(base, patch)
	require.NoError(t, err)

	var obj map[string]map[string]any
	require.NoError(t, json.Unmarshal(got, &obj))
	require.Equal(t, "T2", obj["website"]["title"], "嵌套字段被覆盖")
	require.Equal(t, "F", obj["website"]["footer"], "嵌套对象中未携带的字段保持原值")
	require.Equal(t, "/opt/bin", obj["vnc"]["binDir"], "未涉及的嵌套对象整体不动")
}

func TestMergeDomainPayload_ArraysReplaceWholesale(t *testing.T) {
	t.Parallel()
	base := []byte(`{"disabledModules":["a","b"],"n":1}`)
	patch := []byte(`{"disabledModules":["c"]}`)

	got, err := MergeDomainPayload(base, patch)
	require.NoError(t, err)

	var obj map[string]any
	require.NoError(t, json.Unmarshal(got, &obj))
	require.Equal(t, []any{"c"}, obj["disabledModules"], "数组整体替换，不做元素级合并")
	require.Equal(t, float64(1), obj["n"])
}

func TestMergeDomainPayload_EmptyBase(t *testing.T) {
	t.Parallel()
	got, err := MergeDomainPayload(nil, []byte(`{"a":1}`))
	require.NoError(t, err)

	var obj map[string]any
	require.NoError(t, json.Unmarshal(got, &obj))
	require.Equal(t, float64(1), obj["a"])
}

func TestMergeDomainPayload_RejectsMalformedPatch(t *testing.T) {
	t.Parallel()
	_, err := MergeDomainPayload([]byte(`{"a":1}`), []byte(`not-json`))
	require.Error(t, err)
}
