package portal

import (
	"context"
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"design-cloud-engine/internal/config"

	"github.com/labstack/echo/v5"
)

//go:embed web
var webFS embed.FS

// WebSettingsChecker provides dynamic check methods for embedded web pages.
type WebSettingsChecker interface {
	IsIndexEnabled(ctx context.Context) bool
	IsDashboardEnabled(ctx context.Context) bool
}

// RegisterWebRoutes serves the embedded static web pages (homepage + dashboard).
// Dynamic visibility is governed by webSettings (PlatformSettings.IndexEnabled and DashboardSettings.Enabled).
func RegisterWebRoutes(e *echo.Echo, webSettings WebSettingsChecker) {
	subFS := echo.MustSubFS(webFS, "web")

	// 首页 URL：/ → index.html
	e.GET("/", func(c *echo.Context) error {
		if webSettings != nil && !webSettings.IsIndexEnabled(c.Request().Context()) {
			return echo.NewHTTPError(http.StatusNotFound, "not found")
		}
		return c.FileFS("index.html", subFS)
	})

	// Dashboard URL: /dashboard → dashboard.html
	dashboardHTML, err := loadVersionedHTML(subFS, "dashboard.html")
	e.GET("/dashboard", func(c *echo.Context) error {
		if err != nil || (webSettings != nil && !webSettings.IsDashboardEnabled(c.Request().Context())) {
			return echo.NewHTTPError(http.StatusNotFound, "not found")
		}
		// no-cache：大屏长期驻留，若 HTML 被浏览器缓存则永远拿不到新版
		// （内嵌资源的版本化由 __WEB_VERSION__ 占位符替换实现）。
		c.Response().Header().Set(echo.HeaderCacheControl, "no-cache")
		return c.HTML(http.StatusOK, dashboardHTML)
	})

	// 统一挂载静态资源包目录路径 /static
	e.StaticFS("/static", echo.MustSubFS(webFS, "web/static"))
}

// loadVersionedHTML reads an HTML file from subFS and replaces the __WEB_VERSION__ placeholder
// with the current build version. 用途：内嵌资源的版本化缓存控制——
// HTML 引用带 ?v=<version> 的资源 URL，版本变 → URL 变 → 浏览器重新下载
func loadVersionedHTML(subFS fs.FS, name string) (string, error) {
	content, err := fs.ReadFile(subFS, name)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(content), "__WEB_VERSION__", config.VERSION), nil
}
