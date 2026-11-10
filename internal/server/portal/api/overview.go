package api

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"io"
	"net/http"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/application/overview"
	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
)

// overviewHandler 提供 /api/overview/{me,cluster} 数据接口。
// 大屏开关（platform.website.overviewEnabled）只作用于网页大屏：由
// overviewClusterStreamAuthMW 的访客 Token 分支与 /overview 页面路由判断，
// 已登录会话不受影响。
// overview.allowUserCluster（默认 true）控制普通用户是否可访问集群概览。
type overviewHandler struct {
	svc              *overview.Service
	allowUserCluster func(ctx context.Context) bool
}

func newOverviewHandler(svc *overview.Service, allowUserCluster func(ctx context.Context) bool) *overviewHandler {
	return &overviewHandler{svc: svc, allowUserCluster: allowUserCluster}
}

// route: /api/overview（PolicyUser：常规会话鉴权）
// 裸 GET /api/overview 显式 404（不提供数据）；数据仅 /me、/me/stream、/cluster。
// JSON 快照与 SSE 实时流分属不同路径；SSE 路由统一以 /stream 结尾。
func (h *overviewHandler) overviewUserAPI(g *echo.Group) {
	g.GET("", func(c *echo.Context) error {
		return echo.NewHTTPError(http.StatusNotFound, "not found")
	})
	g.GET("/me", h.getOverviewMe)
	g.GET("/me/stream", h.streamOverviewMe)
	g.GET("/cluster", h.getOverviewCluster)
}

// route: /api/overview/cluster/stream（PolicyOverviewClusterStream：登录会话或网页大屏访客 Token）
func (h *overviewHandler) overviewClusterStreamAPI(g *echo.Group) {
	g.GET("", h.streamOverviewCluster)
}

// getOverviewCluster 全集群快照。
func (h *overviewHandler) getOverviewCluster(c *echo.Context) error {
	if err := h.guardClusterAccess(c); err != nil {
		return httputil.RespondError(c, err)
	}
	return h.respondOverview(c, overview.JobsStatsCluster, "")
}

// getOverviewMe 个人概览快照（scheduler.jobs 仅统计当前用户；仅登录会话）。
func (h *overviewHandler) getOverviewMe(c *echo.Context) error {
	return h.respondOverview(c, overview.JobsStatsSessionUser, httputil.CurrentUsername(c))
}

// streamOverviewCluster 全集群实时流。
func (h *overviewHandler) streamOverviewCluster(c *echo.Context) error {
	if err := h.guardClusterAccess(c); err != nil {
		return httputil.RespondError(c, err)
	}
	return h.streamOverview(c, overview.JobsStatsCluster, "")
}

// streamOverviewMe 个人概览实时流（scheduler.jobs 仅统计当前用户）。
func (h *overviewHandler) streamOverviewMe(c *echo.Context) error {
	return h.streamOverview(c, overview.JobsStatsSessionUser, httputil.CurrentUsername(c))
}

// guardClusterAccess 限制普通用户对集群概览的访问。
// 访客 Token（无 session）已由 overviewClusterStreamAuthMW 放行，此处跳过；
// 管理员（role > Normal 或带 AdminScopes）始终放行；
// 普通用户受 overview.allowUserCluster 控制（默认允许）。
func (h *overviewHandler) guardClusterAccess(c *echo.Context) error {
	val := c.Get(httputil.CtxSessionKey)
	if val == nil {
		return nil
	}
	s, ok := val.(*model.Session)
	if !ok || s == nil {
		return nil
	}
	if s.Role > model.RoleNormal || s.AdminScopes != 0 {
		return nil
	}
	if h.allowUserCluster == nil || h.allowUserCluster(c.Request().Context()) {
		return nil
	}
	return echo.NewHTTPError(http.StatusForbidden, "cluster overview is disabled for ordinary users")
}

func (h *overviewHandler) respondOverview(c *echo.Context, jobsMode overview.JobsStatsMode, sessionUser string) error {
	resp, err := h.svc.GetOverview(c.Request().Context(), jobsMode, sessionUser)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondData(c, resp)
}

func (h *overviewHandler) streamOverview(c *echo.Context, jobsMode overview.JobsStatsMode, sessionUser string) error {
	ctx := c.Request().Context()

	flusher, err := httputil.InitSSEResponse(c.Response(), 0)
	if err != nil {
		return err
	}

	var lastFingerprint string
	if resp, err := h.svc.GetOverview(ctx, jobsMode, sessionUser); err == nil {
		writeOverviewFrame(c.Response(), resp)
		if flusher != nil {
			flusher.Flush()
		}
		lastFingerprint = overviewFingerprint(resp)
	}

	notifyCh, unsubscribe := h.svc.Subscribe()
	defer unsubscribe()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-heartbeat.C:
			_, _ = fmt.Fprint(c.Response(), ": ping\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		case <-notifyCh:
			resp, err := h.svc.GetOverview(ctx, jobsMode, sessionUser)
			if err != nil {
				continue
			}
			fp := overviewFingerprint(resp)
			if fp == lastFingerprint {
				continue
			}
			lastFingerprint = fp
			writeOverviewFrame(c.Response(), resp)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// writeOverviewFrame 向 SSE 连接写入一帧 overview 数据。
// 帧内携带 version（=config.VERSION，引擎版本）：前端收到帧时对比本地记录版本，
// 不同则整页刷新——engine 更新必然重启→SSE 断连→重连首帧即带新版本，
// 大屏秒级感知新代码，无需额外 fetch /api/version。
func writeOverviewFrame(w io.Writer, resp overview.Response) {
	payload, _ := json.Marshal(struct {
		Data    overview.Response `json:"data"`
		Version string            `json:"version"`
	}{Data: resp, Version: config.VERSION})
	_, _ = fmt.Fprintf(w, "event: overview\ndata: %s\n\n", payload)
}

// overviewFingerprint 计算 overview 数据的指纹：各数据块采样时间。
// 任一数据块被刷新（sampledAt 变化）即视为内容变化，触发完整帧推送。
// 使用 UnixNano（而非 %v 格式化 time.Time）以避免时区/单调时钟差异导致误判。
func overviewFingerprint(resp overview.Response) string {
	return fmt.Sprintf("%d|%d|%d|%d",
		resp.NodeSampledAt.UnixNano(),
		resp.MetricsSampledAt.UnixNano(),
		resp.SchedulerSampledAt.UnixNano(),
		resp.StorageSampledAt.UnixNano())
}
