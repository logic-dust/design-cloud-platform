package user

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/settings/schema"
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
	core.settingsSvc = settingsSvc

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
	core.settingsSvc = settingsSvc

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

func TestSyncInterval(t *testing.T) {
	t.Parallel()
	core, _, settingsSvc, _ := setupTestCore(t)

	ctx := context.Background()

	// 1. Set interval to 300 seconds
	authSettings := schema.AuthSettings{
		SessionTTL:                86400,
		MaxLoginSessionsPerUser:   3,
		SessionCleanupInterval:    600,
		MaxUserLoginAttempts:      5,
		MaxIPLoginAttempts:        20,
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
