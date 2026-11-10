package security

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/service/settings"
)

func newTestManager(t *testing.T) *Service {
	t.Helper()
	mgr, _ := newTestManagerWithSettings(t)
	return mgr
}

func newTestManagerWithSettings(t *testing.T) (*Service, *settings.Service) {
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

	settingsSvc, err := settings.NewService(repos.Setting, slog.Default(), auditSvc, nil, repos.CacheVersion, repos.Node)
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
	return mgr, settingsSvc
}

func TestManager_AddFailedCount_BelowThreshold(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()
	ip := "192.0.2.1"
	user := "alice"
	key := ip + ":" + user

	// Add one fewer than the default threshold (5-1=4).
	for i := range defaultAuthSettings.MaxUserLoginAttempts - 1 {
		mgr.RecordFailure(ctx, key, model.BanSubjectUserIP, model.BanSourceLogin, ip, user)
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

	for range defaultAuthSettings.MaxUserLoginAttempts {
		mgr.RecordFailure(ctx, key, model.BanSubjectUserIP, model.BanSourceLogin, ip, user)
	}

	isBanned, rec := mgr.IsBanned(ctx, ip, user)
	if !isBanned {
		t.Fatal("expected user to be banned after reaching threshold")
	}
	if rec.Subject != model.BanSubjectUserIP {
		t.Fatalf("ban type = %v, want %v", rec.Subject, model.BanSubjectUserIP)
	}
	if rec.Reason == "" {
		t.Fatal("expected auto-ban to carry a persisted default reason")
	}
}

func TestManager_IsBanned_ActiveBan(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()
	ip := "192.0.2.3"
	user := "carol"
	key := ip + ":" + user

	if err := mgr.banCache.ApplyBan(ctx, model.Ban{Key: key, Subject: model.BanSubjectUserIP, IP: ip, Username: user, BannedUntil: new(time.Now().Add(time.Hour))}); err != nil {
		t.Fatalf("ApplyBan: %v", err)
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

	if err := mgr.banCache.ApplyBan(ctx, model.Ban{Key: key, Subject: model.BanSubjectUserIP, IP: ip, Username: user, BannedUntil: new(time.Now().Add(-time.Minute))}); err != nil {
		t.Fatalf("ApplyBan: %v", err)
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
	for range defaultAuthSettings.MaxIPLoginAttempts {
		mgr.RecordFailure(ctx, ip, model.BanSubjectIP, model.BanSourceLogin, ip, "")
	}

	isBanned, _ := mgr.IsBannedByIP(ctx, ip)
	if !isBanned {
		t.Fatal("expected IsBannedByIP = true after threshold")
	}
}

func TestManager_GetAuthSettings_ReadsSettingsService(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()

	first := mgr.getAuthSettings(ctx)
	got := mgr.getAuthSettings(ctx)
	if got.MaxUserLoginAttempts != first.MaxUserLoginAttempts {
		t.Fatalf("MaxUserLoginAttempts = %d, want %d", got.MaxUserLoginAttempts, first.MaxUserLoginAttempts)
	}
	if got.MaxUserLoginAttempts != defaultAuthSettings.MaxUserLoginAttempts {
		t.Fatalf("MaxUserLoginAttempts = %d, want schema default %d", got.MaxUserLoginAttempts, defaultAuthSettings.MaxUserLoginAttempts)
	}
}

func TestManager_Unban(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()
	ip := "192.0.2.10"
	user := "eve"

	// Ban the user first.
	for range defaultAuthSettings.MaxUserLoginAttempts {
		mgr.RecordFailure(ctx, ip+":"+user, model.BanSubjectUserIP, model.BanSourceLogin, ip, user)
	}
	if isBanned, _ := mgr.IsBanned(ctx, ip, user); !isBanned {
		t.Fatal("precondition: expected user to be banned")
	}

	mgr.Unban(ctx, ip+":"+user)

	if isBanned, _ := mgr.IsBanned(ctx, ip, user); isBanned {
		t.Fatal("expected user to be unbanned after Unban()")
	}
}

func TestManager_RecordFailureSkipsWhenAlreadyBanned(t *testing.T) {
	t.Parallel()

	mgr, _ := newTestManagerWithSettings(t)
	ctx := context.Background()
	ip := "192.0.2.70"

	for range defaultAuthSettings.MaxIPLoginAttempts {
		mgr.RecordFailure(ctx, ip, model.BanSubjectIP, model.BanSourceSessionToken, ip, "")
	}
	if banned, _ := mgr.IsBannedByIP(ctx, ip); !banned {
		t.Fatal("precondition: expected IP to be banned")
	}
	before, ok := mgr.banCache.GetBanRecord(ip)
	if !ok {
		t.Fatal("precondition: expected ban record")
	}

	mgr.RecordFailure(ctx, ip, model.BanSubjectIP, model.BanSourceSessionToken, ip, "")
	after, ok := mgr.banCache.GetBanRecord(ip)
	if !ok {
		t.Fatal("expected ban record to remain")
	}
	if after.Count != before.Count {
		t.Fatalf("count must not increase while banned: before=%d after=%d", before.Count, after.Count)
	}
}

func TestManager_AllowlistedIPNeverCounted(t *testing.T) {
	t.Parallel()

	mgr, settingsSvc := newTestManagerWithSettings(t)
	ctx := context.Background()
	ip := "192.0.2.50"

	if err := settingsSvc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"banAllowlist":["192.0.2.0/24"]}`)); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	if !mgr.IsAllowlisted(ctx, ip) {
		t.Fatal("expected IP to be allowlisted via CIDR")
	}

	// 超过阈值两倍的失败也不应产生计数或封禁
	for range defaultAuthSettings.MaxIPLoginAttempts * 2 {
		mgr.RecordFailure(ctx, ip, model.BanSubjectIP, model.BanSourceLogin, ip, "")
	}
	if banned, _ := mgr.IsBannedByIP(ctx, ip); banned {
		t.Fatal("allowlisted IP must never be banned")
	}
	if rec, ok := mgr.banCache.GetBanRecord(ip); ok && rec.Count > 0 {
		t.Fatalf("allowlisted IP must not be counted, got count=%d", rec.Count)
	}
}

func TestManager_AllowlistIgnoresActiveBan(t *testing.T) {
	t.Parallel()

	mgr, settingsSvc := newTestManagerWithSettings(t)
	ctx := context.Background()
	ip := "192.0.2.60"

	for range defaultAuthSettings.MaxIPLoginAttempts {
		mgr.RecordFailure(ctx, ip, model.BanSubjectIP, model.BanSourceLogin, ip, "")
	}
	if banned, _ := mgr.IsBannedByIP(ctx, ip); !banned {
		t.Fatal("precondition: expected IP to be banned")
	}

	// 加入放行名单后，已存在的封禁也被忽略（带 WARN 日志的短路）
	if err := settingsSvc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"banAllowlist":["192.0.2.60"]}`)); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	if banned, _ := mgr.IsBannedByIP(ctx, ip); banned {
		t.Fatal("expected active ban to be ignored for allowlisted IP")
	}
	if banned, _ := mgr.IsBanned(ctx, ip, "someuser"); banned {
		t.Fatal("expected IsBanned to ignore active ban for allowlisted IP")
	}
}

