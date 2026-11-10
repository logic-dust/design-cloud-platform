package push

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"sync"
	"sync/atomic"

	"design-cloud-engine/internal/model"
)

const (
	clientBufferSize = 32
	// maxSSEClients caps concurrent local SSE streams to bound hub memory.
	maxSSEClients = 4096
)

// ErrTooManyClients is returned when Register would exceed maxSSEClients.
var ErrTooManyClients = errors.New("too many SSE clients")

// Client represents a single connected SSE stream. Messages are delivered via
// a buffered channel with backpressure: when the buffer is full, the message
// is dropped and Dropped is incremented (slow consumers must not block others).
type Client struct {
	UserID  string
	Role    model.Role
	Ch      chan Message
	dropped atomic.Int64
}

// Dropped returns the number of messages dropped for this client (slow consumer).
func (c *Client) Dropped() int64 {
	return c.dropped.Load()
}

// ClusterForwarder is called when a push message is published locally and needs
// to be forwarded to other control nodes in the cluster.
type ClusterForwarder func(ctx context.Context, targetType string, userIDs []string, role model.Role, msg Message)

// Hub routes messages to connected clients by user ID and role.
type Hub struct {
	mu          sync.RWMutex
	users       map[string]map[*Client]struct{}
	roles       map[model.Role]map[*Client]struct{}
	clientCount int
	next        atomic.Uint64
	forwarder   ClusterForwarder
}

// HubOption customizes a Hub during construction.
type HubOption func(*Hub)

// WithClusterForwarder installs the cross-node forwarding callback before the
// hub is handed to any service. The production wiring uses this option so the
// hub's dependencies are fixed at construction.
func WithClusterForwarder(f ClusterForwarder) HubOption {
	return func(h *Hub) {
		if f != nil {
			h.forwarder = f
		}
	}
}

