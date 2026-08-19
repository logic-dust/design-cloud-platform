package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/server/service/dashboard"

	"github.com/labstack/echo/v5"
)

// TestWriteDashboardFrame_CarriesVersion 验证 SSE 帧携带 version（引擎版本）：
// 前端收到帧即可对比版本，无需额外 fetch /api/version。
func TestWriteDashboardFrame_CarriesVersion(t *testing.T) {
	config.VERSION = "0.3.7-abc1234"

	var buf bytes.Buffer
	writeDashboardFrame(&buf, dashboard.Response{})

	out := buf.String()
	if !strings.HasPrefix(out, "event: dashboard\n") {
		t.Errorf("frame missing event line: %q", out)
	}
	if !strings.Contains(out, "\n\n") {
		t.Errorf("frame missing SSE terminator: %q", out)
	}

	// 提取 data 行并解析 version
	line := strings.TrimPrefix(out, "event: dashboard\ndata: ")
	line = strings.TrimSuffix(line, "\n\n")
	var frame struct {
		Data    dashboard.Response `json:"data"`
		Version string             `json:"version"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	if frame.Version != "0.3.7-abc1234" {
		t.Errorf("version = %q, want 0.3.7-abc1234", frame.Version)
	}
}

// TestGetDashboard_Disabled_ReturnsNotFound 验证当大屏被动态关闭时，GET /api/dashboard 返回 404
func TestGetDashboard_Disabled_ReturnsNotFound(t *testing.T) {
	h := newDashboardHandler(nil, func(context.Context) bool { return false })

	e := echo.New()
	req := httptest.NewRequest("GET", "/api/dashboard", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := h.getDashboard(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != 404 {
		t.Fatalf("expected status 404 when dashboard disabled, got %d", rec.Code)
	}
}
