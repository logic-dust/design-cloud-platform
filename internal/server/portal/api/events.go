package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/push"

	"github.com/labstack/echo/v5"
)

const heartbeatInterval = 15 * time.Second

// eventsHandler serves the global SSE notification stream.
type eventsHandler struct {
	hub    *push.Hub
	logger *slog.Logger
}

// GET /api/events
func (h *eventsHandler) eventsStreamAPI(g *echo.Group) {
	g.GET("", h.eventsStream)
}

// eventsStream is the global notification stream: one connection per logged-in
// client, kept alive with periodic heartbeats, routing events by UserID + Role.
func (h *eventsHandler) eventsStream(c *echo.Context) error {
	sess, err := httputil.RequireSession(c)
	if err != nil {
		return err
	}

	w := c.Response()
	flusher, err := httputil.InitSSEResponse(w, 5)
	if err != nil {
		return err
	}

	// hub 按 username 路由（业务唯一标识），与各服务推送寻址一致
	client := h.hub.Register(sess.Username, sess.Role)
	defer h.hub.Unregister(client)

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_, _ = fmt.Fprintf(w, ": ping\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		case msg, ok := <-client.Ch:
			if !ok {
				return nil
			}
			data, err := json.Marshal(msg.Payload)
			if err != nil {
				// 序列化失败：记录并跳过该事件，避免向客户端发送损坏的空帧
				h.logger.Warn("push: failed to marshal event payload",
					"event", msg.Name, "error", err)
				continue
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n",
				h.hub.NextID(), msg.Name, data)
			if flusher != nil {
				flusher.Flush()
			}
		case <-c.Request().Context().Done():
			return nil
		}
	}
}
