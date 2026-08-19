package api

import (
	"cmp"
	"errors"
	"net/http"
	"slices"

	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/scheduler"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/system/scheduler
func (h *schedulerHandler) adminSchedulerAPI(g *echo.Group) {
	jobsGroup := g.Group("/jobs")
	jobsGroup.GET("", h.adminListJobs)
	jobsGroup.GET("/:id", h.adminGetJob)
	jobsGroup.POST("/:id/:op", h.adminUpdateJobStatus)
	jobsGroup.PATCH("/:id", h.adminModifyJob)
	jobsGroup.POST("/batch-control", h.adminBatchUpdateJobStatus)
}

// --- 获取所有任务（管理员）
func (h *schedulerHandler) adminListJobs(c *echo.Context) error {
	var query scheduler.JobListQuery
	if err := c.Bind(&query); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	jobsInfo, err := h.scheduler.ListJobs(c.Request().Context(), query)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: jobsInfo})
}

// --- 获取单个任务详情（管理员）
func (h *schedulerHandler) adminGetJob(c *echo.Context) error {
	jobInfo, err := h.scheduler.GetJob(c.Request().Context(), c.Param("id"))
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: jobInfo})
}

// --- 更改指定用户的任务状态（管理员）POST /jobs/:id/:op
func (h *schedulerHandler) adminUpdateJobStatus(c *echo.Context) error {
	jobID := c.Param("id")
	if jobID == "0" {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "operation on job ID 0 is not allowed"})
	}
	op, ok := parseJobOp(c.Param("op"))
	if !ok {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "unsupported job operation: " + c.Param("op")})
	}
	var req struct {
		User        string `json:"user"`
		TargetQueue string `json:"targetQueue,omitempty"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
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
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
		}
		return httputil.RespondError(c, err)
	}
	if len(failures) > 0 {
		return c.JSON(http.StatusConflict, httputil.Response{Message: failures[jobID]})
	}
	return c.NoContent(http.StatusAccepted)
}

// --- 管理员修改单个作业属性（PATCH /jobs/:id，仅 name；队列切换走 :op=switch）
func (h *schedulerHandler) adminModifyJob(c *echo.Context) error {
	jobID := c.Param("id")
	if jobID == "0" {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "operation on job ID 0 is not allowed"})
	}
	var req struct {
		Name *string `json:"name,omitempty"`
		User string  `json:"user"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
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
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if len(req.JobIDs) == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "jobIds is required"})
	}
	if slices.Contains(req.JobIDs, "0") {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "operation on job ID 0 is not allowed"})
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
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
		}
		return httputil.RespondError(c, err)
	}
	if len(failures) > 0 {
		var batchFailures []batchJobControlFailure
		for id, msg := range failures {
			batchFailures = append(batchFailures, batchJobControlFailure{JobID: id, Message: msg})
		}
		return c.JSON(http.StatusConflict, httputil.Response{
			Data:    map[string]any{"failures": batchFailures},
			Message: "one or more jobs could not be updated",
		})
	}
	return c.NoContent(http.StatusAccepted)
}
