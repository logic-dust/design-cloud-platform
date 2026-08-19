package alert

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/push"
)

func newTestEvaluator(t *testing.T) (*Evaluator, *push.Client, *push.Hub) {
	t.Helper()
	hub := push.NewHub()
	client := hub.Register("admin-1", model.RoleSystemAdmin)
	t.Cleanup(func() { hub.Unregister(client) })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := NewEvaluator(hub, nil, logger)
	return e, client, hub
}

// mustReceive 断言客户端收到一条 EventSystem notify 通知并返回其 Notification。
func mustReceive(t *testing.T, client *push.Client) push.Notification {
	t.Helper()
	select {
	case msg := <-client.Ch:
		if msg.Name != push.EventSystem {
			t.Fatalf("event name = %q, want %q", msg.Name, push.EventSystem)
		}
		payload, ok := msg.Payload.(push.EventPayload)
		if !ok {
			t.Fatalf("payload type = %T, want push.EventPayload", msg.Payload)
		}
		if payload.Action != "notify" {
			t.Fatalf("action = %q, want notify", payload.Action)
		}
		if payload.Notify == nil {
			t.Fatal("notify must be a pointer (omitempty 对 struct 值无效)")
		}
		return *payload.Notify
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for alert notification")
		return push.Notification{}
	}
}

func mustNotReceive(t *testing.T, client *push.Client) {
	t.Helper()
	select {
	case msg := <-client.Ch:
		t.Fatalf("unexpected message received: %+v", msg)
	default:
		// correct: no notification expected
	}
}

func TestEvalFireAndRecover(t *testing.T) {
	e, client, _ := newTestEvaluator(t)
	ctx := context.Background()
	rule := Rule{Threshold: 90, Cooldown: time.Hour, Level: "warning"}

	// 初次触发：metric >= threshold → alert_state=fire
	e.Eval(ctx, "node.n1.memory", 95.5, rule)
	notify := mustReceive(t, client)
	if notify.Level != "warning" {
		t.Errorf("fire level = %q, want warning", notify.Level)
	}

	// 恢复：metric < threshold → alert_state=recover
	e.Eval(ctx, "node.n1.memory", 50, rule)
	notify = mustReceive(t, client)
	if notify.Level != "info" {
		t.Errorf("recover level = %q, want info", notify.Level)
	}
}

func TestEvalCooldownSuppressesRepeat(t *testing.T) {
	e, client, _ := newTestEvaluator(t)
	ctx := context.Background()
	rule := Rule{Threshold: 90, Cooldown: time.Hour, Level: "warning"}

	e.Eval(ctx, "node.n1.memory", 95, rule)
	mustReceive(t, client)

	// 冷却期内再次触发：不重推
	e.Eval(ctx, "node.n1.memory", 96, rule)
	mustNotReceive(t, client)
}

func TestEvalRepeatAfterCooldown(t *testing.T) {
	e, client, _ := newTestEvaluator(t)
	ctx := context.Background()
	rule := Rule{Threshold: 90, Cooldown: 50 * time.Millisecond, Level: "error"}

	e.Eval(ctx, "node.n1.memory", 95, rule)
	mustReceive(t, client)

	// 超过冷却期后仍超标：按冷却节律重推
	time.Sleep(150 * time.Millisecond)
	e.Eval(ctx, "node.n1.memory", 97, rule)
	notify := mustReceive(t, client)
	if notify.Level != "error" {
		t.Errorf("repeat level = %q, want error", notify.Level)
	}
}

func TestEvalZeroThresholdDisabled(t *testing.T) {
	e, client, _ := newTestEvaluator(t)
	ctx := context.Background()

	// 阈值为 0：任何 metric 均不触发
	e.Eval(ctx, "node.n1.memory", 999, Rule{Threshold: 0, Cooldown: time.Second, Level: "warning"})
	mustNotReceive(t, client)
}

func TestEvalBelowDirection(t *testing.T) {
	e, client, _ := newTestEvaluator(t)
	ctx := context.Background()
	rule := Rule{Threshold: 100, Cooldown: time.Hour, Level: "error", Below: true}

	// Below=true：metric < threshold 时触发（如 minFree）
	e.Eval(ctx, "storage.share.free_kb", 50, rule)
	mustReceive(t, client)

	// 回到阈值之上：恢复
	e.Eval(ctx, "storage.share.free_kb", 500, rule)
	mustReceive(t, client)

	// Below=true 且 metric >= threshold 时不应触发
	e.Eval(ctx, "storage.share.free_kb", 150, rule)
	mustNotReceive(t, client)
}

func TestEvalHysteresisFlappingSuppression(t *testing.T) {
	e, client, _ := newTestEvaluator(t)
	ctx := context.Background()
	rule := Rule{Threshold: 90, Cooldown: time.Hour, Level: "warning", HysteresisMargin: 0.05}
	// 5% margin on 90 -> 90 * 0.95 = 85.5 recovery threshold

	// 1. 初次触发 (91.0 >= 90) → fire
	e.Eval(ctx, "node.n1.memory", 91.0, rule)
	fireNotify := mustReceive(t, client)
	if fireNotify.Level != "warning" {
		t.Fatalf("fire level = %q, want warning", fireNotify.Level)
	}

	// 2. 指标轻微回落到阈值下方 (89.0 < 90)，但仍在迟滞死区 (89.0 >= 85.5)：
	// 状态机应继续保持 active，不误发 recover 通知，也不重推 fire
	e.Eval(ctx, "node.n1.memory", 89.0, rule)
	mustNotReceive(t, client)

	// 3. 指标再次微幅上冲到 90.2：
	// 由于此前处于 active 且未过 Cooldown，应被 Cooldown 抑制，不作为全新告警重推
	e.Eval(ctx, "node.n1.memory", 90.2, rule)
	mustNotReceive(t, client)

	// 4. 指标真正大幅回落至安全裕度以下 (84.0 < 85.5) → recover
	e.Eval(ctx, "node.n1.memory", 84.0, rule)
	recoverNotify := mustReceive(t, client)
	if recoverNotify.Level != "info" {
		t.Fatalf("recover level = %q, want info", recoverNotify.Level)
	}

	// 5. 恢复后指标回到 88.0（低于初次触发阈值 90）：不应误触发
	e.Eval(ctx, "node.n1.memory", 88.0, rule)
	mustNotReceive(t, client)

	// 6. 再次攀升至 92.0 >= 90：再次 fire
	e.Eval(ctx, "node.n1.memory", 92.0, rule)
	mustReceive(t, client)
}

func TestSanitizeKey(t *testing.T) {
	cases := map[string]string{
		"/":             "",
		"/share":        "share",
		"/mnt/data":     "mnt_data",
		"/mnt/data dir": "mnt_data_dir",
		"":              "",
	}
	for in, want := range cases {
		if got := sanitizeKey(in); got != want {
			t.Errorf("sanitizeKey(%q) = %q, want %q", in, got, want)
		}
	}
}
