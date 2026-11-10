package user

import (
	"context"
	json "design-cloud-engine/internal/infra/jsoncodec"
	"log/slog"
	"testing"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	userprovider "design-cloud-engine/internal/server/service/user/provider"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncUsers_FreezeAndRestore(t *testing.T) {
	t.Parallel()
	core, repos, settingsSvc, _ := setupTestCore(t)

	mockProv := &mockProvider{}
	core.provider = mockProv
	core.leaseRepo = repos.TaskLease
	core.cfg = Config{AuthMode: config.UserAuthModeLDAP}
	core.authSettings = settingsSvc

	ctx := context.Background()

	// 1. Create users in DB
	// User 1: Active in DB, but missing in System -> should be frozen
	u1 := model.User{Username: "user1", Status: model.UserActive, Type: model.UserTypeSystem}
	err := repos.User.Create(ctx, &u1)
	require.NoError(t, err)

	// User 2: Locked in DB, but present in System -> should be restored
	u2 := model.User{Username: "user2", Status: model.UserLocked, Type: model.UserTypeSystem}
	err = repos.User.Create(ctx, &u2)
	require.NoError(t, err)
	// Force status to Locked in DB since Create might set default status
	err = repos.User.ChangeStatus(ctx, "user2", model.UserLocked)
	require.NoError(t, err)

	// User 3: Active in DB, present in System -> should remain active
	u3 := model.User{Username: "user3", Status: model.UserActive, Type: model.UserTypeSystem}
	err = repos.User.Create(ctx, &u3)
	require.NoError(t, err)

	// 2. Setup Mock Provider
	mockProv.listUsersFunc = func(ctx context.Context) ([]userprovider.SystemUser, error) {
		return []userprovider.SystemUser{
			{Username: "user2"}, // user2 is in system
			{Username: "user3"}, // user3 is in system
		}, nil
	}

	// 3. Run Sync
	core.SyncUsers(ctx, slog.Default())

	// 4. Verify results
	// user1 should be Locked
	dbUser1, err := repos.User.GetByName(ctx, "user1")
	assert.NoError(t, err)
	assert.Equal(t, model.UserLocked, dbUser1.Status)

	// user2 should be Active
	dbUser2, err := repos.User.GetByName(ctx, "user2")
	assert.NoError(t, err)
	assert.Equal(t, model.UserActive, dbUser2.Status)

	// user3 should be Active
	dbUser3, err := repos.User.GetByName(ctx, "user3")
	assert.NoError(t, err)
	assert.Equal(t, model.UserActive, dbUser3.Status)
}

func TestSyncUsers_AbortOnEmptySystemUsers(t *testing.T) {
	t.Parallel()
	core, repos, settingsSvc, _ := setupTestCore(t)

	mockProv := &mockProvider{}
	core.provider = mockProv
	core.leaseRepo = repos.TaskLease
	core.cfg = Config{AuthMode: config.UserAuthModeLDAP}
	core.authSettings = settingsSvc

	ctx := context.Background()

	// Create user in DB
	u1 := model.User{Username: "user1", Status: model.UserActive, Type: model.UserTypeSystem}
	err := repos.User.Create(ctx, &u1)
	require.NoError(t, err)

	// Mock provider returns 0 users
	mockProv.listUsersFunc = func(ctx context.Context) ([]userprovider.SystemUser, error) {
		return []userprovider.SystemUser{}, nil
	}

	// Run Sync
	core.SyncUsers(ctx, slog.Default())

	// user1 should STILL be Active (abort prevented mass freeze)
	dbUser1, err := repos.User.GetByName(ctx, "user1")
	assert.NoError(t, err)
	assert.Equal(t, model.UserActive, dbUser1.Status)
}

func TestSyncUsers_BumpsIdentity(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	ctx := context.Background()

	core.provider = &mockProvider{
		listUsersFunc: func(context.Context) ([]userprovider.SystemUser, error) {
			return []userprovider.SystemUser{{Username: "u", Uid: "1000", Gid: "2000"}}, nil
		},
	}
	core.idMappingCache.userCache.SetWithTTL(1000, IDMapping{Name: "old", Found: true}, time.Hour)

	before, err := core.cacheVersion.GetAll(ctx)
	require.NoError(t, err)

	core.SyncUsers(ctx, slog.Default())

	after, err := core.cacheVersion.GetAll(ctx)
	require.NoError(t, err)
	require.Equal(t, before[db.CacheVersionTopicIdentity]+1, after[db.CacheVersionTopicIdentity])
	_, ok := core.idMappingCache.userCache.GetIfPresent(1000)
	require.False(t, ok, "completed sync invalidates listed uid locally")
}

func TestSyncUsers_AbortDoesNotBumpIdentity(t *testing.T) {
	t.Parallel()
	core, _, _, _ := setupTestCore(t)
	ctx := context.Background()

	core.provider = &mockProvider{
		listUsersFunc: func(context.Context) ([]userprovider.SystemUser, error) {
			return nil, nil
		},
	}

	before, err := core.cacheVersion.GetAll(ctx)
	require.NoError(t, err)

	core.SyncUsers(ctx, slog.Default())

	after, err := core.cacheVersion.GetAll(ctx)
	require.NoError(t, err)
	require.Equal(t, before[db.CacheVersionTopicIdentity], after[db.CacheVersionTopicIdentity])
}

func TestSyncInterval(t *testing.T) {
	t.Parallel()
	core, _, settingsSvc, _ := setupTestCore(t)

	ctx := context.Background()

	// 1. Set interval to 300 seconds
	authSettings := schema.AuthSettings{
		SessionTTL:                86400,
		MaxSessionsPerUser:        3,
		SessionCleanupInterval:    600,
		MaxUserLoginAttempts:      7,
		MaxIPLoginAttempts:        15,
		UserBanDuration:           300,
		IPBanDuration:             86400,
		UserSyncInterval:          300,
		IDMappingFoundCacheTTL:    schema.IDMappingFoundCacheTTLDefault,
		IDMappingNotFoundCacheTTL: schema.IDMappingNotFoundCacheTTLDefault,
	}
	authJSON, err := json.Marshal(authSettings)
	require.NoError(t, err)

	err = settingsSvc.UpdateByDomain(ctx, schema.DomainAuth, authJSON)
	require.NoError(t, err)

	interval, enabled, err := core.syncInterval(ctx)
	assert.NoError(t, err)
	assert.True(t, enabled)
	assert.Equal(t, 300*time.Second, interval)

	// 2. Set interval to 0 (disabled)
	authSettings.UserSyncInterval = 0
	authJSON, err = json.Marshal(authSettings)
	require.NoError(t, err)

	err = settingsSvc.UpdateByDomain(ctx, schema.DomainAuth, authJSON)
	require.NoError(t, err)

	interval, enabled, err = core.syncInterval(ctx)
	assert.NoError(t, err)
	assert.False(t, enabled)
	assert.Equal(t, 0*time.Second, interval)
}

func TestSyncUsers_PasswordExpiredSyncAndRestore(t *testing.T) {
	t.Parallel()
	core, repos, settingsSvc, _ := setupTestCore(t)
	ctx := context.Background()

	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-24 * time.Hour)

	createWithStatus := func(name string, st model.UserStatusCode) {
		u := model.User{Username: name, Status: st, Type: model.UserTypeSystem}
		require.NoError(t, repos.User.Create(ctx, &u))
		require.NoError(t, repos.User.ChangeStatus(ctx, name, st))
	}

	createWithStatus("ipa-renewed", model.UserPasswordExpired)
	createWithStatus("local-reset", model.UserPasswordExpired)
	createWithStatus("newly-expired", model.UserActive)
	createWithStatus("portal-disabled", model.UserDisabled)

	core.provider = &mockProvider{
		listUsersFunc: func(context.Context) ([]userprovider.SystemUser, error) {
			return []userprovider.SystemUser{
				{Username: "ipa-renewed", Status: "active", PasswordExpiresAt: &future},
				{Username: "local-reset", Status: "active", PasswordExpiresAt: nil},
				{Username: "newly-expired", Status: "passwordExpired", PasswordExpiresAt: &past},
				{Username: "portal-disabled", Status: "active", PasswordExpiresAt: &future},
			}, nil
		},
	}
	core.leaseRepo = repos.TaskLease
	core.cfg = Config{AuthMode: config.UserAuthModeLDAP}
	core.authSettings = settingsSvc

	core.SyncUsers(ctx, slog.Default())

	u1, err := repos.User.GetByName(ctx, "ipa-renewed")
	require.NoError(t, err)
	assert.Equal(t, model.UserActive, u1.Status)

	u2, err := repos.User.GetByName(ctx, "local-reset")
	require.NoError(t, err)
	assert.Equal(t, model.UserPasswordExpired, u2.Status)

	u3, err := repos.User.GetByName(ctx, "newly-expired")
	require.NoError(t, err)
	assert.Equal(t, model.UserPasswordExpired, u3.Status)

	u4, err := repos.User.GetByName(ctx, "portal-disabled")
	require.NoError(t, err)
	assert.Equal(t, model.UserDisabled, u4.Status)
}

