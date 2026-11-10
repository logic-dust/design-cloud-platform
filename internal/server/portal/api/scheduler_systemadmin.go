package api

import (
	"cmp"
	"errors"
	"net/http"

	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/scheduler"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/system/scheduler
func (h *schedulerHandler) adminSchedulerAPI(g *echo.Group) {
	g.GET("/hosts", h.listHosts)
	g.GET("/queues", h.listQueues)
	g.GET("/meta", h.getMeta)
	jobsGroup := g.Group("/jobs")
	jobsGroup.GET("", h.adminListJobs)
	jobsGroup.GET("/:id", h.adminGetJob)
	jobsGroup.POST("/:id/:op", h.adminUpdateJobStatus)
	jobsGroup.PATCH("/:id", h.adminModifyJob)
	jobsGroup.POST("/batch-control", h.adminBatchUpdateJobStatus)
}

// --- 获取所有任务（管理员）
func (h *schedulerHandler) adminListJobs(c *echo.Context) error {
	var userQuery scheduler.JobListQuery
	if err := echo.BindQueryParams(c, &userQuery); err != nil {
		return httputil.RespondError(c, err)
	}
	ctx := c.Request().Context()
	jobsInfo, err := h.scheduler.ListJobs(ctx, userQuery)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondOK(c, jobsInfo)
}

// --- 获取单个任务详情（管理员）
func (h *schedulerHandler) adminGetJob(c *echo.Context) error {
	jobInfo, err := h.scheduler.GetJob(c.Request().Context(), c.Param("id"))
	if err != nil {
		return respondJobQueryError(c, err)
	}
	return httputil.RespondOK(c, jobInfo)
}

// --- 更改指定用户的任务状态（管理员）POST /jobs/:id/:op
func (h *schedulerHandler) adminUpdateJobStatus(c *echo.Context) error {
	jobID := c.Param("id")
	op, ok := scheduler.ParseJobOp(c.Param("op"))
	if !ok {
		return httputil.RespondHTTPError(c, http.StatusBadRequest, "unsupported job operation: "+c.Param("op"))
	}
	var req struct {
		User        string `json:"user"`
		TargetQueue string `json:"targetQueue,omitempty"`
	}
	if err := echo.BindBody(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	user := cmp.Or(req.User, httputil.CurrentUsername(c))
	failures, err := h.scheduler.ControlJob(c.Request().Context(), scheduler.JobControlRequest{
		JobIDs:      []string{jobID},
		Operation:   op,
		RequestUser: user,
		TargetQueue: req.TargetQueue,
	})
	if err != nil {
		if errors.Is(err, scheduler.ErrUnsupportedJobControlOperation) {
			return httputil.RespondHTTPError(c, http.StatusBadRequest, err.Error())
		}
		return httputil.RespondError(c, err)
	}
	if len(failures) > 0 {
		return httputil.RespondHTTPError(c, http.StatusConflict, failures[jobID])
	}
	return c.NoContent(http.StatusAccepted)
}

// --- 管理员修改单个作业属性（PATCH /jobs/:id，仅 name；队列切换走 :op=switch）
func (h *schedulerHandler) adminModifyJob(c *echo.Context) error {
	jobID := c.Param("id")
	var req struct {
		Name *string `json:"name,omitempty"`
		User string  `json:"user"`
	}
	if err := echo.BindBody(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	runUser := cmp.Or(req.User, httputil.CurrentUsername(c))
	if err := h.scheduler.ModifyJob(c.Request().Context(), jobID, scheduler.JobModifyArgs{Name: req.Name}, runUser); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusAccepted)
}

// --- 批量更新任务状态（管理员）
func (h *schedulerHandler) adminBatchUpdateJobStatus(c *echo.Context) error {
	var req struct {
		JobIDs      []string        `json:"jobIds"`
		Operation   scheduler.JobOp `json:"operation"`
		User        string          `json:"user"`
		TargetQueue string          `json:"targetQueue,omitempty"`
	}
	if err := echo.BindBody(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	if len(req.JobIDs) == 0 {
		return httputil.RespondHTTPError(c, http.StatusBadRequest, "jobIds is required")
	}
	user := cmp.Or(req.User, httputil.CurrentUsername(c))
	failures, err := h.scheduler.ControlJob(c.Request().Context(), scheduler.JobControlRequest{
		JobIDs:      req.JobIDs,
		Operation:   req.Operation,
		RequestUser: user,
		TargetQueue: req.TargetQueue,
	})
	if err != nil {
		if errors.Is(err, scheduler.ErrUnsupportedJobControlOperation) {
			return httputil.RespondHTTPError(c, http.StatusBadRequest, err.Error())
		}
		return httputil.RespondError(c, err)
	}
	if len(failures) > 0 {
		return respondBatchJobFailures(c, failures)
	}
	return c.NoContent(http.StatusAccepted)
}
