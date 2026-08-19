package license

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/settings/schema"
)

type mockExecutor struct {
	executeFunc func(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error)
	callCount   atomic.Int64
}

func (m *mockExecutor) Execute(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	m.callCount.Add(1)
	if m.executeFunc != nil {
		return m.executeFunc(ctx, req)
	}
	return &execution.ExecuteResponse{Success: true, Stdout: ""}, nil
}

type mockSettingsService struct {
	mu       sync.RWMutex
	settings schema.LicenseSettings
	err      error
}

func (m *mockSettingsService) GetLicenseSettings(ctx context.Context) (schema.LicenseSettings, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.err != nil {
		return schema.LicenseSettings{}, m.err
	}
	return m.settings, nil
}

func (m *mockSettingsService) setSettings(settings schema.LicenseSettings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = settings
}

func newLicenseServiceForTest(t *testing.T) (*Service, *db.LicenseRepo, *mockExecutor, *mockSettingsService, *db.Repos, *db.Store) {
	t.Helper()

	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repos := db.NewRepos(store)
	mockExec := &mockExecutor{}
	mockSettings := &mockSettingsService{
		settings: schema.LicenseSettings{
			StatusCacheTTL:   schema.LicenseStatusCacheTTLDefault,
			FeaturesCacheTTL: schema.LicenseFeaturesCacheTTLDefault,
		},
	}
	svc := NewService(mockExec, repos.License, mockSettings, slog.Default(), WithNodeRepo(repos.Node))
	return svc, repos.License, mockExec, mockSettings, repos, store
}

func TestServiceCRUD(t *testing.T) {
	svc, _, _, _, _, _ := newLicenseServiceForTest(t)
	ctx := context.Background()

	// 1. Create with default enabled (form.Enabled = true)
	form := model.LicenseServiceBase{
		Enabled: true,
		Name:    "Flex1",
		Address: "27000@server1",
		Type:    model.LicenseTypeFlexlm,
	}
	lic, err := svc.CreateFromInput(ctx, form)
	if err != nil {
		t.Fatalf("failed to create license service: %v", err)
	}
	if lic.ID == 0 || lic.Name != "Flex1" || !lic.Enabled {
		t.Errorf("unexpected created license: %+v", lic)
	}
	if lic.Status != model.LicenseServiceStatusUnknown {
		t.Errorf("expected initial status unknown, got %s", lic.Status)
	}

	// 2. Get
	fetched, err := svc.Get(ctx, lic.ID)
	if err != nil {
		t.Fatalf("failed to get license: %v", err)
	}
	if fetched.Name != "Flex1" {
		t.Errorf("expected Flex1, got %s", fetched.Name)
	}

	// 3. UpdateFromInput
	form.Name = "Flex1-Updated"
	updated, err := svc.UpdateFromInput(ctx, lic.ID, form)
	if err != nil {
		t.Fatalf("failed to update license: %v", err)
	}
	if updated.Name != "Flex1-Updated" {
		t.Errorf("expected Flex1-Updated, got %s", updated.Name)
	}

	// 4. List (should include enabled)
	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("failed to list licenses: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Flex1-Updated" {
		t.Errorf("unexpected list: %v", list)
	}

	// 5. Create disabled license
	formDisabled := model.LicenseServiceBase{
		Enabled: false,
		Name:    "Flex-Disabled",
		Address: "27000@server2",
		Type:    model.LicenseTypeFlexlm,
	}
	licDisabled, err := svc.CreateFromInput(ctx, formDisabled)
	if err != nil {
		t.Fatalf("failed to create disabled license: %v", err)
	}

	// User List should filter out disabled
	userList, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("user list: %v", err)
	}
	if len(userList) != 1 {
		t.Errorf("expected user list to contain 1 item, got %d", len(userList))
	}

	// User Get on disabled should return 404 (ErrNotFound)
	_, err = svc.Get(ctx, licDisabled.ID)
	if !errors.Is(err, common.ErrNotFound) {
		t.Errorf("expected ErrNotFound for disabled license, got %v", err)
	}

	// AdminList sees both
	adminList, err := svc.AdminList(ctx)
	if err != nil {
		t.Fatalf("admin list: %v", err)
	}
	if len(adminList) != 2 {
		t.Errorf("expected admin list to contain 2 items, got %d", len(adminList))
	}

	// AdminGet sees disabled
	adminFetched, err := svc.AdminGet(ctx, licDisabled.ID)
	if err != nil {
		t.Fatalf("admin get: %v", err)
	}
	if adminFetched.Name != "Flex-Disabled" {
		t.Errorf("expected Flex-Disabled, got %s", adminFetched.Name)
	}

	// 6. Delete
	err = svc.Delete(ctx, lic.ID)
	if err != nil {
		t.Fatalf("failed to delete license: %v", err)
	}

	// Get after delete
	_, err = svc.Get(ctx, lic.ID)
	if err == nil {
		t.Error("expected error getting deleted license")
	}
}

