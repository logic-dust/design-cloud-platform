package api

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"design-cloud-engine/internal/server/portal/httputil"
	appsvc "design-cloud-engine/internal/server/service/app"
)

type appHandler struct {
	app *appsvc.Service
}

func newAppHandler(app *appsvc.Service) *appHandler {
	return &appHandler{
		app: app,
	}
}

// route: /api/apps — 普通用户只能查看自己的应用
func (h *appHandler) appsAPI(g *echo.Group) {
	g.GET("", h.listApps)
	g.GET("/:id", h.getApp)
	g.POST("/:id/launch", h.launchApp)
}

// --- 获取当前用户的应用列表
func (h *appHandler) listApps(c *echo.Context) error {
	requestUser := httputil.CurrentUsername(c)
	p := httputil.ParsePage(c)
	list, total, err := h.app.ListAccessiblePage(c.Request().Context(), requestUser, p.Limit, p.Offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondPage(c, list, total)
}

// --- 根据应用 ID 获取应用详情
func (h *appHandler) getApp(c *echo.Context) error {
	appID, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}
	app, err := h.app.GetAccessibleByID(c.Request().Context(), appID, httputil.CurrentUsername(c))
	if err != nil {
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: app})
}

// --- 启动应用
func (h *appHandler) launchApp(c *echo.Context) error {
	appID, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	requestUser := httputil.CurrentUsername(c)

	result, err := h.app.Launch(c.Request().Context(), appID, requestUser)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	statusURL := schedulerJobsBasePath + result["id"]
	c.Response().Header().Set(echo.HeaderLocation, statusURL)

	return c.JSON(http.StatusAccepted, httputil.Response{Data: result})
}
