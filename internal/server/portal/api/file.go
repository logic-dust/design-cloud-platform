package api

import (
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service"
	"design-cloud-engine/internal/server/service/file"

	"github.com/labstack/echo/v5"
)

type fileHandler struct {
	file *file.Service
}

func newFileHandler(svc *service.Container) *fileHandler {
	return &fileHandler{file: svc.File}
}

func (h *fileHandler) requireFileEnabled(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if !h.file.IsEnabled(c.Request().Context()) {
			return httputil.RespondError(c, file.ErrFileDisabled)
		}
		return next(c)
	}
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

// route: /api/files
func (h *fileHandler) filesAPI(g *echo.Group) {
	g.Use(h.requireFileEnabled)

	g.GET("/locations", h.listFileLocations)
	g.GET("/locations/:name/contents", h.listLocationContent)
	g.GET("/locations/:name/contents/*", h.listLocationContent)
	g.POST("/locations/:name/contents", h.createDirectoryContent)
	g.POST("/locations/:name/contents/rename", h.renameLocationContent)
	g.POST("/locations/:name/contents/batch-delete", h.deleteLocationContent)

	// --- downloads
	g.POST("/download-requests", h.createDownloadRequest)
	g.POST("/download-tokens", h.createDownloadToken)
	g.GET("/download-requests", h.listOwnDownloadRequests)
	g.GET("/download-requests/:id", h.getOwnDownloadRequest)
	g.POST("/download-requests/:id/tokens", h.createDownloadTokenFromRequest)

	// --- trash
	h.fileTrashRoutes(g.Group("/trash"))
}

// route: /api/files (file transfer scope)
func (h *fileHandler) fileTransferAPI(g *echo.Group) {
	g.Use(h.requireFileEnabled)

	g.GET("/downloads/:token", h.download)
	g.PUT("/locations/:name/contents/*", h.upload)
}

// ---------------------------------------------------------------------------
// Error mapping
// ---------------------------------------------------------------------------

func handleFileError(c *echo.Context, err error) error {
	if errors.Is(err, file.ErrFileDisabled) {
		return httputil.RespondError(c, err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return c.JSON(http.StatusNotFound, httputil.Response{Message: err.Error()})
	}
	if errors.Is(err, fs.ErrPermission) {
		return c.JSON(http.StatusForbidden, httputil.Response{Message: err.Error()})
	}
	if errors.Is(err, fs.ErrInvalid) || errors.Is(err, file.ErrInvalidFilePath) || errors.Is(err, file.ErrInvalidDownloadStatus) {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if errors.Is(err, file.ErrUploadTooLarge) || errors.Is(err, file.ErrDownloadTooLarge) {
		return c.JSON(http.StatusRequestEntityTooLarge, httputil.Response{Message: err.Error()})
	}
	if errors.Is(err, file.ErrDownloadFileChanged) ||
		errors.Is(err, file.ErrDownloadApprovalRequired) ||
		errors.Is(err, file.ErrDownloadApprovalNotNeeded) ||
		errors.Is(err, file.ErrDownloadRequestConflict) {
		return c.JSON(http.StatusConflict, httputil.Response{Message: err.Error()})
	}
	if errors.Is(err, file.ErrDownloadRequestExpired) {
		return c.JSON(http.StatusGone, httputil.Response{Message: err.Error()})
	}
	if errors.Is(err, file.ErrInvalidDownloadToken) ||
		errors.Is(err, file.ErrNoDownloadPermission) ||
		errors.Is(err, file.ErrNotAllowDownload) ||
		errors.Is(err, file.ErrDownloadRequestNotApproved) ||
		errors.Is(err, file.ErrDownloadRequestRejected) {
		return c.JSON(http.StatusForbidden, httputil.Response{Message: err.Error()})
	}
	if errors.Is(err, file.ErrDownloadRequestNotFound) {
		return c.JSON(http.StatusNotFound, httputil.Response{Message: err.Error()})
	}
	return httputil.RespondError(c, err)
}

// ---------------------------------------------------------------------------
// File locations
// ---------------------------------------------------------------------------

func (h *fileHandler) listFileLocations(c *echo.Context) error {
	locations, err := h.file.ListLocations(c.Request().Context(), httputil.CurrentUsername(c))
	if err != nil {
		return handleFileError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: locations})
}

func (h *fileHandler) listLocationContent(c *echo.Context) error {
	requestUser := httputil.CurrentUsername(c)
	location := c.Param("name")
	apiPath := strings.TrimPrefix(c.Param("*"), "/")

	dirInfo, err := h.file.ListLocationDirPath(c.Request().Context(), location, apiPath, requestUser)
	if err != nil {
		return handleFileError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: dirInfo})
}

