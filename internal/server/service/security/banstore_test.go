package security

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
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
	rec, err := s.AddBanRecord(model.Ban{
		Key:      "192.168.1.1:alice",
		IP:       "192.168.1.1",
		Username: "alice",
		Subject:  model.BanSubjectUserIP,
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
		rec, err := s.AddBanRecord(model.Ban{Key: key, IP: "192.168.1.2", Username: "bob", Subject: model.BanSubjectUserIP})
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
	if _, err := s.AddBanRecord(model.Ban{Key: key, IP: "10.0.0.1", Subject: model.BanSubjectIP}); err != nil {
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

func TestBanStore_ApplyBan(t *testing.T) {
	t.Parallel()

	s := newTestBanCache(t)
	key := "10.0.0.2:charlie"
	if _, err := s.AddBanRecord(model.Ban{Key: key, IP: "10.0.0.2", Username: "charlie", Subject: model.BanSubjectUserIP}); err != nil {
		t.Fatalf("AddBanRecord: %v", err)
	}

	until := time.Now().Add(5 * time.Minute)
	if err := s.ApplyBan(context.Background(), model.Ban{
		Key:         key,
		Subject:     model.BanSubjectUserIP,
		Source:      model.BanSourceLogin,
		IP:          "10.0.0.2",
		Username:    "charlie",
		BannedUntil: &until,
		Reason:      "too many failed tries",
	}); err != nil {
		t.Fatalf("ApplyBan: %v", err)
	}

	got, ok := s.GetBanRecord(key)
	if !ok {
		t.Fatal("expected record after ApplyBan")
	}
	if got.BannedUntil == nil || !got.BannedUntil.After(time.Now()) {
		t.Fatalf("BannedUntil = %v, want future timestamp", got.BannedUntil)
	}
	if got.Reason != "too many failed tries" {
		t.Fatalf("Reason = %q, want persisted reason", got.Reason)
	}
	if got.Count != 1 {
		t.Fatalf("Count = %d, want 1 (ApplyBan must not touch failure count)", got.Count)
	}
}

func TestBanStore_ExpiredBanResetsFailureCount(t *testing.T) {
	t.Parallel()

	s := newTestBanCache(t)
	key := "10.0.0.9:erin"
	if _, err := s.AddBanRecord(model.Ban{Key: key, IP: "10.0.0.9", Username: "erin", Subject: model.BanSubjectUserIP}); err != nil {
		t.Fatalf("AddBanRecord: %v", err)
	}

	past := time.Now().Add(-time.Minute)
	if err := s.ApplyBan(context.Background(), model.Ban{
		Key: key, Subject: model.BanSubjectUserIP, IP: "10.0.0.9", Username: "erin",
		BannedUntil: &past, Reason: "expired window",
	}); err != nil {
		t.Fatalf("ApplyBan: %v", err)
	}

	// 封禁过期后的第一次失败应从 1 重新计数，而不是沿用旧 Count。
	rec, err := s.AddBanRecord(model.Ban{Key: key, IP: "10.0.0.9", Username: "erin", Subject: model.BanSubjectUserIP})
	if err != nil {
		t.Fatalf("AddBanRecord after expiry: %v", err)
	}
	if rec.Count != 1 {
		t.Fatalf("count after expired window = %d, want 1", rec.Count)
	}
	if rec.BannedUntil != nil {
		t.Fatalf("expired BannedUntil should be cleared, got %v", rec.BannedUntil)
	}

	// 活跃封禁不受影响：窗口内失败继续累加。
	until := time.Now().Add(time.Hour)
	if err := s.ApplyBan(context.Background(), model.Ban{
		Key: key, Subject: model.BanSubjectUserIP, IP: "10.0.0.9", Username: "erin",
		BannedUntil: &until, Reason: "active",
	}); err != nil {
		t.Fatalf("ApplyBan active: %v", err)
	}
	rec, err = s.AddBanRecord(model.Ban{Key: key, IP: "10.0.0.9", Username: "erin", Subject: model.BanSubjectUserIP})
	if err != nil {
		t.Fatalf("AddBanRecord during active ban: %v", err)
	}
	if rec.Count != 2 {
		t.Fatalf("count during active ban = %d, want 2", rec.Count)
	}
	if rec.BannedUntil == nil || !rec.BannedUntil.After(time.Now()) {
		t.Fatalf("active BannedUntil should be preserved, got %v", rec.BannedUntil)
	}
}

func TestBanStore_DeleteByKey(t *testing.T) {
	t.Parallel()

	s := newTestBanCache(t)
	key := "10.0.0.3:dave"
	if _, err := s.AddBanRecord(model.Ban{Key: key, IP: "10.0.0.3", Username: "dave", Subject: model.BanSubjectUserIP}); err != nil {
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
		if _, err := s.AddBanRecord(model.Ban{Key: key, IP: "1.2.3.4", Subject: model.BanSubjectIP}); err != nil {
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

func TestBanStore_DelayedFlushPersistsToDB(t *testing.T) {
	t.Parallel()

	repo := newTestBanStore(t)
	s, err := NewBanCache(repo, slog.Default(), nil)
	if err != nil {
		t.Fatalf("NewBanCache: %v", err)
	}
	s.flushDelay = 25 * time.Millisecond
	key := "delayed-flush-key"
	if _, err := s.AddBanRecord(model.Ban{Key: key, IP: "9.9.9.9", Subject: model.BanSubjectIP}); err != nil {
		t.Fatalf("AddBanRecord: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		rows, err := repo.List(context.Background())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(rows) == 1 && rows[0].Key == key {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("record not flushed to DB within deadline; got %d rows", len(rows))
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.Stop()
}

func TestBanStore_NoScheduledFlushWhileIdle(t *testing.T) {
	t.Parallel()

	repo := newTestBanStore(t)
	s, err := NewBanCache(repo, slog.Default(), nil)
	if err != nil {
		t.Fatalf("NewBanCache: %v", err)
	}
	// 间隔取较长值：若空闲仍排程，timer 会一直挂起并被断言捕获。
	s.flushDelay = 5 * time.Second
	time.Sleep(100 * time.Millisecond)

	s.flushTimerMu.Lock()
	pending := s.flushTimer != nil
	s.flushTimerMu.Unlock()
	if pending {
		t.Fatal("idle cache must not schedule a delayed flush timer")
	}

	rows, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("idle cache should not flush; got %d rows in DB", len(rows))
	}
	s.Stop()
}

func TestBanStore_SettleDelayedFlushTracksDirtyState(t *testing.T) {
	t.Parallel()

	s := newTestBanCache(t)
	s.flushDelay = time.Hour

	if _, err := s.AddBanRecord(model.Ban{Key: "settle-key", IP: "8.8.8.8", Subject: model.BanSubjectIP}); err != nil {
		t.Fatalf("AddBanRecord: %v", err)
	}
	// 模拟 flush 周期结束前的“脏但未排程”状态：settle 必须重新挂 timer。
	s.cancelDelayedFlush()
	s.settleDelayedFlush()
	s.flushTimerMu.Lock()
	pending := s.flushTimer != nil
	s.flushTimerMu.Unlock()
	if !pending {
		t.Fatal("dirty entry must re-arm the delayed flush timer")
	}

	// 刷盘成功后无脏，settle 必须撤销 timer。
	s.flush()
	s.settleDelayedFlush()
	s.flushTimerMu.Lock()
	pending = s.flushTimer != nil
	s.flushTimerMu.Unlock()
	if pending {
		t.Fatal("clean cache must not keep a delayed flush timer")
	}
}

func TestBanStore_DirtyCountTriggerFlushesImmediately(t *testing.T) {
	t.Parallel()

	repo := newTestBanStore(t)
	s, err := NewBanCache(repo, slog.Default(), nil)
	if err != nil {
		t.Fatalf("NewBanCache: %v", err)
	}
	// 定时刷盘取很长间隔：落库只能由“脏标记满阈值”路径触发。
	s.flushDelay = 10 * time.Second
	for i := range flushDirtyThreshold {
		key := fmt.Sprintf("dirty-%d:user", i)
		if _, err := s.AddBanRecord(model.Ban{Key: key, IP: "1.2.3.4", Subject: model.BanSubjectIP}); err != nil {
			t.Fatalf("AddBanRecord #%d: %v", i, err)
		}
		if _, ok := s.GetBanRecord(key); !ok {
			t.Fatalf("expected record %q to be present in cache", key)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, err := repo.List(context.Background())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(rows) == flushDirtyThreshold {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dirty threshold did not trigger flush: got %d rows, want %d", len(rows), flushDirtyThreshold)
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.Stop()
}

func TestBanStore_StopFlushesDirtyRecords(t *testing.T) {
	t.Parallel()

	repo := newTestBanStore(t)
	s, err := NewBanCache(repo, slog.Default(), nil)
	if err != nil {
		t.Fatalf("NewBanCache: %v", err)
	}
	// 定时器远未触发，落库只能来自 Stop 的最终 flush。
	s.flushDelay = time.Hour
	key := "stop-flush-key"
	if _, err := s.AddBanRecord(model.Ban{Key: key, IP: "7.7.7.7", Subject: model.BanSubjectIP}); err != nil {
		t.Fatalf("AddBanRecord: %v", err)
	}

	s.Stop()

	rows, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].Key != key {
		t.Fatalf("Stop must flush dirty records: got %d rows, want key %q", len(rows), key)
	}
}

func TestBanCache_PruneExpiredDeletesRows(t *testing.T) {
	t.Parallel()

	repo := newTestBanStore(t)
	s, err := NewBanCache(repo, slog.Default(), nil)
	if err != nil {
		t.Fatalf("NewBanCache: %v", err)
	}
	past := time.Now().Add(-time.Minute)
	key := "prune-expired"
	if err := s.ApplyBan(context.Background(), model.Ban{
		Key: key, IP: "6.6.6.6", Subject: model.BanSubjectIP,
		BannedUntil: &past, Reason: "expired",
	}); err != nil {
		t.Fatalf("ApplyBan: %v", err)
	}

	if deleted := s.pruneExpired(context.Background(), time.Now()); deleted != 1 {
		t.Fatalf("pruneExpired deleted = %d, want 1", deleted)
	}
	if _, ok := s.GetBanRecord(key); ok {
		t.Fatal("expired record must be removed from memory")
	}
	rows, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expired record must be removed from DB; got %d rows", len(rows))
	}
	s.Stop()
}

func TestBanCache_PruneSkipsDirtyRecords(t *testing.T) {
	t.Parallel()

	s := newTestBanCache(t)
	s.flushDelay = 10 * time.Second
	past := time.Now().Add(-time.Minute)
	key := "prune-dirty"
	if err := s.ApplyBan(context.Background(), model.Ban{
		Key: key, IP: "5.5.5.5", Subject: model.BanSubjectIP,
		BannedUntil: &past, Reason: "expired then failed again",
	}); err != nil {
		t.Fatalf("ApplyBan: %v", err)
	}
	if _, err := s.AddBanRecord(model.Ban{Key: key, IP: "5.5.5.5", Subject: model.BanSubjectIP}); err != nil {
		t.Fatalf("AddBanRecord: %v", err)
	}

	if deleted := s.pruneExpired(context.Background(), time.Now()); deleted != 0 {
		t.Fatalf("pruneExpired deleted = %d, want 0 for a dirty record", deleted)
	}
	rec, ok := s.GetBanRecord(key)
	if !ok {
		t.Fatal("dirty record must not be pruned")
	}
	if rec.Count != 1 {
		t.Fatalf("dirty record count = %d, want 1", rec.Count)
	}
}

// --- shouldPruneBanRecord ---

func TestShouldPruneBanRecord_BannedNotExpired(t *testing.T) {
	rec := model.Ban{BannedUntil: new(time.Now().Add(time.Hour))}
	if shouldPruneBanRecord(rec, time.Now()) {
		t.Fatal("expected false for active ban")
	}
}

func TestShouldPruneBanRecord_BannedExpired(t *testing.T) {
	rec := model.Ban{BannedUntil: new(time.Now().Add(-time.Hour))}
	if !shouldPruneBanRecord(rec, time.Now()) {
		t.Fatal("expected true for expired ban")
	}
}

func TestShouldPruneBanRecord_NoBanRecentUpdate(t *testing.T) {
	rec := model.Ban{LastFailedAt: time.Now()}
	if shouldPruneBanRecord(rec, time.Now()) {
		t.Fatal("expected false for recently updated unbanned record")
	}
}

func TestShouldPruneBanRecord_NoBanOldUpdate(t *testing.T) {
	rec := model.Ban{LastFailedAt: time.Now().Add(-(authFailureUnbannedRetention + time.Minute))}
	if !shouldPruneBanRecord(rec, time.Now()) {
		t.Fatal("expected true for stale unbanned record")
	}
}

func TestShouldPruneBanRecord_ZeroLastFailedAt(t *testing.T) {
	rec := model.Ban{} // zero time, no ban
	if !shouldPruneBanRecord(rec, time.Now()) {
		t.Fatal("expected true for zero-time record with no ban")
	}
}

func TestBanStore_CapacityDropsOnlyNewFailureRecords(t *testing.T) {
	t.Parallel()
	s := newTestBanCache(t)
	s.maxRecords = 2

	_, _ = s.AddBanRecord(model.Ban{Key: "1.1.1.1", IP: "1.1.1.1", Subject: model.BanSubjectIP})
	_, _ = s.AddBanRecord(model.Ban{Key: "2.2.2.2", IP: "2.2.2.2", Subject: model.BanSubjectIP})

	// 满载：新 key 的失败计数被丢弃
	_, _ = s.AddBanRecord(model.Ban{Key: "3.3.3.3", IP: "3.3.3.3", Subject: model.BanSubjectIP})
	if _, ok := s.GetBanRecord("3.3.3.3"); ok {
		t.Fatal("new failure record should be dropped at capacity")
	}

	// 已有 key 仍可累加
	rec, _ := s.AddBanRecord(model.Ban{Key: "1.1.1.1", IP: "1.1.1.1", Subject: model.BanSubjectIP})
	if rec.Count != 2 {
		t.Fatalf("existing record count = %d, want 2", rec.Count)
	}

	// 封禁记录不受容量限制
	until := time.Now().Add(time.Hour)
	if err := s.ApplyBan(context.Background(), model.Ban{Key: "4.4.4.4", IP: "4.4.4.4", Subject: model.BanSubjectIP, BannedUntil: &until}); err != nil {
		t.Fatalf("ApplyBan: %v", err)
	}
	if _, ok := s.GetBanRecord("4.4.4.4"); !ok {
		t.Fatal("ban record must be stored regardless of capacity")
	}
}
