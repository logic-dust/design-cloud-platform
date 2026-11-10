package session

import (
	"context"
	"fmt"
	"log/slog"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/server/cache"

	"gorm.io/gorm"
)

// Evictor owns the cross-domain session effects: dropping the local token cache,
// deleting session rows and closing the proxy connections of the revoked user.
//
// It deliberately depends only on storage/cache/proxy ports and never on
// *user.Service. That is the seam that breaks the user↔session construction
// cycle: user.Service needs session eviction for its own write flows, while
// session.Service needs *user.Service. Both now consume the same Evictor
// instance instead of pointing at each other.
type Evictor struct {
	cache        *sessionInfoCache
	sessionRepo  *db.SessionRepo
	invalidator  *cache.Invalidator
	logger       *slog.Logger
	proxyRevoker func(requester string)
}

// NewEvictor builds the shared session-effect component. proxyRevoker is
// optional — nil means eviction will not tear down proxy connections, which is
// acceptable for unit tests or bootstrapping without a proxy service.
func NewEvictor(logger *slog.Logger, sessionRepo *db.SessionRepo, invalidator *cache.Invalidator, proxyRevoker ProxyConnectionRevoker) (*Evictor, error) {
	if logger == nil {
		return nil, fmt.Errorf("session: logger is required")
	}
	if sessionRepo == nil {
		return nil, fmt.Errorf("session: session repo is required")
	}
	e := &Evictor{
		cache:       newSessionInfoCache(),
		sessionRepo: sessionRepo,
		invalidator: invalidator,
		logger:      logger,
		proxyRevoker: func(requester string) {
			if proxyRevoker != nil {
				proxyRevoker(requester)
			}
		},
	}
	if invalidator != nil {
		invalidator.Register(db.CacheVersionTopicSessions, func() {
			e.cache.clear()
		})
	}
	return e, nil
}

// InvalidateByUsername drops the local cache entries of a user without touching
// the session rows. Used where the persisted sessions must survive (profile
// update) but stale in-memory copies have to go.
func (e *Evictor) InvalidateByUsername(username string) {
	if e == nil || username == "" {
		return
	}
	e.cache.invalidateByUsername(username)
}

// Delete removes every persisted session of the user, drops the cache entries
// and closes the user's proxy connections. The returned error keeps the caller's
// existing failure semantics: DB failures surface, proxy cleanup is best-effort.
func (e *Evictor) Delete(ctx context.Context, username string) error {
	if e == nil {
		return nil
	}
	if err := e.sessionRepo.DeleteByUsername(ctx, username, func(tx *gorm.DB) error {
		if e.invalidator == nil {
			return nil
		}
		return e.invalidator.BumpTx(ctx, tx, db.CacheVersionTopicSessions)
	}); err != nil {
		return fmt.Errorf("delete sessions for user %s: %w", username, err)
	}
	e.InvalidateByUsername(username)
	e.revokeProxyConnections(username)
	return nil
}

func (e *Evictor) revokeProxyConnections(requester string) {
	if e == nil || e.proxyRevoker == nil || requester == "" {
		return
	}
	e.proxyRevoker(requester)
}

func (e *Evictor) revokeProxyIfNoSessions(ctx context.Context, userID uint, username string) {
	if e == nil || username == "" {
		return
	}
	remaining, err := e.sessionRepo.CountByUser(ctx, userID)
	if err != nil {
		e.logger.Warn("count sessions after delete failed", "user", username, "error", err)
		e.revokeProxyConnections(username)
		return
	}
	if remaining == 0 {
		e.revokeProxyConnections(username)
	}
}
