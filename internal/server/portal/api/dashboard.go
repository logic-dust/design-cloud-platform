package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/dashboard"

	"github.com/labstack/echo/v5"
)

type dashboardHandler struct {
	svc                *dashboard.Service
	isDashboardEnabled func(ctx context.Context) bool
}

func newDashboardHandler(svc *dashboard.Service, isDashboardEnabled func(ctx context.Context) bool) *dashboardHandler {
	return &dashboardHandler{
		svc:                svc,
		isDashboardEnabled: isDashboardEnabled,
	}
}

// route: /api/dashboard
func (h *dashboardHandler) dashboardAPI(g *echo.Group) {
	g.GET("", h.getDashboard)
}

func (h *dashboardHandler) getDashboard(c *echo.Context) error {
	ctx := c.Request().Context()
	if h.isDashboardEnabled != nil && !h.isDashboardEnabled(ctx) {
		return httputil.RespondError(c, common.ErrNotFound)
	}

	// 当请求参数包含 stream=true 时提供 SSE 实时事件流
	if c.QueryParam("stream") == "true" {
		return h.streamDashboard(c)
	}

	dash, err := h.svc.GetDashboard(ctx)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{
		Data: dash,
	})
}

func (h *dashboardHandler) streamDashboard(c *echo.Context) error {
	ctx := c.Request().Context()

	flusher, err := httputil.InitSSEResponse(c.Response(), 0)
	if err != nil {
		return err
	}

	// 建连瞬间立即推送第一帧快照
	var lastFingerprint string
	if dash, err := h.svc.GetDashboard(ctx); err == nil {
		writeDashboardFrame(c.Response(), dash)
		if flusher != nil {
			flusher.Flush()
		}
		lastFingerprint = dashboardFingerprint(dash)
	}

	// 订阅 dashboard 刷新通知：数据刷新完成（可能变化）才推帧，无轮询。
	// 首个订阅者启动后台刷新循环，最后断开时循环停止（无人订阅不刷新）。
	notifyCh, unsubscribe := h.svc.Subscribe()
	defer unsubscribe()

	// 心跳 ticker 仅保活连接（防代理超时断连），不驱动数据。
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-heartbeat.C:
			// SSE 注释行心跳，前端忽略；仅维持连接活跃
			_, _ = fmt.Fprint(c.Response(), ": ping\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		case <-notifyCh:
			dash, err := h.svc.GetDashboard(ctx)
			if err != nil {
				continue
			}
			// 有变化才推完整帧（刷新完成但数值未变时跳过）
			fp := dashboardFingerprint(dash)
			if fp == lastFingerprint {
				continue
			}
			lastFingerprint = fp
			writeDashboardFrame(c.Response(), dash)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// writeDashboardFrame 向 SSE 连接写入一帧 dashboard 数据。
// 帧内携带 version（=config.VERSION，引擎版本）：前端收到帧时对比本地记录版本，
// 不同则整页刷新——engine 更新必然重启→SSE 断连→重连首帧即带新版本，
// 大屏秒级感知新代码，无需额外 fetch /api/version。
func writeDashboardFrame(w io.Writer, dash dashboard.Response) {
	payload, _ := json.Marshal(struct {
		Data    dashboard.Response `json:"data"`
		Version string             `json:"version"`
	}{Data: dash, Version: config.VERSION})
	_, _ = fmt.Fprintf(w, "event: dashboard\ndata: %s\n\n", payload)
}

// dashboardFingerprint 计算 dashboard 数据的指纹：各数据块采样时间。
// 任一数据块被刷新（sampledAt 变化）即视为内容变化，触发完整帧推送。
// 使用 UnixNano（而非 %v 格式化 time.Time）以避免时区/单调时钟差异导致误判。
func dashboardFingerprint(dash dashboard.Response) string {
	return fmt.Sprintf("%d|%d|%d|%d",
		dash.NodeSampledAt.UnixNano(),
		dash.ResourceSampledAt.UnixNano(),
		dash.SchedulerSampledAt.UnixNano(),
		dash.StorageSampledAt.UnixNano())
}
