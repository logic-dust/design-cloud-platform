package proxy

import (
	"sync"
	"time"

	"design-cloud-engine/internal/common"
)

const proxyStorePruneInterval = 30 * time.Second

type tunnelStore struct {
	mu           sync.RWMutex
	byOTP        map[string]uint
	byConnection map[uint]*Tunnel
	nextPruneAt  time.Time
}

func newTunnelStore() *tunnelStore {
	return &tunnelStore{
		byOTP:        make(map[string]uint),
		byConnection: make(map[uint]*Tunnel),
	}
}

func (s *tunnelStore) Create(tunnel *Tunnel) {
	if tunnel == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.maybePruneExpiredPendingLocked(now)
	_ = s.removeLockedByConnectionID(tunnel.ConnectionID)

	copyTunnel := *tunnel
	s.byConnection[copyTunnel.ConnectionID] = &copyTunnel
	s.byOTP[copyTunnel.OTP] = copyTunnel.ConnectionID
}

func (s *tunnelStore) CloseByConnectionID(connectionID uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.removeLockedByConnectionID(connectionID)
}

func (s *tunnelStore) GetByOTP(otp string) (*Tunnel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	connID, ok := s.byOTP[otp]
	if !ok {
		return nil, common.ErrNotFound
	}
	tunnel, ok := s.byConnection[connID]
	if !ok {
		delete(s.byOTP, otp)
		return nil, common.ErrNotFound
	}
	now := time.Now()
	if isExpiredPending(tunnel, now) {
		_ = s.removeLockedByConnectionID(connID)
		return nil, common.ErrNotFound
	}
	return new(*tunnel), nil
}

func (s *tunnelStore) Activate(otp string, now time.Time) (*Tunnel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	connID, ok := s.byOTP[otp]
	if !ok {
		return nil, common.ErrNotFound
	}
	tunnel, ok := s.byConnection[connID]
	if !ok {
		delete(s.byOTP, otp)
		return nil, common.ErrNotFound
	}
	if isExpiredPending(tunnel, now) {
		_ = s.removeLockedByConnectionID(connID)
		return nil, ErrProxyTunnelActivateFailed
	}
	if tunnel.Status != StatusPending {
		return nil, ErrProxyTunnelActivateFailed
	}
	tunnel.Status = StatusActive
	return new(*tunnel), nil
}

func (s *tunnelStore) GetByConnectionID(connectionID uint) (*Tunnel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tunnel, ok := s.byConnection[connectionID]
	if !ok {
		return nil, common.ErrNotFound
	}
	now := time.Now()
	if isExpiredPending(tunnel, now) {
		_ = s.removeLockedByConnectionID(connectionID)
		return nil, common.ErrNotFound
	}
	return new(*tunnel), nil
}

func (s *tunnelStore) List() []Tunnel {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]Tunnel, 0, len(s.byConnection))
	now := time.Now()
	s.maybePruneExpiredPendingLocked(now)
	for _, tunnel := range s.byConnection {
		if isExpiredPending(tunnel, now) {
			continue
		}
		copyTunnel := *tunnel
		result = append(result, copyTunnel)
	}
	return result
}

func (s *tunnelStore) CountActive() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	var count int64
	now := time.Now()
	s.maybePruneExpiredPendingLocked(now)
	for _, tunnel := range s.byConnection {
		if isExpiredPending(tunnel, now) {
			continue
		}
		if tunnel.Status == StatusPending || tunnel.Status == StatusActive {
			count++
		}
	}
	return count
}

func (s *tunnelStore) removeLockedByConnectionID(connectionID uint) error {
	tunnel, ok := s.byConnection[connectionID]
	if !ok {
		return common.ErrNotFound
	}
	delete(s.byConnection, connectionID)
	delete(s.byOTP, tunnel.OTP)
	return nil
}

func isExpiredPending(tunnel *Tunnel, now time.Time) bool {
	return tunnel != nil && tunnel.Status == StatusPending && !now.Before(tunnel.ExpiresAt)
}

func (s *tunnelStore) maybePruneExpiredPendingLocked(now time.Time) {
	if now.Before(s.nextPruneAt) {
		return
	}
	s.pruneExpiredPendingLocked(now)
	s.nextPruneAt = now.Add(proxyStorePruneInterval)
}

// pruneExpiredPendingLocked removes tunnels that are in Pending status and past their
// ExpiresAt deadline. Must be called with the write lock held.
func (s *tunnelStore) pruneExpiredPendingLocked(now time.Time) int {
	pruned := 0
	for connID, tunnel := range s.byConnection {
		if isExpiredPending(tunnel, now) {
			delete(s.byConnection, connID)
			delete(s.byOTP, tunnel.OTP)
			pruned++
		}
	}
	return pruned
}

// PruneExpiredPending removes all expired pending tunnels from the store.
// Returns the number of entries pruned.
func (s *tunnelStore) PruneExpiredPending(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	pruned := s.pruneExpiredPendingLocked(now)
	s.nextPruneAt = now.Add(proxyStorePruneInterval)
	return pruned
}
