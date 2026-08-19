package model

import (
	"errors"
)

// AdminScope is a single bit representing one delegatable admin capability.
// Each scope maps to one top-level admin API route group:
type AdminScope int64

const (
	ScopeNodes       AdminScope = 1 << 0  // /admin/system/nodes
	ScopeConnections AdminScope = 1 << 1  // /admin/system/connections
	ScopeSchedulers  AdminScope = 1 << 2  // /admin/system/scheduler
	ScopeApps        AdminScope = 1 << 3  // /admin/system/apps
	ScopeLicenses    AdminScope = 1 << 4  // /admin/system/licenses
	ScopeFiles       AdminScope = 1 << 5  // /admin/security/files
	ScopeSettings    AdminScope = 1 << 6  // /admin/system/settings
	ScopeTickets     AdminScope = 1 << 7  // /admin/system/tickets
	ScopeUsers       AdminScope = 1 << 8  // /admin/system/users
	ScopeSessions    AdminScope = 1 << 9  // /admin/security/sessions
	ScopeBans        AdminScope = 1 << 10 // /admin/security/bans
	ScopeStorages    AdminScope = 1 << 11 // /admin/system/storage
	ScopeStatuses    AdminScope = 1 << 12 // /admin/system/status

	// ScopeAll grants every admin capability.
	ScopeAll = ^AdminScope(0)
)

// AdminScopes is a bitmask of AdminScope values with helper methods.
type AdminScopes int64

func (s *AdminScopes) Has(scope AdminScope) bool {
	if *s == AdminScopes(ScopeAll) {
		return true
	}
	return int64(*s)&int64(scope) != 0
}

func (s *AdminScopes) HasAny(scopes ...AdminScope) bool {
	if *s == AdminScopes(ScopeAll) {
		return true
	}
	for _, scope := range scopes {
		if int64(*s)&int64(scope) != 0 {
			return true
		}
	}
	return false
}

func (s *AdminScopes) Set(scope AdminScope) {
	*s = AdminScopes(int64(*s) | int64(scope))
}

// ErrInvalidScope is returned by scope validation/parse helpers.
var ErrInvalidScope = errors.New("invalid AdminScope")
