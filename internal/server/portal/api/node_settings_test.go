package api

import (
	"bytes"
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/push"
	nodesvc "design-cloud-engine/internal/server/service/node"
	"design-cloud-engine/internal/server/service/settings"

	"github.com/labstack/echo/v5"
)

func setupTestNodeSettingsAPI(t *testing.T) (*nodeHandler, *db.NodeRepo, *echo.Echo) {
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

	nodeService := nodesvc.NewService(repos.Node, slog.Default(), nodesvc.Config{
		NodeID:  "test-master",
		CertDir: filepath.Join(tempDir, "certs", "cluster"),
	}, auditSvc, push.NoopPublisher{}, nil, settingsSvc, nil, nil)

	cfg := &config.Config{}
	h := newNodeHandler(nodeService, cfg)

	e := echo.New()
	return h, repos.Node, e
}

func TestNodeSettingsAPI_Get(t *testing.T) {
	t.Parallel()

	h, nodeRepo, e := setupTestNodeSettingsAPI(t)
	ctx := context.Background()

	// 1. Non-existent node -> 404
	{
		req := httptest.NewRequest(http.MethodGet, "/api/admin/system/nodes/nonexistent/settings", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/admin/system/nodes/:id/settings")
		c.SetPathValues(echo.PathValues{{Name: "id", Value: "nonexistent"}})

		if err := h.getNodeSettings(c); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// 2. Existing node with no override -> 200, override is nil, effective has default
	{
		node := &model.Node{
			NodeID: "node-1",
			Status: model.NodeStatusOnline,
		}
		if err := nodeRepo.Create(ctx, node); err != nil {
			t.Fatalf("Create node: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/admin/system/nodes/node-1/settings", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/admin/system/nodes/:id/settings")
		c.SetPathValues(echo.PathValues{{Name: "id", Value: "node-1"}})

		if err := h.getNodeSettings(c); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp httputil.Response
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		viewBytes, _ := json.Marshal(resp.Data)
		var view schema.NodeSettingsView
		if err := json.Unmarshal(viewBytes, &view); err != nil {
			t.Fatalf("unmarshal view: %v", err)
		}
		if view.Override != nil {
			t.Errorf("expected override to be nil, got: %+v", view.Override)
		}
		if view.Effective == nil || view.Effective.Agent.VNC.BinDir == "" {
			t.Errorf("expected effective to have default VNC BinDir, got: %+v", view.Effective)
		}
	}
}

func TestNodeSettingsAPI_Put_ValidAndClear(t *testing.T) {
	t.Parallel()

	h, nodeRepo, e := setupTestNodeSettingsAPI(t)
	ctx := context.Background()

	node := &model.Node{
		NodeID: "node-2",
		Status: model.NodeStatusOnline,
	}
	if err := nodeRepo.Create(ctx, node); err != nil {
		t.Fatalf("Create node: %v", err)
	}

	// 1. PUT valid override
	payload := `{
		"agent": {
			"vnc": {
				"scanInterval": 120
			},
			"monitor": {
				"nvidiaSmiPath": "/usr/bin/nvidia-smi"
			}
		}
	}`
	{
		req := httptest.NewRequest(http.MethodPut, "/api/admin/system/nodes/node-2/settings", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/admin/system/nodes/:id/settings")
		c.SetPathValues(echo.PathValues{{Name: "id", Value: "node-2"}})

		if err := h.updateNodeSettings(c); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp httputil.Response
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		viewBytes, _ := json.Marshal(resp.Data)
		var view schema.NodeSettingsView
		_ = json.Unmarshal(viewBytes, &view)

		if view.Override == nil || view.Override.Agent == nil || view.Override.Agent.VNC == nil {
			t.Fatalf("expected override in response, got nil")
		}
		if view.Override.Agent.VNC.ScanInterval == nil || *view.Override.Agent.VNC.ScanInterval != 120 {
			t.Errorf("expected override scanInterval 120, got %v", view.Override.Agent.VNC.ScanInterval)
		}
		if view.Effective.Agent.VNC.ScanInterval != 120 {
			t.Errorf("expected effective scanInterval 120, got %d", view.Effective.Agent.VNC.ScanInterval)
		}
		if view.Effective.Agent.Monitor.NvidiaSmiPath != "/usr/bin/nvidia-smi" {
			t.Errorf("expected effective nvidiaSmiPath, got %q", view.Effective.Agent.Monitor.NvidiaSmiPath)
		}
	}

	// 2. PUT {} does not clear override
	{
		req := httptest.NewRequest(http.MethodPut, "/api/admin/system/nodes/node-2/settings", bytes.NewBufferString(`{}`))
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/admin/system/nodes/:id/settings")
		c.SetPathValues(echo.PathValues{{Name: "id", Value: "node-2"}})

		if err := h.updateNodeSettings(c); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp httputil.Response
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		viewBytes, _ := json.Marshal(resp.Data)
		var view schema.NodeSettingsView
		_ = json.Unmarshal(viewBytes, &view)

		if view.Override == nil || view.Override.Agent == nil {
			t.Fatalf("expected override to be preserved on {}, got nil")
		}
		if view.Effective.Agent.VNC.ScanInterval != 120 {
			t.Errorf("expected effective scanInterval to stay 120, got %d", view.Effective.Agent.VNC.ScanInterval)
		}
	}

	// 3. Clear override with {"agent": null}
	{
		req := httptest.NewRequest(http.MethodPut, "/api/admin/system/nodes/node-2/settings", bytes.NewBufferString(`{"agent": null}`))
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/admin/system/nodes/:id/settings")
		c.SetPathValues(echo.PathValues{{Name: "id", Value: "node-2"}})

		if err := h.updateNodeSettings(c); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp httputil.Response
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		viewBytes, _ := json.Marshal(resp.Data)
		var view schema.NodeSettingsView
		_ = json.Unmarshal(viewBytes, &view)

		if view.Override != nil {
			t.Errorf("expected override to be cleared (nil), got: %+v", view.Override)
		}
		// Effective resets to global default
		if view.Effective.Agent.VNC.ScanInterval != 60 {
			t.Errorf("expected effective scanInterval reset to 60, got %d", view.Effective.Agent.VNC.ScanInterval)
		}
	}
}

func TestNodeSettingsAPI_Put_ValidationAndUnknownFields(t *testing.T) {
	t.Parallel()

	h, nodeRepo, e := setupTestNodeSettingsAPI(t)
	ctx := context.Background()

	node := &model.Node{
		NodeID: "node-3",
		Status: model.NodeStatusOnline,
	}
	if err := nodeRepo.Create(ctx, node); err != nil {
		t.Fatalf("Create node: %v", err)
	}

	tests := []struct {
		name       string
		nodeID     string
		body       string
		wantStatus int
	}{
		{
			name:       "Node not found -> 404",
			nodeID:     "nonexistent",
			body:       `{}`,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "Malformed JSON syntax -> 400",
			nodeID:     "node-3",
			body:       `{"agent": {`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Non-object payload -> 400",
			nodeID:     "node-3",
			body:       `["array"]`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Root unknown field -> 422",
			nodeID:     "node-3",
			body:       `{"unknownKey": "val"}`,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "Deep nested unknown field (agent.vnc.unknownKey) -> 422",
			nodeID:     "node-3",
			body:       `{"agent": {"vnc": {"unknownKey": "val"}}}`,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "Invalid disk mount OS enum -> 422",
			nodeID:     "node-3",
			body:       `{"agent": {"monitor": {"diskMounts": [{"name": "m1", "path": "/mnt", "os": "invalid-os"}]}}}`,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "Out-of-range scanInterval -> 422",
			nodeID:     "node-3",
			body:       `{"agent": {"vnc": {"scanInterval": 10}}}`,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "Invalid relative path -> 422",
			nodeID:     "node-3",
			body:       `{"agent": {"execBinPath": "relative/path"}}`,
			wantStatus: http.StatusUnprocessableEntity,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/admin/system/nodes/"+tc.nodeID+"/settings", bytes.NewBufferString(tc.body))
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/admin/system/nodes/:id/settings")
			c.SetPathValues(echo.PathValues{{Name: "id", Value: tc.nodeID}})

			_ = h.updateNodeSettings(c)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestNodeSettingsAPI_Batch(t *testing.T) {
	t.Parallel()

	h, nodeRepo, e := setupTestNodeSettingsAPI(t)
	ctx := context.Background()

	// Seed test nodes
	nodes := []*model.Node{
		{
			NodeID: "batch-gpu-01",
			Status: model.NodeStatusOnline,
			OS:     "linux",
			Groups: []string{"gpu", "r1"},
		},
		{
			NodeID: "batch-gpu-02",
			Status: model.NodeStatusOffline,
			OS:     "linux",
			Groups: []string{"gpu", "r2"},
		},
		{
			NodeID: "batch-cpu-01",
			Status: model.NodeStatusOnline,
			OS:     "windows",
			Groups: []string{"cpu"},
		},
	}
	for _, n := range nodes {
		if err := nodeRepo.Create(ctx, n); err != nil {
			t.Fatalf("Create node %s: %v", n.NodeID, err)
		}
	}

	// 1. Empty selector -> 422
	{
		reqBody := `{"selector": {}, "patch": {"agent": {"vnc": {"scanInterval": 120}}}}`
		req := httptest.NewRequest(http.MethodPost, "/api/admin/system/nodes/settings/batch-update", bytes.NewBufferString(reqBody))
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/admin/system/nodes/settings/batch-update")

		_ = h.batchUpdateNodeSettings(c)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for empty selector, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// 2. Nil selector -> 422
	{
		reqBody := `{"patch": {"agent": {"vnc": {"scanInterval": 120}}}}`
		req := httptest.NewRequest(http.MethodPost, "/api/admin/system/nodes/settings/batch-update", bytes.NewBufferString(reqBody))
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/admin/system/nodes/settings/batch-update")

		_ = h.batchUpdateNodeSettings(c)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for nil selector, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// 3. Unknown field or domain (storage) -> 422
	{
		reqBody := `{"selector": {"nodeIds": ["batch-gpu-01"]}, "patch": {"storage": {"foo": "bar"}}}`
		req := httptest.NewRequest(http.MethodPost, "/api/admin/system/nodes/settings/batch-update", bytes.NewBufferString(reqBody))
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/admin/system/nodes/settings/batch-update")

		_ = h.batchUpdateNodeSettings(c)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for storage domain in patch, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// 4. Malformed JSON -> 400
	{
		reqBody := `{bad json`
		req := httptest.NewRequest(http.MethodPost, "/api/admin/system/nodes/settings/batch-update", bytes.NewBufferString(reqBody))
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/admin/system/nodes/settings/batch-update")

		_ = h.batchUpdateNodeSettings(c)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for malformed json, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// 5. Invalid status string -> 422
	{
		reqBody := `{"selector": {"status": ["invalid_status"]}, "patch": {"agent": {"vnc": {"scanInterval": 120}}}}`
		req := httptest.NewRequest(http.MethodPost, "/api/admin/system/nodes/settings/batch-update", bytes.NewBufferString(reqBody))
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/admin/system/nodes/settings/batch-update")

		_ = h.batchUpdateNodeSettings(c)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for invalid status, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// 6. Dry run with groups selector -> matched=2, updated=2, unchanged=0, but DB NOT changed
	{
		reqBody := `{
			"selector": {"groups": ["gpu"]},
			"patch": {
				"agent": {
					"monitor": {
						"diskMounts": [
							{"name": "data", "path": "/data", "enabled": true, "os": "linux"}
						]
					}
				}
			},
			"dryRun": true
		}`
		req := httptest.NewRequest(http.MethodPost, "/api/admin/system/nodes/settings/batch-update", bytes.NewBufferString(reqBody))
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/admin/system/nodes/settings/batch-update")

		_ = h.batchUpdateNodeSettings(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for dryRun, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp httputil.Response
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal resp: %v", err)
		}
		dataMap := resp.Data.(map[string]any)
		if int(dataMap["matched"].(float64)) != 2 || int(dataMap["updated"].(float64)) != 2 || dataMap["dryRun"].(bool) != true {
			t.Fatalf("unexpected dryRun result: %+v", dataMap)
		}

		// Verify DB did NOT change
		node1, err := nodeRepo.GetByNodeID(ctx, "batch-gpu-01")
		if err != nil || len(node1.Settings) != 0 {
			t.Fatalf("expected batch-gpu-01 settings untouched after dryRun, got %v", node1.Settings)
		}
	}

	// 7. Execute batch update (dryRun=false)
	{
		reqBody := `{
			"selector": {"groups": ["gpu"]},
			"patch": {
				"agent": {
					"monitor": {
						"diskMounts": [
							{"name": "data", "path": "/data", "enabled": true, "os": "linux"}
						]
					}
				}
			},
			"dryRun": false
		}`
		req := httptest.NewRequest(http.MethodPost, "/api/admin/system/nodes/settings/batch-update", bytes.NewBufferString(reqBody))
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/admin/system/nodes/settings/batch-update")

		_ = h.batchUpdateNodeSettings(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for batch update, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp httputil.Response
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]any)
		if int(dataMap["matched"].(float64)) != 2 || int(dataMap["updated"].(float64)) != 2 {
			t.Fatalf("unexpected batch update result: %+v", dataMap)
		}

		// Verify DB updated for batch-gpu-01 and batch-gpu-02
		node1, err := nodeRepo.GetByNodeID(ctx, "batch-gpu-01")
		if err != nil || len(node1.Settings) == 0 {
			t.Fatalf("expected batch-gpu-01 settings updated, got %v", node1.Settings)
		}
		node3, err := nodeRepo.GetByNodeID(ctx, "batch-cpu-01")
		if err != nil || len(node3.Settings) != 0 {
			t.Fatalf("batch-cpu-01 should not have been updated, got %v", node3.Settings)
		}

		// Check effective settings on node1
		view, err := h.node.GetNodeSettings(ctx, "batch-gpu-01")
		if err != nil || view.Override == nil || view.Override.Agent.Monitor == nil {
			t.Fatalf("failed to get node1 settings: %v", err)
		}
		if len(view.Override.Agent.Monitor.DiskMounts) != 1 || view.Override.Agent.Monitor.DiskMounts[0].Name != "data" {
			t.Errorf("expected disk mount data, got %+v", view.Override.Agent.Monitor.DiskMounts)
		}
	}

	// 8. Re-running same batch update reports updated=0, unchanged=2 (idempotency)
	{
		reqBody := `{
			"selector": {"groups": ["gpu"]},
			"patch": {
				"agent": {
					"monitor": {
						"diskMounts": [
							{"name": "data", "path": "/data", "enabled": true, "os": "linux"}
						]
					}
				}
			},
			"dryRun": false
		}`
		req := httptest.NewRequest(http.MethodPost, "/api/admin/system/nodes/settings/batch-update", bytes.NewBufferString(reqBody))
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/admin/system/nodes/settings/batch-update")

		_ = h.batchUpdateNodeSettings(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 on retry, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp httputil.Response
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]any)
		if int(dataMap["matched"].(float64)) != 2 || int(dataMap["updated"].(float64)) != 0 || int(dataMap["unchanged"].(float64)) != 2 {
			t.Fatalf("expected 0 updated and 2 unchanged on idempotent retry, got: %+v", dataMap)
		}
	}
}
