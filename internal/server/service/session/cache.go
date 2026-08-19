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
)

// sessionInfoCache is an in-memory token→model.Session cache that eliminates per-request DB lookups.
//
// Design:
//   - byToken is the primary index (token → *model.Session), used on every authenticated request.
//   - byUsername is a reverse index (username → []token) used to bulk-evict a user's sessions
//     when their role changes.
//   - Same-server eviction (Delete/DeleteByID) is synchronous: the caller holds the write lock
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
}

// evictToken removes a single token from both indexes synchronously.
func (c *sessionInfoCache) evictToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeToken(token)
}

// evictByUsername removes all cached sessions for a user (e.g. after role change).
func (c *sessionInfoCache) evictByUsername(username string) {
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

// evictExpired removes all entries whose ExpiresAt <= now from both indexes.
// It is safe to call concurrently; it acquires the write lock internally.
// Returns the number of entries removed.
func (c *sessionInfoCache) evictExpired(now time.Time) int {
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
