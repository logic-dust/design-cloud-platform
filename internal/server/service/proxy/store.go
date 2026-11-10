package proxy

import (
	"errors"
	"sync"

	"design-cloud-engine/internal/contract"
)

const (
	anchorConnection uint8 = 1
	anchorResource   uint8 = 2
)

type sessionKey struct {
	kind      uint8
	id        uint
	requester string
}

func sessionKeyFor(connectionID, resourceID uint, requester string) sessionKey {
	if resourceID != 0 {
		return sessionKey{kind: anchorResource, id: resourceID, requester: requester}
	}
	return sessionKey{kind: anchorConnection, id: connectionID, requester: requester}
}

func sessionKeyFromProxy(session *ProxyConnection) sessionKey {
	if session == nil {
		return sessionKey{}
	}
	return sessionKeyFor(session.ConnectionID, session.ResourceID, session.Requester)
}

type storedSession struct {
	session ProxyConnection
	handle  *ProxyConnectionLifecycle
}

type sessionQuota struct {
	MaxGlobal    int
	MaxPerUser   int
	ConnectionID uint
	ResourceID   uint
	Requester    string
}

func (q sessionQuota) key() sessionKey {
	return sessionKeyFor(q.ConnectionID, q.ResourceID, q.Requester)
}

type proxySessionRegistry struct {
	mu                sync.RWMutex
	byID              map[uint]*storedSession
	byAnchorRequester map[sessionKey]uint
	nextID            uint
}

func newProxySessionRegistry() *proxySessionRegistry {
	return &proxySessionRegistry{
		byID:              make(map[uint]*storedSession),
		byAnchorRequester: make(map[sessionKey]uint),
	}
}

// RegisterActive inserts an active session after the data plane is ready to bridge.
// Replaces any existing session for the same anchor (connection or resource) + requester.
func (s *proxySessionRegistry) RegisterActive(quota sessionQuota, session *ProxyConnection, handle *ProxyConnectionLifecycle) (*storedSession, *ProxyConnectionLifecycle, error) {
	if session == nil {
		return nil, nil, errors.New("proxy session is nil")
	}
	if handle == nil {
		return nil, nil, errors.New("proxy lifecycle is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	replacedStored := s.removeReplacementTargetLocked(quota.key())

	activeCount, userCount := s.countQuotaLocked(quota.Requester)
	if quota.MaxGlobal > 0 && activeCount >= quota.MaxGlobal {
		if replacedStored != nil {
			s.restoreReplacementTargetLocked(replacedStored)
		}
		return nil, nil, ErrProxyMaxSessionsReached
	}
	if quota.MaxPerUser > 0 && userCount >= quota.MaxPerUser {
		if replacedStored != nil {
			s.restoreReplacementTargetLocked(replacedStored)
		}
		return nil, nil, ErrProxyMaxSessionsPerUserReached
	}

	s.nextID++
	copySession := *session
	copySession.ID = s.nextID
	copySession.Status = StatusActive
	stored := &storedSession{session: copySession, handle: handle}
	s.byID[copySession.ID] = stored
	s.byAnchorRequester[sessionKeyFromProxy(&copySession)] = copySession.ID

	if replacedStored != nil {
		return stored, replacedStored.handle, nil
	}
	return stored, nil, nil
}

// CheckQuota reports whether a new active session would exceed limits on this node.
// Replacing the same anchor frees one slot before counting.
func (s *proxySessionRegistry) CheckQuota(quota sessionQuota) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	replacing := s.byAnchorRequester[quota.key()] != 0
	activeCount, userCount := s.countQuotaLocked(quota.Requester)
	if replacing {
		activeCount--
		if userCount > 0 {
			userCount--
		}
	}
	if quota.MaxGlobal > 0 && activeCount >= quota.MaxGlobal {
		return ErrProxyMaxSessionsReached
	}
	if quota.MaxPerUser > 0 && userCount >= quota.MaxPerUser {
		return ErrProxyMaxSessionsPerUserReached
	}
	return nil
}

func (s *proxySessionRegistry) restoreReplacementTargetLocked(stored *storedSession) {
	if stored == nil {
		return
	}
	s.byID[stored.session.ID] = stored
	s.byAnchorRequester[sessionKeyFromProxy(&stored.session)] = stored.session.ID
}

func (s *proxySessionRegistry) removeReplacementTargetLocked(key sessionKey) *storedSession {
	id := s.byAnchorRequester[key]
	if id == 0 {
		return nil
	}
	return s.removeByIDLocked(id)
}

func (s *proxySessionRegistry) removeByIDLocked(id uint) *storedSession {
	if id == 0 {
		return nil
	}
	stored, ok := s.byID[id]
	if !ok {
		return nil
	}
	delete(s.byID, id)
	delete(s.byAnchorRequester, sessionKeyFromProxy(&stored.session))
	return stored
}

func (s *proxySessionRegistry) countQuotaLocked(requester string) (global int, perUser int) {
	for _, stored := range s.byID {
		global++
		if stored.session.Requester == requester {
			perUser++
		}
	}
	return global, perUser
}

func (s *proxySessionRegistry) GetByConnectionIDAndRequester(connectionID uint, requester string) (*ProxyConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.byAnchorRequester[sessionKeyFor(connectionID, 0, requester)]
	stored, ok := s.byID[id]
	if !ok {
		return nil, contract.ErrNotFound
	}
	return cloneSession(&stored.session), nil
}

// RemoveByID removes exactly one session by identity. Safe for bridge teardown:
// a replaced session's deferred close will not remove a newer session that reused
// the same anchor key.
func (s *proxySessionRegistry) RemoveByID(id uint) (*ProxyConnectionLifecycle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored := s.removeByIDLocked(id)
	if stored == nil {
		return nil, contract.ErrNotFound
	}
	return stored.handle, nil
}

func (s *proxySessionRegistry) RemoveAllByConnectionID(connectionID uint) []*ProxyConnectionLifecycle {
	s.mu.Lock()
	defer s.mu.Unlock()

	var handles []*ProxyConnectionLifecycle
	for id, stored := range s.byID {
		if stored.session.ConnectionID != connectionID || stored.session.ResourceID != 0 {
			continue
		}
		if removed := s.removeByIDLocked(id); removed != nil && removed.handle != nil {
			handles = append(handles, removed.handle)
		}
	}
	if len(handles) == 0 {
		return nil
	}
	return handles
}

func (s *proxySessionRegistry) RemoveAllByRequester(requester string) []*ProxyConnectionLifecycle {
	if requester == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var handles []*ProxyConnectionLifecycle
	for id, stored := range s.byID {
		if stored.session.Requester != requester {
			continue
		}
		if removed := s.removeByIDLocked(id); removed != nil && removed.handle != nil {
			handles = append(handles, removed.handle)
		}
	}
	return handles
}

func (s *proxySessionRegistry) RemoveAll() []*ProxyConnectionLifecycle {
	s.mu.Lock()
	defer s.mu.Unlock()

	var handles []*ProxyConnectionLifecycle
	for id := range s.byID {
		if removed := s.removeByIDLocked(id); removed != nil && removed.handle != nil {
			handles = append(handles, removed.handle)
		}
	}
	return handles
}

func (s *proxySessionRegistry) List() []ProxyConnection {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]ProxyConnection, 0, len(s.byID))
	for _, stored := range s.byID {
		result = append(result, *cloneSession(&stored.session))
	}
	return result
}

func cloneSession(session *ProxyConnection) *ProxyConnection {
	if session == nil {
		return nil
	}
	copySession := *session
	return &copySession
}
