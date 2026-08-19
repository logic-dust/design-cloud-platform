package execution_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
)

// TestNormalizePayload_RoundTrippedLauncherRequest 复现引擎 → launcher 的真实 JSON 往返。
//
// 引擎侧 Payload 为 json.RawMessage，序列化后 payload 以 JSON 对象上线；launcher 用
// json.Unmarshal 解到 any，必然得到 map[string]any。若不在解码边界收敛形态，
// GetPayload 会判为未知类型而拒绝请求，使所有携带 payload 的 launcher 动作失败。
func TestNormalizePayload_RoundTrippedLauncherRequest(t *testing.T) {
	t.Parallel()

	req := &execution.ExecuteRequest{
		TraceID:     "trace-roundtrip",
		ExecutionID: "exec-roundtrip",
		ActionName:  execution.ActionUserPasswordVerify,
		TargetUser:  "alice",
		Payload:     json.RawMessage(`{"username":"alice","password":"s3cret"}`),
	}

	wire, err := json.Marshal(execution.ExecuteInput{Version: "1.2.3", Token: "t", Request: req})
	if err != nil {
		t.Fatalf("marshal wire message: %v", err)
	}

	var decoded execution.ExecuteInput
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("decode wire message: %v", err)
	}

	// 前提断言：JSON 往返确实把 payload 退化成了 map，否则本测试没有覆盖到目标缺陷。
	if _, ok := decoded.Request.Payload.(map[string]any); !ok {
		t.Fatalf("precondition failed: decoded payload is %T, want map[string]any", decoded.Request.Payload)
	}

	if err := decoded.Request.NormalizePayload(); err != nil {
		t.Fatalf("normalize payload: %v", err)
	}

	got, err := execution.GetPayload[payload.UserPasswordVerify](decoded.Request)
	if err != nil {
		t.Fatalf("GetPayload after normalize: %v", err)
	}
	if got.Username != "alice" || got.Password != "s3cret" {
		t.Fatalf("payload = %+v, want username=alice password=s3cret", *got)
	}
}

// TestNormalizePayload_KeepsSupportedForms 确认已受支持的形态（含 nil）不被改写。
func TestNormalizePayload_KeepsSupportedForms(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"username":"bob","password":"pw"}`)

	tests := []struct {
		name    string
		payload any
	}{
		{name: "nil payload", payload: nil},
		{name: "json.RawMessage", payload: raw},
		{name: "[]byte", payload: []byte(`{"username":"bob"}`)},
		{name: "string", payload: `{"username":"bob"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := &execution.ExecuteRequest{ActionName: execution.ActionUserPasswordVerify, Payload: tc.payload}
			if err := req.NormalizePayload(); err != nil {
				t.Fatalf("normalize payload: %v", err)
			}

			// 用 DeepEqual 而非 ==：json.RawMessage / []byte 是切片，不可比较。
			if !reflect.DeepEqual(req.Payload, tc.payload) {
				t.Fatalf("payload was rewritten: got %T %#v, want %T %#v", req.Payload, req.Payload, tc.payload, tc.payload)
			}
		})
	}
}
