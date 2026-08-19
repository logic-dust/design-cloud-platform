package security

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/settings"
)

func newTestManager(t *testing.T) *Service {
	t.Helper()
	tmpDir := t.TempDir()

	dbStore, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(tmpDir, "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new db store: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })
	if err := dbStore.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := db.NewRepos(dbStore)

	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  filepath.Join(tmpDir, "audit.log"),
		SecurityLogPath: filepath.Join(tmpDir, "security.log"),
	})
	if err != nil {
		t.Fatalf("new audit service: %v", err)
	}
	// Windows 无法删除仍被打开的文件，须在 TempDir 清理前关闭日志句柄。
	t.Cleanup(func() { _ = auditSvc.Close() })

	settingsSvc, err := settings.NewService(repos.Setting, auditSvc, nil)
	if err != nil {
		t.Fatalf("new settings service: %v", err)
	}

	banCache, err := NewBanCache(repos.Ban, slog.Default(), nil)
	if err != nil {
		t.Fatalf("new ban cache: %v", err)
	}
	t.Cleanup(banCache.Stop)

	mgr, err := NewService(settingsSvc, banCache, slog.Default())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	return mgr
}

func TestManager_AddFailedCount_BelowThreshold(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()
	ip := "192.0.2.1"
	user := "alice"
	key := ip + ":" + user

	// Add one fewer than the default threshold (5-1=4).
	for i := range defaultMaxUserFailedAttempts - 1 {
		mgr.AddFailedCount(ctx, key, model.BanTypeUser, ip, user)
		if isBanned, _ := mgr.IsBanned(ctx, ip, user); isBanned {
			t.Fatalf("should not be banned after %d attempt(s)", i+1)
		}
	}
}

func TestManager_AddFailedCount_TriggersUserBan(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()
	ip := "192.0.2.2"
	user := "bob"
	key := ip + ":" + user

	for range defaultMaxUserFailedAttempts {
		mgr.AddFailedCount(ctx, key, model.BanTypeUser, ip, user)
	}

	isBanned, rec := mgr.IsBanned(ctx, ip, user)
	if !isBanned {
		t.Fatal("expected user to be banned after reaching threshold")
	}
	if rec.Type != model.BanTypeUser {
		t.Fatalf("ban type = %v, want %v", rec.Type, model.BanTypeUser)
	}
}

func TestManager_IsBanned_ActiveBan(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()
	ip := "192.0.2.3"
	user := "carol"
	key := ip + ":" + user

	if err := mgr.banCache.SetBannedUntil(ctx, key, new(time.Now().Add(time.Hour))); err != nil {
		t.Fatalf("SetBannedUntil: %v", err)
	}

	isBanned, _ := mgr.IsBanned(ctx, ip, user)
	if !isBanned {
		t.Fatal("expected IsBanned = true for active ban")
	}
}

func TestManager_IsBanned_ExpiredBan(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()
	ip := "192.0.2.4"
	user := "dave"
	key := ip + ":" + user

	if err := mgr.banCache.SetBannedUntil(ctx, key, new(time.Now().Add(-time.Minute))); err != nil {
		t.Fatalf("SetBannedUntil: %v", err)
	}

	isBanned, _ := mgr.IsBanned(ctx, ip, user)
	if isBanned {
		t.Fatal("expected IsBanned = false for expired ban")
	}
}

func TestManager_IsBannedByIP_NoBan(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()
	isBanned, _ := mgr.IsBannedByIP(ctx, "10.0.0.99")
	if isBanned {
		t.Fatal("expected false for IP with no ban record")
	}
}

func TestManager_IsBannedByIP_ActiveBan(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()
	ip := "10.0.0.1"

	// Add enough IP-level failures to trigger a ban.
	for range defaultMaxIPFailedAttempts {
		mgr.AddFailedCount(ctx, ip, model.BanTypeIP, ip, "")
	}

	isBanned, _ := mgr.IsBannedByIP(ctx, ip)
	if !isBanned {
		t.Fatal("expected IsBannedByIP = true after threshold")
	}
}

func TestManager_GetAuthSettings_CacheHit(t *testing.T) {
	// No t.Parallel() — mutates manager's cache.
	mgr := newTestManager(t)
	ctx := context.Background()

	// First call populates the cache (returns DB defaults).
	first := mgr.getAuthSettings(ctx)

	// Second call within TTL should return the cached value, not re-fetch DB.
	got := mgr.getAuthSettings(ctx)
	if got.MaxUserLoginAttempts != first.MaxUserLoginAttempts {
		t.Fatalf("cache hit: MaxUserLoginAttempts = %d, want %d (same as first call)", got.MaxUserLoginAttempts, first.MaxUserLoginAttempts)
	}
}

func TestManager_GetAuthSettings_CacheMiss(t *testing.T) {
	// No t.Parallel() — mutates manager's cache.
	mgr := newTestManager(t)
	ctx := context.Background()

	// Populate cache first.
	first := mgr.getAuthSettings(ctx)

	// Invalidate the cache so the next call must re-fetch from DB.
	mgr.authSettingsCache.Invalidate(authSettingsKey)

	got := mgr.getAuthSettings(ctx)
	if got.MaxUserLoginAttempts != first.MaxUserLoginAttempts {
		t.Fatalf("cache miss re-fetch: MaxUserLoginAttempts = %d, want %d (DB default)", got.MaxUserLoginAttempts, first.MaxUserLoginAttempts)
	}
}

func TestManager_Unban(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()
	ip := "192.0.2.10"
	user := "eve"

	// Ban the user first.
	for range defaultMaxUserFailedAttempts {
		mgr.AddFailedCount(ctx, ip+":"+user, model.BanTypeUser, ip, user)
	}
	if isBanned, _ := mgr.IsBanned(ctx, ip, user); !isBanned {
		t.Fatal("precondition: expected user to be banned")
	}

	mgr.Unban(ctx, ip+":"+user)

	if isBanned, _ := mgr.IsBanned(ctx, ip, user); isBanned {
		t.Fatal("expected user to be unbanned after Unban()")
	}
}