func TestManager_NotAllowlistedByDefault(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()
	if mgr.IsAllowlisted(ctx, "192.0.2.99") {
		t.Fatal("expected IP not to be allowlisted by default")
	}
	if mgr.IsAllowlisted(ctx, "not-an-ip") {
		t.Fatal("unparseable IP must never match allowlist")
	}
}

func TestManager_ManualBan_IPSubject(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()
	ip := "192.0.2.70"

	rec, err := mgr.ManualBan(ctx, model.BanSubjectIP, ip, "", time.Hour, "abusive scanner")
	if err != nil {
		t.Fatalf("ManualBan: %v", err)
	}
	if rec.Source != model.BanSourceManual {
		t.Fatalf("source = %v, want %v", rec.Source, model.BanSourceManual)
	}
	if rec.Reason != "abusive scanner" {
		t.Fatalf("reason = %q, want %q", rec.Reason, "abusive scanner")
	}

	banned, got := mgr.IsBannedByIP(ctx, ip)
	if !banned {
		t.Fatal("expected IP to be banned after ManualBan")
	}
	if got.Reason != "abusive scanner" {
		t.Fatalf("IsBannedByIP reason = %q, want %q", got.Reason, "abusive scanner")
	}

	// 列表中应可见且带 Manual 来源；手动封禁不得污染失败计数
	list := mgr.ListBannedRecords()
	entry, ok := list[ip]
	if !ok {
		t.Fatal("expected manual ban to appear in ListBannedRecords")
	}
	listed, ok := entry.(BannedRecord)
	if !ok {
		t.Fatalf("list entry type = %T, want BannedRecord", entry)
	}
	if listed.Source != model.BanSourceManual {
		t.Fatalf("listed source = %v, want %v", listed.Source, model.BanSourceManual)
	}
	if dbRec, ok := mgr.banCache.GetBanRecord(ip); !ok || dbRec.Count != 0 {
		t.Fatalf("manual ban must not touch failure count, got %+v", dbRec)
	}
}