func (h *fileHandler) createDirectoryContent(c *echo.Context) error {
	var req struct {
		Name string `json:"name"`
		Path string `json:"path,omitempty"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	if err := h.file.CreateLocationDir(c.Request().Context(), c.Param("name"), req.Path, req.Name, httputil.CurrentUsername(c)); err != nil {
		return handleFileError(c, err)
	}
	return c.NoContent(http.StatusCreated)
}

func (h *fileHandler) renameLocationContent(c *echo.Context) error {
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if err := h.file.RenameLocationPath(c.Request().Context(), c.Param("name"), req.Path, req.Name, httputil.CurrentUsername(c)); err != nil {
		return handleFileError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *fileHandler) deleteLocationContent(c *echo.Context) error {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if err := h.file.DeleteLocationPaths(c.Request().Context(), c.Param("name"), req.Paths, httputil.CurrentUsername(c)); err != nil {
		return handleFileError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Downloads
// ---------------------------------------------------------------------------

func (h *fileHandler) createDownloadToken(c *echo.Context) error {
	var req struct {
		Location string `json:"location"`
		Path     string `json:"path"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	downloadToken, err := h.file.CreateAllowedLocationDownloadToken(c.Request().Context(), req.Location, req.Path, httputil.CurrentUsername(c))
	if err != nil {
		return handleFileError(c, err)
	}
	return c.JSON(http.StatusCreated, httputil.Response{Data: downloadTokenResponse(downloadToken)})
}

func (h *fileHandler) createDownloadRequest(c *echo.Context) error {
	var req struct {
		Location string `json:"location"`
		Path     string `json:"path"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	downloadRequest, err := h.file.SubmitLocationDownloadRequest(c.Request().Context(), req.Location, req.Path, httputil.CurrentUsername(c))
	if err != nil {
		return handleFileError(c, err)
	}
	return c.JSON(http.StatusAccepted, httputil.Response{Data: struct {
		DownloadRequestID uint                 `json:"downloadRequestId"`
		Status            model.DownloadStatus `json:"status"`
	}{
		DownloadRequestID: downloadRequest.ID,
		Status:            downloadRequest.Status,
	}})
}

func (h *fileHandler) listOwnDownloadRequests(c *echo.Context) error {
	var status model.DownloadStatus
	if s := c.QueryParam("status"); s != "" {
		parsed, err := model.ParseDownloadStatus(s)
		if err != nil {
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
		}
		status = parsed
	}
	downloads, err := h.file.ListDownloadRequests(
		c.Request().Context(),
		status,
		httputil.CurrentUsername(c))
	if err != nil {
		return handleFileError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: downloads})
}

func (h *fileHandler) getOwnDownloadRequest(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	download, err := h.file.GetDownloadRequest(c.Request().Context(), int(id), httputil.CurrentUsername(c))
	if err != nil {
		return handleFileError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: download})
}

func (h *fileHandler) createDownloadTokenFromRequest(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	token, err := h.file.CreateApprovedDownloadToken(c.Request().Context(), int(id), httputil.CurrentUsername(c))
	if err != nil {
		return handleFileError(c, err)
	}
	return c.JSON(http.StatusCreated, httputil.Response{Data: downloadTokenResponse(token)})
}

// ---------------------------------------------------------------------------
// File transfer (download stream / upload stream)
// ---------------------------------------------------------------------------

func (h *fileHandler) download(c *echo.Context) error {
	requestUser := httputil.CurrentUsername(c)
	token := c.Param("token")

	if token == "" {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "missing download token"})
	}

	downloadFilePath, downloadSize, err := h.file.GetAllowedDownloadFile(c.Request().Context(), token, requestUser)
	if err != nil {
		return handleFileError(c, err)
	}

	filename := filepath.Base(downloadFilePath)
	c.Response().Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	c.Response().Header().Set("Content-Type", "application/octet-stream")

	written, err := h.file.StreamDownload(c.Request().Context(), c.Response(), requestUser, downloadFilePath, downloadSize)
	if err != nil {
		slog.Error("stream download failed", "error", err, "user", requestUser, "path", downloadFilePath)
		if written == 0 {
			c.Response().Header().Del("Content-Disposition")
			c.Response().Header().Del("Content-Type")
			return handleFileError(c, err)
		}
	}
	return nil
}

func (h *fileHandler) upload(c *echo.Context) error {
	apiPath := strings.TrimPrefix(c.Param("*"), "/")
	if err := h.file.StreamUploadLocation(c.Request().Context(), uploadBodyReader(c.Request()), httputil.CurrentUsername(c), c.Param("name"), apiPath, c.Request().ContentLength); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return c.JSON(http.StatusForbidden, httputil.Response{Message: err.Error()})
		}
		return handleFileError(c, err)
	}
	return c.NoContent(http.StatusCreated)
}

func downloadTokenResponse(token file.DownloadToken) any {
	return struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt,omitempty"`
	}{
		Token:     token.Token,
		ExpiresAt: token.ExpiresAt.Format(time.RFC3339),
	}
}

type limitReadCloser struct {
	io.Reader
	closer io.Closer
}

func (l *limitReadCloser) Close() error { return l.closer.Close() }

func uploadBodyReader(req *http.Request) io.ReadCloser {
	if req.ContentLength >= 0 {
		return &limitReadCloser{
			Reader: io.LimitReader(req.Body, req.ContentLength),
			closer: req.Body,
		}
	}
	return req.Body
}

// ---------------------------------------------------------------------------
// Trash
// ---------------------------------------------------------------------------

func (h *fileHandler) fileTrashRoutes(g *echo.Group) {
	g.GET("/contents", h.listTrash)
	g.GET("/contents/*", h.listTrash)
	g.POST("/items", h.moveFilesToTrash)
	g.POST("/items/batch-delete", h.deleteTrashItems)
	g.DELETE("/items/:trashName", h.deleteTrashItem)
	g.DELETE("/items", h.emptyTrash)
}

func (h *fileHandler) moveFilesToTrash(c *echo.Context) error {
	var info struct {
		Location string   `json:"location"`
		Paths    []string `json:"paths"`
	}
	if err := c.Bind(&info); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if info.Location == "" || len(info.Paths) == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "location and paths are required"})
	}
	if err := h.file.MoveLocationPathsToTrash(c.Request().Context(), info.Location, info.Paths, httputil.CurrentUsername(c)); err != nil {
		return handleFileError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *fileHandler) deleteTrashItem(c *echo.Context) error {
	trashName, err := url.PathUnescape(c.Param("trashName"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "invalid trash name"})
	}
	if err := h.file.DeleteTrashItem(c.Request().Context(), trashName, httputil.CurrentUsername(c)); err != nil {
		return handleFileError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *fileHandler) deleteTrashItems(c *echo.Context) error {
	var info struct {
		Names []string `json:"names"`
	}
	if err := c.Bind(&info); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if err := h.file.DeleteTrashItems(c.Request().Context(), info.Names, httputil.CurrentUsername(c)); err != nil {
		return handleFileError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *fileHandler) listTrash(c *echo.Context) error {
	trashPath := strings.TrimPrefix(c.Param("*"), "/")
	dirInfo, err := h.file.ListTrash(c.Request().Context(), trashPath, httputil.CurrentUsername(c))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c.JSON(http.StatusNotFound, httputil.Response{Message: err.Error()})
		}
		if errors.Is(err, fs.ErrInvalid) {
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
		}
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: dirInfo})
}

func (h *fileHandler) emptyTrash(c *echo.Context) error {
	if err := h.file.EmptyTrash(c.Request().Context(), httputil.CurrentUsername(c)); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