func TestValidation(t *testing.T) {
	svc, _, _, _, repos, _ := newLicenseServiceForTest(t)
	ctx := context.Background()

	// Add a valid node to repos.Node
	_ = repos.Node.Create(ctx, &model.Node{
		NodeID: "valid-node",
	})

	// 1. Empty name
	_, err := svc.CreateFromInput(ctx, model.LicenseServiceBase{
		Name:    "",
		Address: "27000@server",
		Type:    model.LicenseTypeFlexlm,
	})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("expected fs.ErrInvalid for empty name, got %v", err)
	}

	// 2. Name too long (>64)
	longName := string(make([]byte, 65))
	_, err = svc.CreateFromInput(ctx, model.LicenseServiceBase{
		Name:    longName,
		Address: "27000@server",
		Type:    model.LicenseTypeFlexlm,
	})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("expected fs.ErrInvalid for long name, got %v", err)
	}

	// 3. Invalid type
	_, err = svc.CreateFromInput(ctx, model.LicenseServiceBase{
		Name:    "Flex",
		Address: "27000@server",
		Type:    "unsupported",
	})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("expected fs.ErrInvalid for invalid type, got %v", err)
	}

	// 4. Empty address
	_, err = svc.CreateFromInput(ctx, model.LicenseServiceBase{
		Name:    "Flex",
		Address: "",
		Type:    model.LicenseTypeFlexlm,
	})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("expected fs.ErrInvalid for empty address, got %v", err)
	}

	// 5. Server node not found
	_, err = svc.CreateFromInput(ctx, model.LicenseServiceBase{
		Name:    "Flex",
		Address: "27000@server",
		Server:  "non-existent-node",
		Type:    model.LicenseTypeFlexlm,
	})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("expected fs.ErrInvalid for non-existent server node, got %v", err)
	}

	// 6. Valid with node
	created, err := svc.CreateFromInput(ctx, model.LicenseServiceBase{
		Name:    "Flex",
		Address: "27000@server",
		Server:  "valid-node",
		Type:    model.LicenseTypeFlexlm,
	})
	if err != nil {
		t.Fatalf("expected valid create to succeed, got %v", err)
	}
	if created.Server != "valid-node" {
		t.Errorf("expected server valid-node, got %s", created.Server)
	}
}

