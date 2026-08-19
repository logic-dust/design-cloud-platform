package session

import "errors"

var (
	// ErrInvalidSessionToken indicates the provided token is not found or invalid.
	ErrInvalidSessionToken = errors.New("invalid session token")
	// ErrExpiredSession indicates the session has surpassed its TTL.
	ErrExpiredSession = errors.New("session expired")
	// ErrPasswordChangeRequired indicates the session belongs to a user who must change password.
	ErrPasswordChangeRequired = errors.New("password change required")
	// ErrSessionNotFound indicates the requested session record does not exist.
	ErrSessionNotFound = errors.New("session not found")
	// ErrSessionAccessDenied indicates the session does not belong to the caller.
	ErrSessionAccessDenied = errors.New("session access denied")
	// ErrAdminModeRestriction indicates the user's role is not allowed under the current admin mode.
	ErrAdminModeRestriction = errors.New("role not allowed in current admin mode")
	// ErrLoginBlocked indicates the username is reserved and not permitted to login.
	ErrLoginBlocked = errors.New("login is not permitted")
)
