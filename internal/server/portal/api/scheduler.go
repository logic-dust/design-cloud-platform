package api

import (
	"errors"
	"net/http"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/scheduler"

	"github.com/labstack/echo/v5"
)

type schedulerHandler struct {
	scheduler *scheduler.Service
}

func newSchedulerHandler(schedulerSvc *scheduler.Service) *schedulerHandler {
	return &schedulerHandler{scheduler: schedulerSvc}
}

// schedulerJobsBasePath 是 portal 调度作业路由前缀。
// appHandler 在启动应用时用它构造作业状态回跳 URL（Location header）。
const schedulerJobsBasePath = "/api/scheduler/jobs/"

// route: /api/scheduler
func (h *schedulerHandler) schedulerAPI(g *echo.Group) {
	// --- hosts & queues (read-only scheduler metadata)
	g.GET("/hosts", h.listHosts)
	g.GET("/queues", h.listQueues)
	g.GET("/meta", h.getMeta)

	// --- jobs
	jobsGroup := g.Group("/jobs")
	jobsGroup.GET("", h.listJobs)
	jobsGroup.GET("/me", h.listJobsMe)
	jobsGroup.GET("/:id", h.getJob)
	jobsGroup.POST("/:id/:op", h.updateJobStatus)
	jobsGroup.PATCH("/:id", h.modifyJob)
	jobsGroup.POST("/batch-control", h.batchUpdateJobStatus)
}

// --- 获取当前 HPC 调度器元数据（推荐列、核心字段映射、状态动作集合）；随 settings.scheduler.type 变化
func (h *schedulerHandler) getMeta(c *echo.Context) error {
	meta, err := h.scheduler.Meta(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: meta})
}

// --- 获取 HPC 调度集群计算主机列表与状态汇总
func (h *schedulerHandler) listHosts(c *echo.Context) error {
	hostsInfo, err := h.scheduler.ListHosts(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: hostsInfo})
}

// --- 获取 HPC 调度队列（分区）作业数汇总
func (h *schedulerHandler) listQueues(c *echo.Context) error {
	queuesInfo, err := h.scheduler.ListQueues(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: queuesInfo})
}

// --- GET /jobs：全集群、所有用户的作业列表（任何已登录用户可见；与调度器原生可见性一致）。
func (h *schedulerHandler) listJobs(c *echo.Context) error {
	jobsInfo, err := h.scheduler.ListJobs(c.Request().Context(), scheduler.JobListQuery{})
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: jobsInfo})
}

// --- GET /jobs/me：当前登录用户的作业（全集群列表见 GET /jobs；管理员接口可按 user/queue/node 过滤）。
func (h *schedulerHandler) listJobsMe(c *echo.Context) error {
	username := httputil.CurrentUsername(c)
	if username == "" {
		return httputil.RespondError(c, contract.NewUnauthorizedError("session required"))
	}
	jobsInfo, err := h.scheduler.ListJobs(c.Request().Context(), scheduler.JobListQuery{User: username})
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: jobsInfo})
}

// --- 根据任务ID获取任务详情
func (h *schedulerHandler) getJob(c *echo.Context) error {
	// jobID：调度器作业号（正整数），由 scheduler.ValidateJobID 统一校验。
	jobInfo, err := h.scheduler.GetJob(c.Request().Context(), c.Param("id"))
	if err != nil {
		return respondJobQueryError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: jobInfo})
}

func respondJobQueryError(c *echo.Context, err error) error {
	if errors.Is(err, scheduler.ErrJobNotFound) {
		return c.JSON(http.StatusNotFound, httputil.Response{Message: scheduler.ErrJobNotFound.Error()})
	}
	return httputil.RespondError(c, err)
}

// --- 更新单个任务状态（POST /jobs/:id/:op，op 为 kill/suspend/resume/toTop/toBottom/switch）
func (h *schedulerHandler) updateJobStatus(c *echo.Context) error {
	jobID := c.Param("id")
	op, ok := scheduler.ParseJobOp(c.Param("op"))
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
