package push

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"design-cloud-engine/internal/model"
)

func TestHubRegisterUnregister(t *testing.T) {
	h := NewHub()
	c, err := h.Register("user-1", model.RoleNormal)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := h.ClientCount(); got != 1 {
		t.Fatalf("ClientCount = %d, want 1", got)
	}
	h.Unregister(c)
	if got := h.ClientCount(); got != 0 {
		t.Fatalf("ClientCount after unregister = %d, want 0", got)
	}
}

func TestHubPublishToUser(t *testing.T) {
	h := NewHub()
	c, err := h.Register("user-1", model.RoleNormal)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer h.Unregister(c)
	c2, err := h.Register("user-2", model.RoleNormal)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer h.Unregister(c2)

	msg := Message{Name: EventTicket, Payload: EventPayload{Action: "refresh"}}
	h.PublishToUser(context.Background(), []string{"user-1"}, msg)

	select {
	case got := <-c.Ch:
		if got.Name != EventTicket {
			t.Fatalf("event name = %q, want %q", got.Name, EventTicket)
		}
	default:
		t.Fatal("user-1 did not receive message")
	}
}

func TestHubPublishToRoleIncludesSuperAdmin(t *testing.T) {
	h := NewHub()
	normal, err := h.Register("user-1", model.RoleNormal)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer h.Unregister(normal)
	sa, err := h.Register("sa", model.RoleSuperAdmin)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer h.Unregister(sa)

	msg := Message{Name: EventSystem, Payload: EventPayload{Action: "notify"}}
	h.PublishToRole(context.Background(), model.RoleNormal, msg)

	for name, c := range map[string]*Client{"normal": normal, "superadmin": sa} {
		select {
		case <-c.Ch:
			// received: expected
		default:
			t.Fatalf("%s did not receive role message", name)
		}
	}
}

func TestHubPublishToRoleExcludesOtherRoles(t *testing.T) {
	h := NewHub()
	c, err := h.Register("sa-only", model.RoleSystemAdmin)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer h.Unregister(c)

	h.PublishToRole(context.Background(), model.RoleAuditAdmin, Message{Name: EventJob})

	select {
	case got := <-c.Ch:
		t.Fatalf("unexpected message to other role: %+v", got)
	default:
		// correct: no delivery
	}
}

func TestHubBroadcast(t *testing.T) {
	h := NewHub()
	c1, err := h.Register("u1", model.RoleNormal)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer h.Unregister(c1)
	c2, err := h.Register("u2", model.RoleSecurityAdmin)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer h.Unregister(c2)

	h.Broadcast(context.Background(), Message{Name: EventSystem})

	for name, c := range map[string]*Client{"u1": c1, "u2": c2} {
		select {
		case <-c.Ch:
		default:
			t.Fatalf("%s did not receive broadcast", name)
		}
	}
}

func TestHubBackpressureDrops(t *testing.T) {
	h := NewHub()
	c, err := h.Register("user-1", model.RoleNormal)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer h.Unregister(c)

	// Fill the buffer beyond capacity with non-blocking sends.
	msg := Message{Name: EventJob}
	for range clientBufferSize * 3 {
		h.PublishToUser(context.Background(), []string{"user-1"}, msg)
	}
	if c.Dropped() == 0 {
		t.Fatal("expected dropped count > 0 on buffer overflow")
	}
	// Exactly one message should be receivable (rest dropped or pending).
	if got := len(c.Ch); got != clientBufferSize {
		t.Fatalf("buffered = %d, want %d", got, clientBufferSize)
	}
}

func TestHubNextIDMonotonic(t *testing.T) {
	h := NewHub()
	prev := h.NextID()
	for range 100 {
		cur := h.NextID()
		if cur <= prev {
			t.Fatalf("NextID not monotonic: %d after %d", cur, prev)
		}
		prev = cur
	}
}

