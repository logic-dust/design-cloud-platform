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

	scopes, err := ParseScopes([]string{"systemadmin"})
	require.NoError(t, err)
	assert.Equal(t, model.AdminScopes(model.ScopeAll), scopes)

	names := ScopeNames(scopes)
	assert.Equal(t, []string{"systemadmin"}, names)
}

func TestParseScopes_Granular(t *testing.T) {
	t.Parallel()

	scopes, err := ParseScopes([]string{"node", "connection"})
	require.NoError(t, err)
	expected := model.AdminScopes(model.ScopeNodes | model.ScopeConnections)
	assert.Equal(t, expected, scopes)
	assert.True(t, scopes.Has(model.ScopeNodes))
	assert.False(t, scopes.Has(model.ScopeApps))
	assert.True(t, scopes.Has(model.ScopeConnections))
}

func TestParseScopes_RejectsNonCanonicalKeys(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"connections", "users", "Node", "CONNECTION", "security"} {
		_, err := ParseScopes([]string{name})
		assert.Error(t, err, name)
		assert.True(t, errors.Is(err, model.ErrInvalidScope), name)
	}
}

func TestScopeNames_PermAll_OnlySysadmin(t *testing.T) {
	t.Parallel()

	scopes := model.AdminScopes(model.ScopeAll)
	names := ScopeNames(scopes)
	assert.Equal(t, []string{"systemadmin"}, names)
	assert.Len(t, names, 1)
}

func TestParseScopes_MixedGranular(t *testing.T) {
	t.Parallel()

	scopes, err := ParseScopes([]string{"node", "ban", "file"})
	require.NoError(t, err)
	assert.True(t, scopes.Has(model.ScopeNodes))
	assert.False(t, scopes.Has(model.ScopeApps))
	assert.True(t, scopes.Has(model.ScopeBans))
	assert.True(t, scopes.Has(model.ScopeFiles))

	names := ScopeNames(scopes)
	assert.ElementsMatch(t, []string{"ban", "file", "node"}, names)
	assert.NotContains(t, names, "systemadmin")
}

func TestValidateScopes_RejectsUnknownBit(t *testing.T) {
	t.Parallel()

	err := validateScopes(model.AdminScopes(1 << 14))
	assert.Error(t, err)
	assert.True(t, errors.Is(err, model.ErrInvalidScope))
}

// The catalog must stay in sync with the model bit definitions: a new scope that
// is not registered here would be ungrantable and invisible to the schema.
func TestScopeCatalog_CoversAllModelScopes(t *testing.T) {
	t.Parallel()

	defined := []model.AdminScope{
		model.ScopeNodes, model.ScopeConnections, model.ScopeSchedulers,
		model.ScopeApps, model.ScopeLicenses, model.ScopeFiles,
		model.ScopeSettings, model.ScopeTickets, model.ScopeUsers,
		model.ScopeSessions, model.ScopeBans, model.ScopeStorages,
		model.ScopeStatuses, model.ScopeDesktop,
	}
	require.Len(t, scopeCatalog, len(defined))

	seen := make(map[model.AdminScope]bool, len(scopeCatalog))
	for _, def := range scopeCatalog {
		assert.NotEmpty(t, def.name)
		assert.NotEmpty(t, def.label)
		assert.NotEmpty(t, def.desc)
		assert.True(t, AllowedScopes.Has(def.scope), "scope %q missing from AllowedScopes", def.name)
		assert.Equal(t, def.name, scopeName[def.scope])
		assert.Equal(t, def.scope, scFromName[def.name])
		seen[def.scope] = true
	}
	for _, scope := range defined {
		assert.True(t, seen[scope], "model scope %d missing from scopeCatalog", scope)
	}
}

func TestGetScopeSchema_IncludesFullDelegationAndAllGranular(t *testing.T) {
	t.Parallel()

	keys := make(map[string]bool)
	for _, meta := range GetScopeSchema() {
		keys[meta.Key] = true
	}
	assert.True(t, keys[scopeNameSysAdmin], "schema must expose the full-delegation entry")
	for _, def := range scopeCatalog {
		assert.True(t, keys[def.name], "schema missing scope %q", def.name)
	}
}

func TestScopeNames_DeterministicOrder(t *testing.T) {
	t.Parallel()

	scopes := model.AdminScopes(model.ScopeUsers | model.ScopeNodes | model.ScopeApps)
	assert.Equal(t, []string{"app", "node", "user"}, ScopeNames(scopes))
}
