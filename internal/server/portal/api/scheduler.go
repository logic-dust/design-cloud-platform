package api

import (
	"cmp"
	"errors"
	"net/http"
	"slices"

	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service"
	"design-cloud-engine/internal/server/service/scheduler"

	"github.com/labstack/echo/v5"
)

type schedulerHandler struct {
	scheduler *scheduler.Service
}

func newSchedulerHandler(svc *service.Container) *schedulerHandler {
	return &schedulerHandler{scheduler: svc.Scheduler}
}

// schedulerJobsBasePath 是 portal 调度作业路由前缀。
// appHandler 在启动应用时用它构造作业状态回跳 URL（Location header）。
const schedulerJobsBasePath = "/api/scheduler/jobs/"

// parseJobOp 将 URL 尾部的动作动词映射为调度器操作码（白名单）。
// 无效动词返回 ("", false)，由 handler 拒绝为 400。
func parseJobOp(s string) (scheduler.JobOp, bool) {
	switch s {
	case "kill":
		return scheduler.JobOpKill, true
	case "suspend":
		return scheduler.JobOpSuspend, true
	case "resume":
		return scheduler.JobOpResume, true
	case "top":
		return scheduler.JobOpToTop, true
	case "bottom":
		return scheduler.JobOpToBottom, true
	case "switch":
		return scheduler.JobOpSwitch, true
	default:
		return "", false
	}
}

// route: /api/scheduler
func (h *schedulerHandler) schedulerAPI(g *echo.Group) {
	// --- hosts
	g.GET("/hosts", h.listHosts)

	// --- jobs
	jobsGroup := g.Group("/jobs")
	jobsGroup.GET("", h.listJobs)
	jobsGroup.GET("/:id", h.getJob)
	jobsGroup.POST("/:id/:op", h.updateJobStatus)
	jobsGroup.PATCH("/:id", h.modifyJob)
	jobsGroup.POST("/batch-control", h.batchUpdateJobStatus)
}

// --- 获取 HPC 调度集群计算主机列表与状态汇总
func (h *schedulerHandler) listHosts(c *echo.Context) error {
	hostsInfo, err := h.scheduler.ListHosts(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: hostsInfo})
}

// --- 获取作业列表，?user=xxx 可查指定用户，不传则默认当前用户。
// 跨用户查询是有意开放的（与集群侧 bjobs -u <user> 的能力一致，Portal 不额外
// 收窄），因此此处不做"仅限自身"的限制，勿按越权修改。
// 注意：该 user 会直接参与 jobCache 的 key，键空间由请求决定，故缓存设有条目硬顶。
func (h *schedulerHandler) listJobs(c *echo.Context) error {
	var query scheduler.JobListQuery
	if err := c.Bind(&query); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	query.User = cmp.Or(query.User, httputil.CurrentUsername(c))
	jobsInfo, err := h.scheduler.ListJobs(c.Request().Context(), query)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: jobsInfo})
}

// --- 根据任务ID获取任务详情
func (h *schedulerHandler) getJob(c *echo.Context) error {
	// Note: scheduler jobs use string IDs (e.g. UUIDs or numeric strings),
	// but currently the service uses string for jobID.
	// If it was uint, we would use parseUintParam.
	jobInfo, err := h.scheduler.GetJob(c.Request().Context(), c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "job not found"})
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: jobInfo})
}

// --- 更新单个任务状态（POST /jobs/:id/:op，op 为 kill/suspend/resume/top/bottom/switch）
func (h *schedulerHandler) updateJobStatus(c *echo.Context) error {
	jobID := c.Param("id")
	if jobID == "0" {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "operation on job ID 0 is not allowed"})
	}
	op, ok := parseJobOp(c.Param("op"))
	if !ok {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "unsupported job operation: " + c.Param("op")})
	}
	var req struct {
		TargetQueue string `json:"targetQueue,omitempty"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	failures, err := h.scheduler.ControlJob(c.Request().Context(), scheduler.JobControlRequest{
		JobIDs:      []string{jobID},
		Operation:   op,
		RequestUser: httputil.CurrentUsername(c),
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

// --- 修改单个作业属性（PATCH /jobs/:id，仅 name；队列切换走 :op=switch）
func (h *schedulerHandler) modifyJob(c *echo.Context) error {
	jobID := c.Param("id")
	if jobID == "0" {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "operation on job ID 0 is not allowed"})
	}
	var req struct {
		Name *string `json:"name,omitempty"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if err := h.scheduler.ModifyJob(c.Request().Context(), jobID, scheduler.JobModifyArgs{Name: req.Name}, httputil.CurrentUsername(c)); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusAccepted)
}

type batchJobControlRequest struct {
	JobIDs      []string        `json:"jobIds"`
	Operation   scheduler.JobOp `json:"operation"`
	TargetQueue string          `json:"targetQueue,omitempty"`
}

type batchJobControlFailure struct {
	JobID   string `json:"jobId"`
	Message string `json:"message"`
}

// --- 批量更新任务状态
func (h *schedulerHandler) batchUpdateJobStatus(c *echo.Context) error {
	var req batchJobControlRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if len(req.JobIDs) == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "jobIds is required"})
	}
	if slices.Contains(req.JobIDs, "0") {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "operation on job ID 0 is not allowed"})
	}
	failures, err := h.scheduler.ControlJob(c.Request().Context(), scheduler.JobControlRequest{
		JobIDs:      req.JobIDs,
		Operation:   req.Operation,
		RequestUser: httputil.CurrentUsername(c),
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