func TestHubConcurrentPublish(t *testing.T) {
	h := NewHub()
	const clients = 8
	const publishers = 4
	const perPublisher = 200

	cs := make([]*Client, 0, clients)
	for i := range clients {
		c, err := h.Register(fmt.Sprintf("user-%d", i), model.RoleNormal)
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		t.Cleanup(func() { h.Unregister(c) })
		cs = append(cs, c)
	}

	var wg sync.WaitGroup
	for range publishers {
		wg.Go(func() {
			for range perPublisher {
				h.Broadcast(context.Background(), Message{Name: EventJob})
			}
		})
	}
	wg.Wait()

	for _, c := range cs {
		if len(c.Ch) == 0 {
			t.Fatalf("client %s received nothing under concurrency", c.UserID)
		}
	}
	if h.ClientCount() != clients {
		t.Fatalf("ClientCount = %d, want %d", h.ClientCount(), clients)
	}
}

func TestHubCrossNodeForwarder(t *testing.T) {
	var forwardedTarget string
	var forwardedUsers []string
	var forwardedRole model.Role
	var forwardedMsg Message

	h := NewHub(WithClusterForwarder(func(_ context.Context, targetType string, userIDs []string, role model.Role, msg Message) {
		forwardedTarget = targetType
		forwardedUsers = userIDs
		forwardedRole = role
		forwardedMsg = msg
	}))
	c, err := h.Register("user-1", model.RoleNormal)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer h.Unregister(c)

	// 1. PublishToUser triggers local delivery AND cluster forwarder
	msg := Message{Name: EventTicket, Payload: EventPayload{Action: "refresh"}}
	h.PublishToUser(context.Background(), []string{"user-1"}, msg)

	select {
	case <-c.Ch:
	default:
		t.Fatal("local client did not receive message")
	}

	if forwardedTarget != "user" || len(forwardedUsers) != 1 || forwardedUsers[0] != "user-1" || forwardedMsg.Name != EventTicket {
		t.Fatalf("cluster forwarder got unexpected arguments: target=%s, users=%v, msg=%+v", forwardedTarget, forwardedUsers, forwardedMsg)
	}

	// 2. PublishToRole triggers cluster forwarder with role
	h.PublishToRole(context.Background(), model.RoleSystemAdmin, Message{Name: EventSystem})
	if forwardedTarget != "role" || forwardedRole != model.RoleSystemAdmin {
		t.Fatalf("cluster forwarder role expected: target=role, role=%v, got: target=%s, role=%v", model.RoleSystemAdmin, forwardedTarget, forwardedRole)
	}

	// 3. DeliverRemote delivers locally WITHOUT triggering forwarder (loop prevention)
	forwardedTarget = ""
	h.DeliverRemote("user", []string{"user-1"}, 0, Message{Name: EventSystem})

	select {
	case got := <-c.Ch:
		if got.Name != EventSystem {
			t.Fatalf("expected EventSystem, got %s", got.Name)
		}
	default:
		t.Fatal("local client did not receive remote delivered message")
	}

	if forwardedTarget != "" {
		t.Fatalf("DeliverRemote must NOT re-trigger cluster forwarder, got target: %s", forwardedTarget)
	}
}

func TestHubDeliverRemoteEventJSON(t *testing.T) {
	h := NewHub()
	c, err := h.Register("user-admin", model.RoleSystemAdmin)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer h.Unregister(c)

	payloadJSON := `{"action":"notify","targetId":"node-1","notify":{"title":"Node Online","message":"Node 1 is ready","level":"info"}}`
	h.DeliverRemoteEvent(context.Background(), "role", nil, int(model.RoleSystemAdmin), string(EventNode), payloadJSON)

	select {
	case got := <-c.Ch:
		if got.Name != EventNode {
			t.Fatalf("event name = %s, want %s", got.Name, EventNode)
		}
		ep, ok := got.Payload.(EventPayload)
		if !ok {
			t.Fatalf("payload type = %T, want EventPayload", got.Payload)
		}
		if ep.TargetID != "node-1" || ep.Notify == nil || ep.Notify.Title != "Node Online" {
			t.Fatalf("unexpected payload content: %+v", ep)
		}
	default:
		t.Fatal("client did not receive remote event")
	}
}
