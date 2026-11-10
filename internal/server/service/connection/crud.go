package connection

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/push"
)

func formatConnID(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

// notifyOwnerRefresh pushes a connection refresh to the owner when the caller is
// a passive party. Self-service sync CRUD (requestUser == owner) skips push —
// the HTTP response is the operator feedback. Pass requestUser="" for async
// agent paths; pass AllUsers for admin-on-behalf so the owner is notified.
func (s *Service) notifyOwnerRefresh(ctx context.Context, requestUser, owner string, connID uint) {
	if s.publisher == nil {
		return
	}
	if owner != "" && requestUser != "" && requestUser != AllUsers && requestUser == owner {
		return
	}
	// Empty TargetID means full list refresh (push contract); never emit "0".
	targetID := ""
	if connID != 0 {
		targetID = formatConnID(connID)
	}
	msg := push.Message{
		Name: push.EventConnection,
		Payload: push.EventPayload{
			Action:   "refresh",
			TargetID: targetID,
		},
	}
	if owner == "" {
		s.publisher.Broadcast(ctx, msg)
		return
	}
	s.publisher.PublishToUser(ctx, []string{owner}, msg)
}

// ListPage returns a paginated connection list. limit must be > 0 (enforced by repo).
// Admin list view includes terminating tombstones and node-effective status.
// Host is intentionally empty here (admin inventory); detail/user list/launch paths
// project dial host via attachDialHost / ListByUserWithDialHost.
func (s *Service) ListPage(ctx context.Context, limit, offset int) ([]model.Connection, int64, error) {
	conns, total, err := s.connectionRepo.List(ctx, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	conns, err = s.withEffectiveStatuses(ctx, conns)
	return conns, total, err
}

func (s *Service) ListByUser(ctx context.Context, user string) ([]model.Connection, error) {
	var conns []model.Connection
	var err error
	if user == AllUsers {
		conns, err = s.connectionRepo.ListAll(ctx)
		if err != nil {
			return nil, err
		}
		conns = excludeTerminating(conns)
	} else {
		conns, err = s.connectionRepo.ListByUserWithDialHost(ctx, user)
		if err != nil {
			return nil, fmt.Errorf("list connections for user %s: %w", user, err)
		}
	}
	return s.withEffectiveStatuses(ctx, conns)
}

// GetByID loads the persisted connection row without dial-host projection.
// Prefer GetVisibleByIDForUser for API JSON that must include Host.
func (s *Service) GetByID(ctx context.Context, id uint) (model.Connection, error) {
	conn, err := s.connectionRepo.GetByID(ctx, id)
	if err != nil {
		return model.Connection{}, fmt.Errorf("get connection %d: %w", id, err)
	}
	return conn, nil
}

// GetByIDForUser returns the raw DB row for lifecycle operations (no Host, no effective-status).
func (s *Service) GetByIDForUser(ctx context.Context, id uint, username string) (model.Connection, error) {
	conn, err := s.GetByID(ctx, id)
	if err != nil {
		return model.Connection{}, err
	}
	if conn.Status == model.ConnectionTerminating && username != AllUsers {
		return model.Connection{}, fmt.Errorf("connection %d: %w", id, contract.ErrNotFound)
	}
	if username != AllUsers && conn.Username != username {
		return model.Connection{}, contract.NewForbiddenError("no permission to access this connection")
	}
	return conn, nil
}

// GetVisibleByIDForUser is the user/admin GET detail path with effective status overlay.
func (s *Service) GetVisibleByIDForUser(ctx context.Context, id uint, username string) (model.Connection, error) {
	conn, err := s.GetByIDForUser(ctx, id, username)
	if err != nil {
		return model.Connection{}, err
	}
	out, err := s.withEffectiveStatuses(ctx, []model.Connection{conn})
	if err != nil {
		return model.Connection{}, err
	}
	s.attachDialHost(ctx, &out[0])
	return out[0], nil
}

func (s *Service) Touch(ctx context.Context, id uint, username string) error {
	conn, err := s.GetByIDForUser(ctx, id, username)
	if err != nil {
		return err
	}

	now := time.Now()
	if !s.rememberTouch(id, now) {
		return nil
	}

	return s.connectionRepo.UpdateFields(ctx, conn.ID, map[string]any{
		"last_accessed_at": now,
	})
}

func (s *Service) rememberTouch(id uint, now time.Time) bool {
	s.touchMu.Lock()
	defer s.touchMu.Unlock()
	if s.lastTouchAt == nil {
		s.lastTouchAt = make(map[uint]time.Time)
	}
	for key, touched := range s.lastTouchAt {
		if now.Sub(touched) >= 60*time.Second {
			delete(s.lastTouchAt, key)
		}
	}
	if last, ok := s.lastTouchAt[id]; ok && now.Sub(last) < 60*time.Second {
		return false
	}
	s.lastTouchAt[id] = now
	return true
}

func (s *Service) PurgeUserConnections(ctx context.Context, username string) error {
	username = strings.TrimSpace(username)
	if username == "" || s.connectionRepo == nil {
		return nil
	}
	// Include terminating tombstones so user deletion does not leave quota/ghost rows.
	conns, err := s.connectionRepo.ListAllByUser(ctx, username)
	if err != nil {
		return err
	}
	if len(conns) > 0 {
		ids := make([]uint, 0, len(conns))
		for _, conn := range conns {
			ids = append(ids, conn.ID)
		}
		if _, err := s.Delete(ctx, AllUsers, ids...); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) dialHostForNode(ctx context.Context, nodeID string) string {
	if nodeID == "" {
		return ""
	}
	if s.nodeRepo == nil {
		return nodeID
	}
	node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return nodeID
		}
		return nodeID
	}
	return model.DialHostFor(node.DialHost, node.NodeID)
}

func (s *Service) attachDialHost(ctx context.Context, conn *model.Connection) {
	if conn == nil {
		return
	}
	conn.Host = s.dialHostForNode(ctx, conn.NodeID)
}
