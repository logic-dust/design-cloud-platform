package db

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/settings/schema"
)

func TestMigrateSeedsDefaultSettings(t *testing.T) {
	t.Parallel()

	store, err := NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})

	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Seed default settings directly (cannot use settings.EnsureDefaults due to import cycle in test)
	seedDefaultSettings(t, store)

	repo := NewRepos(store).Setting
	for _, domain := range schema.AllDomains() {
		if _, ok := schema.DefaultSettings(domain); !ok {
			continue
		}
		var value map[string]any
		if err := repo.Load(context.Background(), domain.String(), &value); err != nil {
			t.Fatalf("load %s settings: %v", domain.String(), err)
		}
		if len(value) == 0 {
			t.Fatalf("%s settings were not seeded", domain.String())
		}
	}
}

func seedDefaultSettings(t *testing.T, store *Store) {
	t.Helper()
	for _, domain := range schema.AllDomains() {
		settings, ok := schema.DefaultSettings(domain)
		if !ok {
			continue
		}
		jsonBytes, err := json.Marshal(settings)
		if err != nil {
			t.Fatalf("marshal default %s settings: %v", domain.String(), err)
		}
		var compactBuf bytes.Buffer
		if err := json.Compact(&compactBuf, jsonBytes); err == nil {
			jsonBytes = compactBuf.Bytes()
		}
		if err := store.DB().Create(&model.Setting{
			Domain: domain.String(),
			Value:  string(jsonBytes),
		}).Error; err != nil {
			t.Fatalf("create default %s settings: %v", domain.String(), err)
		}
	}
}
