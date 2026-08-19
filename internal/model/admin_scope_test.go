package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAdminScopes_Has(t *testing.T) {
	t.Parallel()

	var s AdminScopes
	s.Set(ScopeNodes)
	assert.True(t, s.Has(ScopeNodes))
	assert.False(t, s.Has(ScopeApps))

	// ScopeAll short-circuits every query.
	all := AdminScopes(ScopeAll)
	assert.True(t, all.Has(ScopeNodes))
	assert.True(t, all.Has(ScopeBans))
}

func TestAdminScopes_HasAny(t *testing.T) {
	t.Parallel()

	var s AdminScopes
	s.Set(ScopeFiles)
	assert.True(t, s.HasAny(ScopeFiles, ScopeApps))
	assert.False(t, s.HasAny(ScopeApps))

	all := AdminScopes(ScopeAll)
	assert.True(t, all.HasAny(ScopeNodes))
}

func TestAdminScopes_Set(t *testing.T) {
	t.Parallel()

	var s AdminScopes
	s.Set(ScopeConnections)
	s.Set(ScopeLicenses)
	assert.Equal(t, AdminScopes(ScopeConnections|ScopeLicenses), s)
}
