package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"

	"design-cloud-engine/internal/contract"
	"github.com/labstack/echo/v5"
)

type auditLogService interface {
	ReadAuditLogTail(name string, tail, page int) (audit.LogTailResponse, error)
	LogPath(name string) (string, bool)
	ListExecutions(ctx context.Context, filter model.ExecutionListFilter) (*audit.ExecutionListResult, error)
	GetExecutionByExecutionID(ctx context.Context, executionID string) (*audit.ExecutionRecordView, error)
	FollowLogTail(ctx context.Context, name string, onLine func(string) error) error
}

type auditHandler struct {
	audit auditLogService
}

func newAuditHandler(auditSvc auditLogService) *auditHandler {
	return &auditHandler{audit: auditSvc}
}

// route: /api/admin/audit
func (h *auditHandler) auditAdminAPI(g *echo.Group) {
	g.GET("/logs/:name", h.getAuditLogTail)
	g.GET("/logs/:name/stream", h.streamAuditLogRoute)
	g.GET("/executions", h.listExecutions)
	g.GET("/executions/:executionId", h.getExecutionByExecutionID)
}

func isAuditLogName(name string) bool {
	return name == "audit" || name == "security"
}

// --- GET /logs/:name/stream：SSE 长连接，tail-follow 从文件末尾推送新行
func (h *auditHandler) streamAuditLogRoute(c *echo.Context) error {
	name := c.Param("name")
	if !isAuditLogName(name) {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "unknown audit log file: " + name})
	}
	return h.streamAuditLog(c, name)
}

func (h *auditHandler) getAuditLogTail(c *echo.Context) error {
	name := c.Param("name")
	if !isAuditLogName(name) {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "unknown audit log file: " + name})
	}

	tail, err := httputil.ParseOptionalIntQuery(c, "tail")
	if err != nil {
		return err
	}

	page, err := httputil.ParseOptionalIntQuery(c, "page")
	if err != nil {
		return err
	}

	result, err := h.audit.ReadAuditLogTail(name, tail, page)
	if err != nil {
		return c.JSON(http.StatusNotFound, httputil.Response{Message: err.Error()})
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}

func (h *auditHandler) listExecutions(c *echo.Context) error {
	filter := model.ExecutionListFilter{
		ServerNode: c.QueryParam("serverNode"),
		ActionName: c.QueryParam("actionName"),
	}

	if statusStr := c.QueryParam("status"); statusStr != "" {
		status, err := parseExecutionStatus(statusStr)
		if err != nil {
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
		}
		filter.Status = &status
	}

	if from, ok, err := parseOptionalTimeQuery(c, "from"); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	} else if ok {
		filter.From = &from
	}
	if to, ok, err := parseOptionalTimeQuery(c, "to"); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	} else if ok {
		filter.To = &to
	}

	page, err := httputil.ParseOptionalListPage(c)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			return c.JSON(he.Code, httputil.Response{Message: fmt.Sprint(he.Message)})
		}
		return httputil.RespondError(c, err)
	}
	filter.Limit = page.Limit
	filter.Offset = page.Offset

	result, err := h.audit.ListExecutions(c.Request().Context(), filter)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}

func (h *auditHandler) getExecutionByExecutionID(c *echo.Context) error {
	executionID := c.Param("executionId")
	if executionID == "" {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "missing [executionId] parameter"})
	}

	view, err := h.audit.GetExecutionByExecutionID(c.Request().Context(), executionID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return c.JSON(http.StatusNotFound, httputil.Response{Message: err.Error()})
		}
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: view})
}

func parseExecutionStatus(s string) (model.ExecutionStatus, error) {
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid status: %s", s)
	}
	status := model.ExecutionStatus(v)
	switch status {
	case model.ExecutionStatusPending, model.ExecutionStatusDispatched,
		model.ExecutionStatusSucceeded, model.ExecutionStatusFailed,
		model.ExecutionStatusTimedOut, model.ExecutionStatusCancelled:
		return status, nil
	default:
		return 0, fmt.Errorf("unknown execution status: %s", s)
	}
}

// parseOptionalTimeQuery parses RFC3339 timestamps from query parameters.
// Returns (time.Time{}, false, nil) when the parameter is missing.
func parseOptionalTimeQuery(c *echo.Context, name string) (time.Time, bool, error) {
	raw := c.QueryParam(name)
	if raw == "" {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("invalid %s timestamp: %s", name, raw)
	}
	return t, true, nil
}

// streamAuditLog serves an SSE tail-follow stream for audit/security logs.
func (h *auditHandler) streamAuditLog(c *echo.Context, name string) error {
	w := c.Response()
	flusher, err := httputil.InitSSEResponse(w, 5)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(c.Request().Context())
	defer cancel()

	// 心跳保活：在日志静默时每 15s 发送一次 : ping\n\n，防止反向代理因空闲超时断开长连接
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				_, _ = fmt.Fprintf(w, ": ping\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}()

	err = h.audit.FollowLogTail(ctx, name, func(line string) error {
		_, writeErr := fmt.Fprintf(w, "data: %s\n\n", line)
		if flusher != nil {
			flusher.Flush()
		}
		return writeErr
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		return c.JSON(http.StatusServiceUnavailable, httputil.Response{Message: err.Error()})
	}
	return nil
}
