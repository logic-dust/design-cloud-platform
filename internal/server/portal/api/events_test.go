package api

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/push"

	"github.com/labstack/echo/v5"
)

// newTestEventsHandler 构造一个带静默 logger 的 eventsHandler（SSE 帧不走日志）。
func newTestEventsHandler(hub *push.Hub) *eventsHandler {
	return &eventsHandler{
		hub:    hub,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

type safeRecorder struct {
	*httptest.ResponseRecorder
	mu  sync.Mutex
	buf bytes.Buffer
}

func newSafeRecorder() *safeRecorder {
	return &safeRecorder{
		ResponseRecorder: httptest.NewRecorder(),
	}
}

func (r *safeRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf.Write(b)
	return r.ResponseRecorder.Write(b)
}

func (r *safeRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

// startEventsStream 在 goroutine 中启动 eventsStream，返回响应记录器与结束信号。
// 测试通过 cancel() 终止流（模拟客户端断开）。
func startEventsStream(t *testing.T, h *eventsHandler, hub *push.Hub) (*safeRecorder, context.CancelFunc, <-chan error) {
	t.Helper()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rec := newSafeRecorder()

	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	c := e.NewContext(req, rec)
	// 生产环境由 PolicySSE 链的 sessionAuthMW 注入 session；此处直接注入模拟该行为。
	c.Set(httputil.CtxSessionKey, &model.Session{UserID: 1, Username: "alice", Role: model.RoleNormal})

	done := make(chan error, 1)
	go func() { done <- h.eventsStream(c) }()

	return rec, cancel, done
}

// waitForBody 轮询响应体直到包含 want 或超时，规避 SSE 异步写入的竞态。
func waitForBody(t *testing.T, rec *safeRecorder, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for body to contain %q; body=%q", want, rec.String())
}

// TestEventsStreamInitialFrame 验证握手：SSE 头 + retry 帧 + Hub 注册。
func TestEventsStreamInitialFrame(t *testing.T) {
	t.Parallel()

	hub := push.NewHub()
	h := newTestEventsHandler(hub)
	rec, cancel, _ := startEventsStream(t, h, hub)
	defer cancel()

	// 1. 首帧 retry: 5000
	waitForBody(t, rec, "retry: 5000", 2*time.Second)

	// 2. 关键响应头
	if ct := rec.Header().Get(echo.HeaderContentType); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := rec.Header().Get(echo.HeaderCacheControl); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	if conn := rec.Header().Get(echo.HeaderConnection); conn != "keep-alive" {
		t.Errorf("Connection = %q, want keep-alive", conn)
	}
	if accel := rec.Header().Get("X-Accel-Buffering"); accel != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", accel)
	}

	// 3. 连接已注册到 Hub（userID "1"）
	if got := hub.ClientCount(); got != 1 {
		t.Fatalf("ClientCount = %d, want 1", got)
	}
}

// TestEventsStreamEventFrame 验证事件帧格式：id + event + data（含 JSON 载荷）。
func TestEventsStreamEventFrame(t *testing.T) {
	t.Parallel()

	hub := push.NewHub()
	h := newTestEventsHandler(hub)
	rec, cancel, _ := startEventsStream(t, h, hub)
	defer cancel()

	// 等握手完成，再发布事件，确保 Register 已就绪
	waitForBody(t, rec, "retry: 5000", 2*time.Second)

	// hub 按 username 路由（Register(sess.Username)）
	hub.PublishToUser(context.Background(), []string{"alice"}, push.Message{
		Name: push.EventJob,
		Payload: push.EventPayload{
			Action:   "refresh",
			TargetID: "job-abc",
			Notify: &push.Notification{
				Title:   "作业状态变更",
				Message: "模型训练作业已完成",
				Level:   "success",
			},
		},
	})

	waitForBody(t, rec, "event: job", 2*time.Second)
	body := rec.Body.String()

	// id 帧：全局单调递增，首个事件 id 为 1
	if !strings.Contains(body, "id: 1") {
		t.Errorf("expected id frame `id: 1`, body=%q", body)
	}
	// event 帧
	if !strings.Contains(body, "event: job") {
		t.Errorf("expected event frame `event: job`, body=%q", body)
	}
	// data 帧：EventPayload 统一契约（omitempty 压缩后的 JSON）
	for _, want := range []string{`"action":"refresh"`, `"targetId":"job-abc"`, `"level":"success"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected payload to contain %s, body=%q", want, body)
		}
	}
}

// TestEventsStreamUnregisterOnDisconnect 验证断连后客户端从 Hub 注销（无泄漏）。
func TestEventsStreamUnregisterOnDisconnect(t *testing.T) {
	t.Parallel()

	hub := push.NewHub()
	h := newTestEventsHandler(hub)
	_, cancel, done := startEventsStream(t, h, hub)

	// 连接已建立
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := hub.ClientCount(); got != 1 {
		t.Fatalf("ClientCount before disconnect = %d, want 1", got)
	}

	// 客户端断开（取消 context）→ handler 退出 → Unregister
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("eventsStream returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("eventsStream did not return after context cancel")
	}

	if got := hub.ClientCount(); got != 0 {
		t.Errorf("ClientCount after disconnect = %d, want 0", got)
	}
}