// NewHub creates an empty Hub.
func NewHub(opts ...HubOption) *Hub {
	h := &Hub{
		users: make(map[string]map[*Client]struct{}),
		roles: make(map[model.Role]map[*Client]struct{}),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Register adds a client (one SSE connection) to the Hub and returns it.
// The caller must defer Unregister(client). Returns ErrTooManyClients when full.
func (h *Hub) Register(userID string, role model.Role) (*Client, error) {
	c := &Client{UserID: userID, Role: role, Ch: make(chan Message, clientBufferSize)}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clientCount >= maxSSEClients {
		return nil, ErrTooManyClients
	}
	if h.users[userID] == nil {
		h.users[userID] = make(map[*Client]struct{})
	}
	h.users[userID][c] = struct{}{}
	if h.roles[role] == nil {
		h.roles[role] = make(map[*Client]struct{})
	}
	h.roles[role][c] = struct{}{}
	h.clientCount++
	return c, nil
}

// Unregister removes a client from the Hub.
func (h *Hub) Unregister(c *Client) {
	if c == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	removed := false
	if set := h.users[c.UserID]; set != nil {
		if _, ok := set[c]; ok {
			delete(set, c)
			removed = true
		}
		if len(set) == 0 {
			delete(h.users, c.UserID)
		}
	}
	if set := h.roles[c.Role]; set != nil {
		delete(set, c)
		if len(set) == 0 {
			delete(h.roles, c.Role)
		}
	}
	if removed && h.clientCount > 0 {
		h.clientCount--
	}
}

// PublishToUser delivers msg to all local connections of the given users
// and forwards to peer control nodes if configured.
// key 语义：username（与 Register 的 userID 参数一致，业务唯一标识）。
func (h *Hub) PublishToUser(ctx context.Context, userIDs []string, msg Message) {
	h.DeliverToUser(userIDs, msg)
	f := h.forwarder
	if f != nil {
		f(ctx, "user", userIDs, 0, msg)
	}
}

// DeliverToUser delivers msg to local connections of userIDs without cross-node forwarding.
func (h *Hub) DeliverToUser(userIDs []string, msg Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, id := range userIDs {
		for c := range h.users[id] {
			h.deliver(c, msg)
		}
	}
}

// PublishToRole delivers msg to all local connections with the given role
// and forwards to peer control nodes if configured.
// RoleSuperAdmin is always included (super admins observe every role).
func (h *Hub) PublishToRole(ctx context.Context, role model.Role, msg Message) {
	h.DeliverToRole(role, msg)
	f := h.forwarder
	if f != nil {
		f(ctx, "role", nil, role, msg)
	}
}

// DeliverToRole delivers msg to local connections with the given role without cross-node forwarding.
func (h *Hub) DeliverToRole(role model.Role, msg Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	seen := make(map[*Client]struct{})
	for _, r := range []model.Role{role, model.RoleSuperAdmin} {
		for c := range h.roles[r] {
			if _, ok := seen[c]; ok {
				continue
			}
			seen[c] = struct{}{}
			h.deliver(c, msg)
		}
	}
}

// Broadcast delivers msg to every connected local client
// and forwards to peer control nodes if configured.
func (h *Hub) Broadcast(ctx context.Context, msg Message) {
	h.DeliverBroadcast(msg)
	f := h.forwarder
	if f != nil {
		f(ctx, "broadcast", nil, 0, msg)
	}
}

// DeliverBroadcast delivers msg to every connected local client without cross-node forwarding.
func (h *Hub) DeliverBroadcast(msg Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, clients := range h.users {
		for c := range clients {
			select {
			case c.Ch <- msg:
			default:
				c.dropped.Add(1)
			}
		}
	}
}

// DeliverRemote delivers an event received from a remote control node to local clients.
func (h *Hub) DeliverRemote(targetType string, userIDs []string, role model.Role, msg Message) {
	switch targetType {
	case "user":
		h.DeliverToUser(userIDs, msg)
	case "role":
		h.DeliverToRole(role, msg)
	case "broadcast":
		h.DeliverBroadcast(msg)
	}
}

// DeliverRemoteEvent satisfies contract.ClusterEventPublisher.
func (h *Hub) DeliverRemoteEvent(_ context.Context, targetType string, userIDs []string, role int, eventName string, payloadJSON string) {
	var payload any
	if payloadJSON != "" {
		var ep EventPayload
		if err := json.Unmarshal([]byte(payloadJSON), &ep); err == nil {
			payload = ep
		} else {
			payload = payloadJSON
		}
	}
	msg := Message{
		Name:    EventName(eventName),
		Payload: payload,
	}
	h.DeliverRemote(targetType, userIDs, model.Role(role), msg)
}

// PublishNodeEvent satisfies contract.ClusterEventPublisher.
func (h *Hub) PublishNodeEvent(ctx context.Context, role int, nodeID, action, title, message, level string) {
	var notify *Notification
	if title != "" || message != "" {
		notify = &Notification{
			Title:   title,
			Message: message,
			Level:   level,
		}
	}
	h.PublishToRole(ctx, model.Role(role), Message{
		Name: EventNode,
		Payload: EventPayload{
			Action:   action,
			TargetID: nodeID,
			Notify:   notify,
		},
	})
}

// NextID returns a globally monotonic event ID for the SSE id: field.
func (h *Hub) NextID() uint64 {
	return h.next.Add(1)
}

// ClientCount returns the number of connected clients (tests / monitoring).
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.clientCount
}

// DroppedTotal returns the total dropped messages across all clients.
func (h *Hub) DroppedTotal() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var total int64
	for _, set := range h.users {
		for c := range set {
			total += c.Dropped()
		}
	}
	return total
}

// deliver is non-blocking with backpressure drop. Must hold h.mu (read) when called.
func (h *Hub) deliver(c *Client, msg Message) {
	select {
	case c.Ch <- msg:
	default:
		c.dropped.Add(1)
	}
}