func TestGetStatus_CacheAndErrors(t *testing.T) {
	svc, licStore, mockExec, _, _, _ := newLicenseServiceForTest(t)
	ctx := context.Background()

	// 1. Non-existent ID
	_, err := svc.GetStatus(ctx, 999)
	if err == nil {
		t.Error("expected error for non-existent license ID")
	}

	// Create an enabled license
	lic := model.LicenseService{
		LicenseServiceBase: model.LicenseServiceBase{
			Enabled: true,
			Name:    "Flex",
			Address: "27000@server",
			Type:    model.LicenseTypeFlexlm,
		},
	}
	if err := licStore.Save(ctx, &lic); err != nil {
		t.Fatalf("failed to save license: %v", err)
	}

	// 2. First call: Cache miss, action succeeds
	mockExec.executeFunc = func(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
		output := `
mgt01: license server UP (v11.16.2)
Vendor daemon status (on mgt01):
  snpslmd: UP (v11.16.2)
`
		return &execution.ExecuteResponse{Success: true, Stdout: output}, nil
	}

	status, err := svc.GetStatus(ctx, lic.ID)
	if err != nil {
		t.Fatalf("expected status check to succeed, got %v", err)
	}
	if status != string(model.LicenseServiceStatusUp) {
		t.Errorf("expected UP, got %s", status)
	}
	if mockExec.callCount.Load() != 1 {
		t.Errorf("expected 1 executor call, got %d", mockExec.callCount.Load())
	}

	// Verify that DB was NOT updated with status
	persisted, _ := licStore.Get(ctx, lic.ID)
	if persisted.Status != "" {
		t.Errorf("expected DB status to be empty (transient field), got %s", persisted.Status)
	}

	// 3. Second call: Cache hit (executor should NOT be called again)
	status, err = svc.GetStatus(ctx, lic.ID)
	if err != nil {
		t.Fatalf("expected cache hit to succeed, got %v", err)
	}
	if status != string(model.LicenseServiceStatusUp) {
		t.Errorf("expected cached UP, got %s", status)
	}
	if mockExec.callCount.Load() != 1 {
		t.Errorf("expected still 1 executor call on cache hit, got %d", mockExec.callCount.Load())
	}

	// 4. UpdateFromInput invalidates cache
	_, err = svc.UpdateFromInput(ctx, lic.ID, lic.LicenseServiceBase)
	if err != nil {
		t.Fatalf("update license: %v", err)
	}

	// Next GetStatus should call executor again
	_, err = svc.GetStatus(ctx, lic.ID)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if mockExec.callCount.Load() != 2 {
		t.Errorf("expected 2 executor calls after cache invalidation, got %d", mockExec.callCount.Load())
	}

	// 5. Stale-on-error fallback:
	// Set mock executor to fail. Since there's a cached value, GetWithTTL returns stale if expired,
	// but here it's fresh so it returns fresh cached.
	// Let's test disabled license:
	lic.Enabled = false
	_ = licStore.Save(ctx, &lic)
	_, err = svc.GetStatus(ctx, lic.ID)
	if !errors.Is(err, common.ErrNotFound) {
		t.Errorf("expected ErrNotFound for disabled license on GetStatus, got %v", err)
	}
}

func TestGetStatus_SingleFlight(t *testing.T) {
	svc, licStore, mockExec, _, _, _ := newLicenseServiceForTest(t)
	ctx := context.Background()

	lic := model.LicenseService{
		LicenseServiceBase: model.LicenseServiceBase{
			Enabled: true,
			Name:    "Flex-SF",
			Address: "27000@server",
			Type:    model.LicenseTypeFlexlm,
		},
	}
	_ = licStore.Save(ctx, &lic)

	// Slow executor simulating concurrency
	blockCh := make(chan struct{})
	mockExec.executeFunc = func(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
		<-blockCh
		output := `
mgt01: license server UP (v11.16.2)
Vendor daemon status (on mgt01):
  snpslmd: UP (v11.16.2)
`
		return &execution.ExecuteResponse{Success: true, Stdout: output}, nil
	}

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make([]error, n)
	statuses := make([]string, n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			statuses[idx], errs[idx] = svc.GetStatus(ctx, lic.ID)
		}(i)
	}

	// Let the requests pile up on singleflight
	time.Sleep(50 * time.Millisecond)
	close(blockCh)
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Errorf("request %d failed: %v", i, errs[i])
		}
		if statuses[i] != string(model.LicenseServiceStatusUp) {
			t.Errorf("request %d status = %s, want up", i, statuses[i])
		}
	}

	// Singleflight guarantees exactly 1 execution
	if mockExec.callCount.Load() != 1 {
		t.Errorf("expected singleflight to coalesce to 1 executor call, got %d", mockExec.callCount.Load())
	}
}

