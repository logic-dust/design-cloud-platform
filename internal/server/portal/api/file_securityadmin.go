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
			return httputil.RespondHTTPError(c, http.StatusBadRequest, err.Error())
		}
		status = parsed
	}
	downloads, err := h.file.ListDownloadRequests(c.Request().Context(), status, c.QueryParam("requester"))
	if err != nil {
		return handleFileError(c, err)
	}
	return httputil.RespondOK(c, downloads)
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
	return httputil.RespondOK(c, download)
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
	if err := echo.BindBody(c, &body); err != nil {
		return httputil.RespondError(c, err)
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
		return httputil.RespondHTTPError(c, http.StatusBadRequest, "decision must be approve or reject")
	}
	if reviewErr != nil {
		return handleFileError(c, reviewErr)
	}
	return httputil.RespondOK(c, download)
}
