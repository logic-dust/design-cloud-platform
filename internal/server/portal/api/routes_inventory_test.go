package api

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unsafe"

	"design-cloud-engine/internal/server/service/proxy"

	"github.com/labstack/echo/v5"
)

// routeInventoryFile 是 Portal 路由清单（权威来源 = InitRouter 实际注册结果）。
// test/portal 的 .http 覆盖校验（portal_routes.py / verify_http_tests.py）读取此文件。
// 路由变更后执行：UPDATE_ROUTE_INVENTORY=1 go test ./internal/server/portal/api/ -run TestPortalRouteInventory
const routeInventoryFile = "../../../../test/portal/portal_routes.txt"

// registeredPortalRoutes 以零值子 handler 组装 Handler 并调用 InitRouter，返回 "METHOD /path" 列表。
func buildZeroHandler() *Handler {
	h := &Handler{}
	v := reflect.ValueOf(h).Elem()
	for _, f := range v.Fields() {
		f := f
		if f.Kind() == reflect.Pointer {
			reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(reflect.New(f.Type().Elem()))
		}
	}
	// WebSocket 数据面仅在 proxy 服务启用时注册
	h.connProxyHandler.proxy = &proxy.Service{}
	return h
}

func registeredPortalRoutes(t *testing.T) []string {
	t.Helper()
	h := buildZeroHandler()
	e := echo.New()
	h.InitRouter(e)
	seen := map[string]bool{}
	var out []string
	for _, r := range e.Router().Routes() {
		if !strings.HasPrefix(r.Method, "echo_") && strings.HasPrefix(r.Path, "/api/") {
			key := r.Method + " " + r.Path
			if !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestPortalRouteInventory(t *testing.T) {
	routes := registeredPortalRoutes(t)
	want := "# 自动生成，勿手改：UPDATE_ROUTE_INVENTORY=1 go test ./internal/server/portal/api/ -run TestPortalRouteInventory\n" +
		strings.Join(routes, "\n") + "\n"
	path := filepath.Clean(routeInventoryFile)
	if os.Getenv("UPDATE_ROUTE_INVENTORY") == "1" {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s is out of date (%d routes registered); regenerate with UPDATE_ROUTE_INVENTORY=1 go test ./internal/server/portal/api/ -run TestPortalRouteInventory", path, len(routes))
	}
}