func TestListFeatures_CacheAndDelimiterError(t *testing.T) {
	svc, licStore, mockExec, _, _, _ := newLicenseServiceForTest(t)
	ctx := context.Background()

	lic := model.LicenseService{
		LicenseServiceBase: model.LicenseServiceBase{
			Enabled: true,
			Name:    "Flex-Features",
			Address: "27000@server",
			Type:    model.LicenseTypeFlexlm,
		},
	}
	_ = licStore.Save(ctx, &lic)

	// 1. Success case
	mockExec.executeFunc = func(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
		output := `lmstat -a output...
Users of SSS:  (Total of 1 license issued;  Total of 0 licenses in use)

NOTE: lmstat -i
 Feature   Version   #licences  Vendor   Expires
_______   _________ _________   ______   ________
 SSS        1.0        1       snpslmd  18-Feb-2024
`
		return &execution.ExecuteResponse{Success: true, Stdout: output}, nil
	}

	features, err := svc.ListFeatures(ctx, lic.ID)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(features) != 1 || features[0].Name != "SSS" {
		t.Errorf("unexpected features: %v", features)
	}
	if mockExec.callCount.Load() != 1 {
		t.Errorf("expected 1 call, got %d", mockExec.callCount.Load())
	}

	// 2. Cache hit: second call does not invoke executor
	featuresCached, err := svc.ListFeatures(ctx, lic.ID)
	if err != nil {
		t.Fatalf("expected cache hit success, got %v", err)
	}
	if len(featuresCached) != 1 {
		t.Errorf("unexpected cached features: %v", featuresCached)
	}
	if mockExec.callCount.Load() != 1 {
		t.Errorf("expected still 1 call on cache hit, got %d", mockExec.callCount.Load())
	}

	// 3. Delimiter missing on new license without cache
	lic2 := model.LicenseService{
		LicenseServiceBase: model.LicenseServiceBase{
			Enabled: true,
			Name:    "Flex-NoDelim",
			Address: "27000@server2",
			Type:    model.LicenseTypeFlexlm,
		},
	}
	_ = licStore.Save(ctx, &lic2)

	mockExec.executeFunc = func(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
		return &execution.ExecuteResponse{Success: true, Stdout: "corrupted output without split token"}, nil
	}

	_, err = svc.ListFeatures(ctx, lic2.ID)
	if err == nil {
		t.Error("expected error when lmstat output lacks delimiter token, got nil")
	}
}

func TestStatus_FlexLMBranching(t *testing.T) {
	svc, _, mockExec, _, _, _ := newLicenseServiceForTest(t)
	ctx := context.Background()

	lic := &model.LicenseService{
		LicenseServiceBase: model.LicenseServiceBase{
			Type: model.LicenseTypeFlexlm,
		},
	}

	// 1. Both UP -> Up
	mockExec.executeFunc = func(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
		output := `
mgt01: license server UP (v11.16.2)
Vendor daemon status (on mgt01):
  snpslmd: UP (v11.16.2)
`
		return &execution.ExecuteResponse{Success: true, Stdout: output}, nil
	}
	status, err := svc.Status(ctx, lic)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != model.LicenseServiceStatusUp {
		t.Errorf("expected Up, got %s", status)
	}

	// 2. Server UP, Vendor DOWN -> Down
	mockExec.executeFunc = func(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
		output := `
mgt01: license server UP (v11.16.2)
Vendor daemon status (on mgt01):
  snpslmd: DOWN
`
		return &execution.ExecuteResponse{Success: true, Stdout: output}, nil
	}
	status, err = svc.Status(ctx, lic)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != model.LicenseServiceStatusDown {
		t.Errorf("expected Down, got %s", status)
	}

	// 3. Unsupported type
	unsupportedLic := &model.LicenseService{
		LicenseServiceBase: model.LicenseServiceBase{
			Type: "unsupported",
		},
	}
	_, err = svc.Status(ctx, unsupportedLic)
	if err == nil {
		t.Error("expected error for unsupported license type")
	}
}

func TestSettingsTTLHotUpdate(t *testing.T) {
	svc, _, _, mockSettings, _, _ := newLicenseServiceForTest(t)
	ctx := context.Background()

	// Initial default values
	if ttl := svc.statusCacheTTL(ctx); ttl != 60*time.Second {
		t.Errorf("expected initial status TTL 60s, got %v", ttl)
	}
	if ttl := svc.featuresCacheTTL(ctx); ttl != 60*time.Second {
		t.Errorf("expected initial features TTL 60s, got %v", ttl)
	}

	// Hot-update to new values
	mockSettings.setSettings(schema.LicenseSettings{
		StatusCacheTTL:   45,
		FeaturesCacheTTL: 90,
	})

	if ttl := svc.statusCacheTTL(ctx); ttl != 45*time.Second {
		t.Errorf("expected updated status TTL 45s, got %v", ttl)
	}
	if ttl := svc.featuresCacheTTL(ctx); ttl != 90*time.Second {
		t.Errorf("expected updated features TTL 90s, got %v", ttl)
	}

	// Settings error falls back to 0 (which triggers TTLCache default)
	mockSettings.err = errors.New("settings unavailable")
	if ttl := svc.statusCacheTTL(ctx); ttl != 0 {
		t.Errorf("expected 0 on settings error, got %v", ttl)
	}
	if ttl := svc.featuresCacheTTL(ctx); ttl != 0 {
		t.Errorf("expected 0 on settings error, got %v", ttl)
	}
}
