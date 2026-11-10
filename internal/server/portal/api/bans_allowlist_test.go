package api

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/service/settings"

	"github.com/labstack/echo/v5"
)

// newTestSettingsServiceForAPI 构造基于临时 sqlite 的真实 settings 服务（含默认域种子）。
func newTestSettingsServiceForAPI(t *testing.T) (*db.Store, *settings.Service) {
	t.Helper()

	tempDir := t.TempDir()
	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(tempDir, "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := settings.EnsureDefaults(store); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}

	repos := db.NewRepos(store)
	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  filepath.Join(tempDir, "audit.log"),
		SecurityLogPath: filepath.Join(tempDir, "security.log"),
		NodeID:          "test-master",
		Version:         config.VERSION,
	})
	if err != nil {
		t.Fatalf("audit.NewService: %v", err)
	}
	t.Cleanup(func() { _ = auditSvc.Close() })

	settingsSvc, err := settings.NewService(repos.Setting, slog.Default(), auditSvc, nil, repos.CacheVersion, repos.Node)
	if err != nil {
		t.Fatalf("settings.NewService: %v", err)
	}
	return store, settingsSvc
}

// decodeAPIResponse 解开标准 {"data":...} 响应信封。
func decodeAPIResponse[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()

	var envelope struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return envelope.Data
}

// setupTestBanAllowlistAPI 构造带真实 settings 服务的 banHandler（security 依赖对放行名单路由不可达，置 nil）。
func setupTestBanAllowlistAPI(t *testing.T) (*banHandler, *echo.Echo) {
	t.Helper()

	_, settingsSvc := newTestSettingsServiceForAPI(t)
	return newBanHandler(nil, settingsSvc), echo.New()
}

func doBanAllowlistRequest(t *testing.T, h *banHandler, e *echo.Echo, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	var err error
	switch method {
	case http.MethodGet:
		err = h.adminGetBanAllowlist(c)
	case http.MethodPost:
		err = h.adminAddBanAllowlist(c)
	case http.MethodDelete:
		err = h.adminRemoveBanAllowlist(c)
	default:
		t.Fatalf("unsupported method %s", method)
	}
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	return rec
}

func allowlistEntries(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()

	resp := decodeAPIResponse[struct {
		Entries []string `json:"entries"`
	}](t, rec)
	return resp.Entries
}

// TestBanAllowlistAPI 覆盖放行名单三路由：查/增（幂等）/删（幂等）与参数校验。
func TestBanAllowlistAPI(t *testing.T) {
	t.Parallel()

	h, e := setupTestBanAllowlistAPI(t)

	// 1. 默认空列表（[] 而非 null）
	rec := doBanAllowlistRequest(t, h, e, http.MethodGet, "/api/admin/security/bans/allowlist", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	entries := allowlistEntries(t, rec)
	if entries == nil || len(entries) != 0 {
		t.Fatalf("expected empty non-nil entries, got %#v", entries)
	}

	// 2. 追加两个条目
	rec = doBanAllowlistRequest(t, h, e, http.MethodPost, "/api/admin/security/bans/allowlist",
		`{"entries":["192.0.2.10","10.0.0.0/8"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body = %s", rec.Code, rec.Body.String())
	}
	entries = allowlistEntries(t, rec)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %v", entries)
	}

	// 3. 重复追加幂等
	rec = doBanAllowlistRequest(t, h, e, http.MethodPost, "/api/admin/security/bans/allowlist",
		`{"entries":["192.0.2.10"]}`)
	entries = allowlistEntries(t, rec)
	if len(entries) != 2 {
		t.Errorf("expected duplicate add to be a no-op, got %v", entries)
	}

	// 4. 非法条目 → 422
	rec = doBanAllowlistRequest(t, h, e, http.MethodPost, "/api/admin/security/bans/allowlist",
		`{"entries":["not-an-ip"]}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid entry status = %d, want 422", rec.Code)
	}

	// 5. 空 entries → 400
	rec = doBanAllowlistRequest(t, h, e, http.MethodPost, "/api/admin/security/bans/allowlist", `{"entries":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty entries status = %d, want 400", rec.Code)
	}

	// 6. 移除存在条目（CIDR 须查询串编码）
	rec = doBanAllowlistRequest(t, h, e, http.MethodDelete, "/api/admin/security/bans/allowlist?entry=10.0.0.0%2F8", "")
	entries = allowlistEntries(t, rec)
	if len(entries) != 1 || entries[0] != "192.0.2.10" {
		t.Errorf("expected [192.0.2.10], got %v", entries)
	}

	// 7. 移除不存在条目：幂等 200
	rec = doBanAllowlistRequest(t, h, e, http.MethodDelete, "/api/admin/security/bans/allowlist?entry=203.0.113.9", "")
	if rec.Code != http.StatusOK {
		t.Errorf("idempotent remove status = %d, want 200", rec.Code)
	}
	entries = allowlistEntries(t, rec)
	if len(entries) != 1 {
		t.Errorf("expected no-op remove to keep 1 entry, got %v", entries)
	}

	// 8. 缺少 entry 参数 → 400
	rec = doBanAllowlistRequest(t, h, e, http.MethodDelete, "/api/admin/security/bans/allowlist", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing entry param status = %d, want 400", rec.Code)
	}
}
