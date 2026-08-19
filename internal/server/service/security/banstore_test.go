package security

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

func newTestBanStore(t *testing.T) *db.BanRepo {
	t.Helper()
	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db.NewRepos(store).Ban
}

func newTestBanCache(t *testing.T) *BanCache {
	t.Helper()
	s, err := NewBanCache(newTestBanStore(t), slog.Default(), nil)
	if err != nil {
		t.Fatalf("new auth failure store: %v", err)
	}
	t.Cleanup(s.Stop)
	return s
}

func TestBanStore_AddNewRecord(t *testing.T) {
	t.Parallel()

	s := newTestBanCache(t)
	rec, err := s.AddBanRecord(model.BanRecord{
		Key:      "192.168.1.1:alice",
		IP:       "192.168.1.1",
		Username: "alice",
		Type:     model.BanTypeUser,
	})
	if err != nil {
		t.Fatalf("AddBanRecord: %v", err)
	}
	if rec.Count != 1 {
		t.Fatalf("count = %d, want 1", rec.Count)
	}
}

func TestBanStore_IncrementCount(t *testing.T) {
	t.Parallel()

	s := newTestBanCache(t)
	key := "192.168.1.2:bob"
	for i := 1; i <= 3; i++ {
		rec, err := s.AddBanRecord(model.BanRecord{Key: key, IP: "192.168.1.2", Username: "bob", Type: model.BanTypeUser})
		if err != nil {
			t.Fatalf("AddBanRecord #%d: %v", i, err)
		}
		if rec.Count != uint(i) {
			t.Fatalf("count after add #%d = %d, want %d", i, rec.Count, i)
		}
	}
}

func TestBanStore_GetNotFound(t *testing.T) {
	t.Parallel()

	s := newTestBanCache(t)
	_, ok := s.GetBanRecord("no-such-key")
	if ok {
		t.Fatal("expected GetBanRecord to return false for missing key")
	}
}

func TestBanStore_GetFound(t *testing.T) {
	t.Parallel()

	s := newTestBanCache(t)
	key := "10.0.0.1"
	if _, err := s.AddBanRecord(model.BanRecord{Key: key, IP: "10.0.0.1", Type: model.BanTypeIP}); err != nil {
		t.Fatalf("AddBanRecord: %v", err)
	}

	got, ok := s.GetBanRecord(key)
	if !ok {
		t.Fatal("expected GetBanRecord to return true")
	}
	if got.Key != key {
		t.Fatalf("key = %q, want %q", got.Key, key)
	}
}

func TestBanStore_SetBannedUntil(t *testing.T) {
	t.Parallel()

	s := newTestBanCache(t)
	key := "10.0.0.2:charlie"
	if _, err := s.AddBanRecord(model.BanRecord{Key: key, IP: "10.0.0.2", Username: "charlie", Type: model.BanTypeUser}); err != nil {
		t.Fatalf("AddBanRecord: %v", err)
	}

	if err := s.SetBannedUntil(context.Background(), key, new(time.Now().Add(5*time.Minute))); err != nil {
		t.Fatalf("SetBannedUntil: %v", err)
	}

	got, ok := s.GetBanRecord(key)
	if !ok {
		t.Fatal("expected record after SetBannedUntil")
	}
	if got.BannedUntil == nil || !got.BannedUntil.After(time.Now()) {
		t.Fatalf("BannedUntil = %v, want future timestamp", got.BannedUntil)
	}
}

func TestBanStore_DeleteByKey(t *testing.T) {
	t.Parallel()

	s := newTestBanCache(t)
	key := "10.0.0.3:dave"
	if _, err := s.AddBanRecord(model.BanRecord{Key: key, IP: "10.0.0.3", Username: "dave", Type: model.BanTypeUser}); err != nil {
		t.Fatalf("AddBanRecord: %v", err)
	}

	if err := s.DeleteByKey(context.Background(), key); err != nil {
		t.Fatalf("DeleteByKey: %v", err)
	}

	if _, ok := s.GetBanRecord(key); ok {
		t.Fatal("expected record to be gone after Delete")
	}
}

func TestBanStore_DeleteAll(t *testing.T) {
	t.Parallel()

	s := newTestBanCache(t)
	for _, key := range []string{"k1", "k2", "k3"} {
		if _, err := s.AddBanRecord(model.BanRecord{Key: key, IP: "1.2.3.4", Type: model.BanTypeIP}); err != nil {
			t.Fatalf("AddBanRecord(%s): %v", key, err)
		}
	}

	if err := s.DeleteAll(context.Background()); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}

	if list := s.List(); len(list) != 0 {
		t.Fatalf("after DeleteAll: len = %d, want 0", len(list))
	}
}

func TestBanStore_DirtyCountTrigger(t *testing.T) {
	t.Parallel()

	s := newTestBanCache(t)
	// Add exactly flushDirtyThreshold entries with distinct keys.
	// After the last one, dirtyCount >= threshold triggers a non-blocking send
	// on flushNow, which causes flushLoop to call flush().
	for i := range flushDirtyThreshold {
		key := "host:user" + string('a'+rune(i))
		if _, err := s.AddBanRecord(model.BanRecord{Key: key, IP: "1.2.3.4", Type: model.BanTypeIP}); err != nil {
			t.Fatalf("AddBanRecord #%d: %v", i, err)
		}
	}

	// Verify all records are present in the cache after reaching the threshold.
	// (The flush is asynchronous; we don't race against flushLoop consuming the
	// signal — we just verify the records are correctly maintained.)
	for i := range flushDirtyThreshold {
		key := "host:user" + string('a'+rune(i))
		if _, ok := s.GetBanRecord(key); !ok {
			t.Fatalf("expected record %q to be present after adding flushDirtyThreshold entries", key)
		}
	}
}

// --- shouldPruneBanRecord ---

func TestShouldPruneBanRecord_BannedNotExpired(t *testing.T) {
	rec := model.BanRecord{BannedUntil: new(time.Now().Add(time.Hour))}
	if shouldPruneBanRecord(rec, time.Now()) {
		t.Fatal("expected false for active ban")
	}
}

func TestShouldPruneBanRecord_BannedExpired(t *testing.T) {
	rec := model.BanRecord{BannedUntil: new(time.Now().Add(-time.Hour))}
	if !shouldPruneBanRecord(rec, time.Now()) {
		t.Fatal("expected true for expired ban")
	}
}

func TestShouldPruneBanRecord_NoBanRecentUpdate(t *testing.T) {
	rec := model.BanRecord{UpdatedAt: time.Now()}
	if shouldPruneBanRecord(rec, time.Now()) {
		t.Fatal("expected false for recently updated unbanned record")
	}
}

func TestShouldPruneBanRecord_NoBanOldUpdate(t *testing.T) {
	rec := model.BanRecord{UpdatedAt: time.Now().Add(-(authFailureUnbannedRetention + time.Minute))}
	if !shouldPruneBanRecord(rec, time.Now()) {
		t.Fatal("expected true for stale unbanned record")
	}
}

func TestShouldPruneBanRecord_ZeroUpdatedAt(t *testing.T) {
	rec := model.BanRecord{} // zero time, no ban
	if !shouldPruneBanRecord(rec, time.Now()) {
		t.Fatal("expected true for zero-time record with no ban")
	}
}
