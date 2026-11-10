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
	IsOverviewEnabled(ctx context.Context) bool
}

// RegisterWebRoutes serves the embedded static web pages (homepage + overview).
// Dynamic visibility is governed by webSettings (PlatformSettings.Pages.Index / Pages.Overview).
func RegisterWebRoutes(e *echo.Echo, webSettings WebSettingsChecker) {
	subFS := echo.MustSubFS(webFS, "web")

	indexHTML, indexErr := loadVersionedHTML(subFS, "index.html")
	// 首页 URL：/ → index.html
	e.GET("/", func(c *echo.Context) error {
		if indexErr != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "index page unavailable")
		}
		if webSettings != nil && !webSettings.IsIndexEnabled(c.Request().Context()) {
			return echo.NewHTTPError(http.StatusNotFound, "not found")
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-cache")
		return c.HTML(http.StatusOK, indexHTML)
	})

	// Overview URL: /overview → overview.html
	overviewHTML, err := loadVersionedHTML(subFS, "overview.html")
	e.GET("/overview", func(c *echo.Context) error {
		if err != nil || (webSettings != nil && !webSettings.IsOverviewEnabled(c.Request().Context())) {
			return echo.NewHTTPError(http.StatusNotFound, "not found")
		}
		// no-cache：大屏长期驻留，若 HTML 被浏览器缓存则永远拿不到新版
		// （内嵌资源的版本化由 __WEB_VERSION__ 占位符替换实现）。
		c.Response().Header().Set(echo.HeaderCacheControl, "no-cache")
		return c.HTML(http.StatusOK, overviewHTML)
	})

	// 统一挂载静态资源包目录路径 /static
	e.StaticFS("/static", echo.MustSubFS(webFS, "web/static"))
}

// loadVersionedHTML reads an HTML file from subFS and replaces build placeholders.
// __WEB_VERSION__：静态资源 ?v= cache bust（与 config.VERSION 一致，不再拼接 COMMIT）。
// __ENGINE_VERSION__ / __ENGINE_COMMIT__ / __ENGINE_DATE__：首页展示用构建身份（与 /api/version 同源）。
func loadVersionedHTML(subFS fs.FS, name string) (string, error) {
	content, err := fs.ReadFile(subFS, name)
	if err != nil {
		return "", err
	}
	body := string(content)
	body = strings.ReplaceAll(body, "__WEB_VERSION__", config.VERSION)
	body = strings.ReplaceAll(body, "__ENGINE_VERSION__", config.VERSION)
	body = strings.ReplaceAll(body, "__ENGINE_COMMIT__", config.COMMIT)
	body = strings.ReplaceAll(body, "__ENGINE_DATE__", config.DATE)
	return body, nil
}