func TestGet_ReconcilesSystemUserStatusWithDB(t *testing.T) {
	t.Parallel()
	core, repos, _, _ := setupTestCore(t)
	ctx := context.Background()

	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-24 * time.Hour)

	createWithStatus := func(name string, st model.UserStatusCode) {
		u := model.User{Username: name, Status: st, Type: model.UserTypeSystem}
		require.NoError(t, repos.User.Create(ctx, &u))
		require.NoError(t, repos.User.ChangeStatus(ctx, name, st))
	}

	createWithStatus("ipa-renewed", model.UserPasswordExpired)
	createWithStatus("local-reset", model.UserPasswordExpired)
	createWithStatus("newly-expired", model.UserActive)
	createWithStatus("portal-disabled", model.UserDisabled)

	core.provider = &mockProvider{
		getUserFunc: func(_ context.Context, username string) (*userprovider.SystemUser, error) {
			switch username {
			case "ipa-renewed":
				return &userprovider.SystemUser{Username: username, Status: "active", PasswordExpiresAt: &future}, nil
			case "local-reset":
				return &userprovider.SystemUser{Username: username, Status: "active", PasswordExpiresAt: nil}, nil
			case "newly-expired":
				return &userprovider.SystemUser{Username: username, Status: "passwordExpired", PasswordExpiresAt: &past}, nil
			case "portal-disabled":
				return &userprovider.SystemUser{Username: username, Status: "active", PasswordExpiresAt: &future}, nil
			default:
				return nil, userprovider.ErrUserNotFound
			}
		},
	}

	su1, err := core.Get(ctx, "ipa-renewed")
	require.NoError(t, err)
	assert.Equal(t, "active", su1.Status)
	db1, _ := repos.User.GetByName(ctx, "ipa-renewed")
	assert.Equal(t, model.UserActive, db1.Status)

	su2, err := core.Get(ctx, "local-reset")
	require.NoError(t, err)
	assert.Equal(t, "passwordExpired", su2.Status)
	db2, _ := repos.User.GetByName(ctx, "local-reset")
	assert.Equal(t, model.UserPasswordExpired, db2.Status)

	su3, err := core.Get(ctx, "newly-expired")
	require.NoError(t, err)
	assert.Equal(t, "passwordExpired", su3.Status)
	db3, _ := repos.User.GetByName(ctx, "newly-expired")
	assert.Equal(t, model.UserPasswordExpired, db3.Status)

	su4, err := core.Get(ctx, "portal-disabled")
	require.NoError(t, err)
	assert.Equal(t, "disabled", su4.Status)
	db4, _ := repos.User.GetByName(ctx, "portal-disabled")
	assert.Equal(t, model.UserDisabled, db4.Status)
}
