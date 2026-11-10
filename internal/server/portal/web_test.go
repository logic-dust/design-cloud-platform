package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"design-cloud-engine/internal/platform/config"

	"github.com/labstack/echo/v5"
)

// TestLoadVersionedHTML_PlaceholderReplaced 验证 __WEB_VERSION__ 占位符
// 被替换为 config.VERSION(+COMMIT)。
// 注意：修改 config 全局变量，不能与其它测试并行。
func TestLoadVersionedHTML_PlaceholderReplaced(t *testing.T) {
	// 用真实 embed 的 overview.html：引用占位符资源 URL，且含内嵌版本脚本
	subFS := echo.MustSubFS(webFS, "web")

	config.VERSION = "0.3.7"
	config.COMMIT = "abc1234"

	body, err := loadVersionedHTML(subFS, "overview.html")
	if err != nil {
		t.Fatalf("loadVersionedHTML: %v", err)
	}

	if strings.Contains(body, "__WEB_VERSION__") {
		t.Error("placeholder __WEB_VERSION__ not replaced in loaded HTML")
	}
	// config.VERSION 已含 commit 后缀，loadVersionedHTML 不得再追加 COMMIT
	if !strings.Contains(body, "js/theme.js?v=0.3.7") {
		t.Error("theme.js reference not versioned with config.VERSION")
	}
	if strings.Contains(body, "js/theme.js?v=0.3.7-abc1234") {
		t.Error("loadVersionedHTML must not append COMMIT to VERSION (VERSION already includes it)")
	}
	if !strings.Contains(body, "css/theme.css?v=0.3.7") {
		t.Error("theme.css reference not versioned with config.VERSION")
	}
	if !strings.Contains(body, "js/overview.js?v=0.3.7") {
		t.Error("overview.js reference not versioned with config.VERSION")
	}

	indexBody, err := loadVersionedHTML(subFS, "index.html")
	if err != nil {
		t.Fatalf("loadVersionedHTML index: %v", err)
	}
	if strings.Contains(indexBody, "__WEB_VERSION__") {
		t.Error("index.html placeholder __WEB_VERSION__ not replaced")
	}
	if !strings.Contains(indexBody, "js/theme.js?v=0.3.7") {
		t.Error("index theme.js reference not versioned")
	}
	if !strings.Contains(indexBody, "css/index.css?v=0.3.7") {
		t.Error("index.css reference not versioned with config.VERSION")
	}
	if !strings.Contains(indexBody, "js/index.js?v=0.3.7") {
		t.Error("index.js reference not versioned with config.VERSION")
	}
}

type mockWebSettings struct {
	indexEnabled    bool
	overviewEnabled bool
}

func (m *mockWebSettings) IsIndexEnabled(ctx context.Context) bool {
	return m.indexEnabled
}

func (m *mockWebSettings) IsOverviewEnabled(ctx context.Context) bool {
	return m.overviewEnabled
}

// TestRegisterWebRoutes_DynamicPageSwitches 验证 Web 页面开关动态生效
func TestRegisterWebRoutes_DynamicPageSwitches(t *testing.T) {
	e := echo.New()
	settings := &mockWebSettings{indexEnabled: true, overviewEnabled: true}
	RegisterWebRoutes(e, settings)

	// 1. 均开启时，GET / 和 GET /overview 均返回 200
	reqIndex := httptest.NewRequest(http.MethodGet, "/", nil)
	recIndex := httptest.NewRecorder()
	e.ServeHTTP(recIndex, reqIndex)
	if recIndex.Code != http.StatusOK {
		t.Fatalf("GET / when enabled = %d, want 200", recIndex.Code)
	}

	reqDash := httptest.NewRequest(http.MethodGet, "/overview", nil)
	recDash := httptest.NewRecorder()
	e.ServeHTTP(recDash, reqDash)
	if recDash.Code != http.StatusOK {
		t.Fatalf("GET /overview when enabled = %d, want 200", recDash.Code)
	}
	if got := recDash.Header().Get(echo.HeaderCacheControl); got != "no-cache" {
		t.Errorf("overview Cache-Control = %q, want no-cache", got)
	}
	if got := recIndex.Header().Get(echo.HeaderCacheControl); got != "no-cache" {
		t.Errorf("index Cache-Control = %q, want no-cache", got)
	}

	// 2. 动态关闭首页后，GET / 返回 404
	settings.indexEnabled = false
	recIndexDisabled := httptest.NewRecorder()
	e.ServeHTTP(recIndexDisabled, reqIndex)
	if recIndexDisabled.Code != http.StatusNotFound {
		t.Fatalf("GET / when disabled = %d, want 404", recIndexDisabled.Code)
	}

	// 3. 动态关闭大屏后，GET /overview 返回 404
	settings.overviewEnabled = false
	recDashDisabled := httptest.NewRecorder()
	e.ServeHTTP(recDashDisabled, reqDash)
	if recDashDisabled.Code != http.StatusNotFound {
		t.Fatalf("GET /overview when disabled = %d, want 404", recDashDisabled.Code)
	}
}

// TestLoadVersionedHTML_CommitNotAppended 验证 COMMIT 不参与版本拼接
// （config.VERSION 已含 commit 后缀）。
// 注意：修改 config 全局变量，不能与其它测试并行。
func TestLoadVersionedHTML_CommitNotAppended(t *testing.T) {
	subFS := echo.MustSubFS(webFS, "web")

	config.VERSION = "0.3.7"
	config.COMMIT = "abc1234"

	body, err := loadVersionedHTML(subFS, "overview.html")
	if err != nil {
		t.Fatalf("loadVersionedHTML: %v", err)
	}

	if strings.Contains(body, "js/theme.js?v=0.3.7-abc1234") {
		t.Error("theme.js should not append COMMIT (VERSION already carries it)")
	}
}
