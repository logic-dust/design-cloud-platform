package user

import (
	"errors"
	"testing"

	"design-cloud-engine/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseScopes(t *testing.T) {
	t.Parallel()

	scopes, err := ParseScopes([]string{"systemadmin", "SYSTEMADMIN"})
	require.NoError(t, err)
	assert.Equal(t, model.AdminScopes(model.ScopeAll), scopes)

	names := ScopeNames(scopes)
	assert.Equal(t, []string{"systemadmin"}, names)
}

func TestParseScopes_RejectsUnknownScope(t *testing.T) {
	t.Parallel()

	_, err := ParseScopes([]string{"security"})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, model.ErrInvalidScope))
}

func TestParseScopes_Granular(t *testing.T) {
	t.Parallel()

	scopes, err := ParseScopes([]string{"node", "connections"})
	require.NoError(t, err)
	expected := model.AdminScopes(model.ScopeNodes | model.ScopeConnections)
	assert.Equal(t, expected, scopes)
	assert.True(t, scopes.Has(model.ScopeNodes))
	assert.False(t, scopes.Has(model.ScopeApps))
	assert.True(t, scopes.Has(model.ScopeConnections))
}

func TestScopeNames_Granular(t *testing.T) {
	t.Parallel()

	scopes := model.AdminScopes(model.ScopeNodes | model.ScopeApps)
	names := ScopeNames(scopes)
	assert.ElementsMatch(t, []string{"node", "apps"}, names)
	assert.NotContains(t, names, "systemadmin")
}

func TestScopeNames_PermAll_OnlySysadmin(t *testing.T) {
	t.Parallel()

	scopes := model.AdminScopes(model.ScopeAll)
	names := ScopeNames(scopes)
	assert.Equal(t, []string{"systemadmin"}, names)
	assert.Len(t, names, 1)
}

func TestParseScopes_Sessions(t *testing.T) {
	t.Parallel()

	scopes, err := ParseScopes([]string{"sessions"})
	require.NoError(t, err)
	assert.Equal(t, model.AdminScopes(model.ScopeSessions), scopes)

	names := ScopeNames(scopes)
	assert.Equal(t, []string{"sessions"}, names)
}

func TestParseScopes_MixedGranular(t *testing.T) {
	t.Parallel()

	scopes, err := ParseScopes([]string{"node", "bans", "files"})
	require.NoError(t, err)
	assert.True(t, scopes.Has(model.ScopeNodes))
	assert.False(t, scopes.Has(model.ScopeApps))
	assert.True(t, scopes.Has(model.ScopeBans))
	assert.True(t, scopes.Has(model.ScopeFiles))

	names := ScopeNames(scopes)
	assert.ElementsMatch(t, []string{"node", "bans", "files"}, names)
	assert.NotContains(t, names, "systemadmin")
}

func TestValidateScopes_RejectsUnknownBit(t *testing.T) {
	t.Parallel()

	err := validateScopes(model.AdminScopes(1 << 13))
	assert.Error(t, err)
	assert.True(t, errors.Is(err, model.ErrInvalidScope))
}

func TestScopeSchema(t *testing.T) {
	t.Parallel()

	schema := GetScopeSchema()
	require.NotEmpty(t, schema)
	assert.Contains(t, schema, ScopeMeta{Key: "node", Value: uint64(model.ScopeNodes), Name: "节点管理", Desc: "节点注册/证书/token"})
}
