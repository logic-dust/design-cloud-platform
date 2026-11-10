// Tests for composition-root wiring in server.go (settings change → Agent / Portal push).
package server

import (
	"context"
	"testing"
	"time"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/push"

	"github.com/stretchr/testify/require"
)

type recordingPublisher struct {
	ch chan push.Message
}

func (p *recordingPublisher) PublishToUser(context.Context, []string, push.Message) {}
func (p *recordingPublisher) PublishToRole(context.Context, model.Role, push.Message) {
}
func (p *recordingPublisher) Broadcast(_ context.Context, msg push.Message) {
	p.ch <- msg
}

func TestSubscribeAgentSettingsPush_OnlyAgentRelatedDomains(t *testing.T) {
	svc := newTestSettingsService(t)
	pushes := make(chan struct{}, 8)
	subscribeAgentSettingsPush(svc, func(context.Context) error {
		pushes <- struct{}{}
		return nil
	})

	ctx := context.Background()
	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"sessionTTL":7200}`)))
	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainDesktop, []byte(`{"linuxEnabled":false}`)))
	assertNoAgentPush(t, pushes, 200*time.Millisecond)

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainAgent, []byte(`{"execBinPath":"/usr/bin/dc-exec"}`)))
	waitAgentPush(t, pushes, 2*time.Second)

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainStorage, []byte(`{"probeInterval":3600,"storages":[]}`)))
	waitAgentPush(t, pushes, 2*time.Second)

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainLicense, []byte(`{"lmutilPath":"/opt/lmutil","statusCacheTTL":120,"featuresCacheTTL":300}`)))
	waitAgentPush(t, pushes, 2*time.Second)

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainScheduler, []byte(`{"type":"lsf","jobCacheTTL":60}`)))
	waitAgentPush(t, pushes, 2*time.Second)
}

func TestSubscribeDesktopSettingsRefresh_BroadcastsOnlyDesktopDomain(t *testing.T) {
	svc := newTestSettingsService(t)
	pub := &recordingPublisher{ch: make(chan push.Message, 4)}
	subscribeDesktopSettingsRefresh(svc, pub)

	ctx := context.Background()
	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainAuth, []byte(`{"sessionTTL":7200}`)))
	select {
	case msg := <-pub.ch:
		t.Fatalf("auth update should not broadcast desktop refresh, got %+v", msg)
	case <-time.After(200 * time.Millisecond):
	}

	require.NoError(t, svc.UpdateByDomain(ctx, schema.DomainDesktop, []byte(`{"linuxEnabled":false}`)))
	select {
	case msg := <-pub.ch:
		if msg.Name != push.EventSystem {
			t.Fatalf("event = %q, want %q", msg.Name, push.EventSystem)
		}
		payload, ok := msg.Payload.(push.EventPayload)
		if !ok {
			t.Fatalf("payload type %T", msg.Payload)
		}
		if payload.Action != "refresh" {
			t.Fatalf("action = %q, want refresh", payload.Action)
		}
		if payload.Notify != nil {
			t.Fatal("notify should be omitted")
		}
		extra, _ := payload.Extra.(map[string]string)
		if extra["domain"] != schema.DomainDesktop.String() {
			t.Fatalf("extra = %#v, want domain=desktop", payload.Extra)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for desktop settings refresh")
	}

	select {
	case msg := <-pub.ch:
		t.Fatalf("unexpected extra broadcast: %+v", msg)
	case <-time.After(200 * time.Millisecond):
	}
}

func waitAgentPush(t *testing.T, ch <-chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatal("timed out waiting for agent settings push")
	}
}

func assertNoAgentPush(t *testing.T, ch <-chan struct{}, window time.Duration) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("unexpected agent settings push")
	case <-time.After(window):
	}
}
