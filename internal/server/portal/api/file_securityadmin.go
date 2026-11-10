package api

import (
	"net/http"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/security/files
func (h *fileHandler) adminFilesAPI(g *echo.Group) {
	g.GET("/download-requests", h.adminListDownloadRequests)
	g.GET("/download-requests/:id", h.adminGetDownloadRequest)
	g.POST("/download-requests/:id/review", h.adminReviewDownloadRequest)
}

func (h *fileHandler) adminListDownloadRequests(c *echo.Context) error {
	var status model.DownloadRequestStatus
	if s := c.QueryParam("status"); s != "" {
		parsed, err := model.ParseDownloadRequestStatus(s)
		if err != nil {
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
		}
		status = parsed
	}
	downloads, err := h.file.ListDownloadRequests(c.Request().Context(), status, c.QueryParam("requester"))
	if err != nil {
		return handleFileError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: downloads})
}

func (h *fileHandler) adminGetDownloadRequest(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	download, err := h.file.GetDownloadRequest(c.Request().Context(), int(id), "")
	if err != nil {
		return handleFileError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: download})
}

func (h *fileHandler) adminReviewDownloadRequest(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	var (
		download  model.DownloadRequest
		reviewErr error
	)
	switch body.Decision {
	case "approve":
		download, reviewErr = h.file.ApproveDownloadRequest(c.Request().Context(), int(id), httputil.CurrentUsername(c))
	case "reject":
		download, reviewErr = h.file.RejectDownloadRequest(c.Request().Context(), int(id), httputil.CurrentUsername(c), body.Reason)
	default:
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "decision must be approve or reject"})
	}
	if reviewErr != nil {
		return handleFileError(c, reviewErr)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: download})
}