func TestManager_ManualBan_UserIPSubject(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()
	ip := "192.0.2.71"
	user := "frank"

	// reason 为空时应填默认管理员原因
	if _, err := mgr.ManualBan(ctx, model.BanSubjectUserIP, ip, user, 10*time.Minute, ""); err != nil {
		t.Fatalf("ManualBan: %v", err)
	}

	banned, rec := mgr.IsBanned(ctx, ip, user)
	if !banned {
		t.Fatal("expected IP:username to be banned after ManualBan")
	}
	if rec.Source != model.BanSourceManual {
		t.Fatalf("source = %v, want %v", rec.Source, model.BanSourceManual)
	}
	if rec.Reason != "banned by administrator" {
		t.Fatalf("default reason = %q, want %q", rec.Reason, "banned by administrator")
	}

	// UserIP 封禁不影响同 IP 的纯 IP 键
	if banned, _ := mgr.IsBannedByIP(ctx, ip); banned {
		t.Fatal("UserIP ban must not ban the bare IP key")
	}
}

func TestManager_ManualBan_InvalidParams(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()

	cases := []struct {
		name     string
		subject  model.BanSubject
		ip       string
		username string
		duration time.Duration
	}{
		{"bad subject", "User", "192.0.2.1", "", time.Hour},
		{"bad ip", model.BanSubjectIP, "not-an-ip", "", time.Hour},
		{"userip without username", model.BanSubjectUserIP, "192.0.2.1", "", time.Hour},
		{"duration too short", model.BanSubjectIP, "192.0.2.1", "", 30 * time.Second},
		{"duration too long", model.BanSubjectIP, "192.0.2.1", "", 31 * 24 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := mgr.ManualBan(ctx, tc.subject, tc.ip, tc.username, tc.duration, ""); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestManager_ManualBan_UnbanRemoves(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	ctx := context.Background()
	ip := "192.0.2.72"

	if _, err := mgr.ManualBan(ctx, model.BanSubjectIP, ip, "", time.Hour, ""); err != nil {
		t.Fatalf("ManualBan: %v", err)
	}
	mgr.Unban(ctx, ip)
	if banned, _ := mgr.IsBannedByIP(ctx, ip); banned {
		t.Fatal("expected manual ban to be removed by Unban")
	}
}

func TestDefaultAuthSettingsSingleSource(t *testing.T) {
	t.Parallel()

	v, ok := schema.DefaultSettings(schema.DomainAuth)
	if !ok {
		t.Fatal("DomainAuth defaults missing")
	}
	want, ok := v.(schema.AuthSettings)
	if !ok {
		t.Fatalf("DefaultSettings(DomainAuth) type = %T, want AuthSettings", v)
	}
	if defaultAuthSettings.MaxUserLoginAttempts != want.MaxUserLoginAttempts ||
		defaultAuthSettings.MaxIPLoginAttempts != want.MaxIPLoginAttempts ||
		defaultAuthSettings.UserBanDuration != want.UserBanDuration ||
		defaultAuthSettings.IPBanDuration != want.IPBanDuration {
		t.Fatalf("security ban defaults drifted from schema.DefaultSettings(DomainAuth):\n got attempts=%d/%d duration=%d/%d\nwant attempts=%d/%d duration=%d/%d",
			defaultAuthSettings.MaxUserLoginAttempts, defaultAuthSettings.MaxIPLoginAttempts,
			defaultAuthSettings.UserBanDuration, defaultAuthSettings.IPBanDuration,
			want.MaxUserLoginAttempts, want.MaxIPLoginAttempts,
			want.UserBanDuration, want.IPBanDuration)
	}
	if defaultAuthSettings.MaxUserLoginAttempts <= 0 ||
		defaultAuthSettings.MaxIPLoginAttempts <= 0 ||
		defaultAuthSettings.UserBanDuration < 60 ||
		defaultAuthSettings.IPBanDuration < 60 {
		t.Fatalf("DomainAuth ban defaults unusable for fallback: %+v", defaultAuthSettings)
	}
}
