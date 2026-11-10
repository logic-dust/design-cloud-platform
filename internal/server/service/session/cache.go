package session

import (
	"sync"
	"time"

	"design-cloud-engine/internal/model"

	"golang.org/x/time/rate"
)

const (
	defaultSessionRateLimit = 50  // req/s per session
	defaultSessionRateBurst = 150 // burst token capacity per session
	// maxSessionCacheEntries bounds in-memory token maps between cleanup ticks.
	maxSessionCacheEntries = 100_000
)

// sessionInfoCache is an in-memory token→model.Session cache that eliminates per-request DB lookups.
//
// Design:
//   - byToken is the primary index (token → *model.Session), used on every authenticated request.
//   - byUsername is a reverse index (username → []token) used to bulk-invalidate a user's sessions
//     when their role changes.
//   - Same-server invalidation (Delete/DeleteByID) is synchronous: the caller holds the write lock
//     and removes the entry immediately, so there is no delay on the current server.
type sessionInfoCache struct {
	mu         sync.RWMutex
	byToken    map[string]*model.Session
	byUsername map[string]map[string]struct{} // username → set of tokens (O(1) lookup/insert/delete)
}

func newSessionInfoCache() *sessionInfoCache {
	return &sessionInfoCache{
		byToken:    make(map[string]*model.Session),
		byUsername: make(map[string]map[string]struct{}),
	}
}

// get returns the cached model.Session for the given token, or (nil, false) on a miss.
func (c *sessionInfoCache) get(token string) (*model.Session, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	info, ok := c.byToken[token]
	return info, ok
}

// put stores info in both indexes and initializes the rate limiter if needed.
func (c *sessionInfoCache) put(info *model.Session) {
	if info == nil || info.Token == "" {
		return
	}
	// Initialize the per-session rate limiter lazily.
	if info.RateLimiter == nil {
		info.RateLimiter = rate.NewLimiter(rate.Limit(defaultSessionRateLimit), defaultSessionRateBurst)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byToken[info.Token] = info
	// maintain reverse index (O(1) set insert)
	if c.byUsername[info.Username] == nil {
		c.byUsername[info.Username] = make(map[string]struct{})
	}
	c.byUsername[info.Username][info.Token] = struct{}{}
	c.enforceMaxEntriesLocked(time.Now())
}

// enforceMaxEntriesLocked drops expired entries first, then arbitrary extras
// until len(byToken) <= maxSessionCacheEntries. Caller must hold write lock.
func (c *sessionInfoCache) enforceMaxEntriesLocked(now time.Time) {
	if len(c.byToken) <= maxSessionCacheEntries {
		return
	}
	for token, info := range c.byToken {
		if info == nil || !now.Before(info.ExpiresAt) {
			c.removeToken(token)
		}
	}
	for token := range c.byToken {
		if len(c.byToken) <= maxSessionCacheEntries {
			return
		}
		c.removeToken(token)
	}
}

// invalidateToken removes a single token from both indexes synchronously.
func (c *sessionInfoCache) invalidateToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeToken(token)
}

// invalidateByUsername removes all cached sessions for a user (e.g. after role change).
func (c *sessionInfoCache) invalidateByUsername(username string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for token := range c.byUsername[username] {
		delete(c.byToken, token)
	}
	delete(c.byUsername, username)
}

// clear removes all entries from the cache (used for multi-node cache invalidation).
func (c *sessionInfoCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.byToken)
	clear(c.byUsername)
}

// invalidateExpired removes all entries whose ExpiresAt <= now from both indexes.
// It is safe to call concurrently; it acquires the write lock internally.
// Returns the number of entries removed.
func (c *sessionInfoCache) invalidateExpired(now time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	deleted := 0
	for token, info := range c.byToken {
		if info == nil || !now.Before(info.ExpiresAt) {
			c.removeToken(token)
			deleted++
		}
	}
	return deleted
}

// removeToken must be called with c.mu held (write).
func (c *sessionInfoCache) removeToken(token string) {
	info, ok := c.byToken[token]
	if !ok {
		return
	}
	delete(c.byToken, token)
	if info == nil {
		return
	}
	tokens := c.byUsername[info.Username]
	delete(tokens, token)
	if len(tokens) == 0 {
		delete(c.byUsername, info.Username)
	}
}
