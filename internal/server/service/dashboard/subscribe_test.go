package dashboard

import (
	"context"
	"testing"
	"time"

	"design-cloud-engine/internal/server/service/settings/schema"
)

// stubSettings 实现 settingsProvider，供无需真实 settings 的测试使用。
type stubSettings struct{}

func (stubSettings) GetAgentSettings(context.Context) (schema.AgentSettings, error) {
	return schema.AgentSettings{}, nil
}
func (stubSettings) GetStorageSettings(context.Context) (schema.StorageSettings, error) {
	return schema.StorageSettings{}, nil
}
func (stubSettings) GetPlatformSettings(context.Context) (schema.PlatformSettings, error) {
	return schema.PlatformSettings{}, nil
}
func (stubSettings) GetDashboardSettings(context.Context) (schema.DashboardSettings, error) {
	return schema.DashboardSettings{}, nil
}
func (stubSettings) GetAlertSettings(context.Context) (schema.AlertSettings, error) {
	return schema.AlertSettings{}, nil
}

func newTestService() *Service {
	return NewService(nil, nil, nil, stubSettings{}, nil)
}

// TestSubscribeNotify verifies subscriber receives notification and unsubscribe works.
func TestSubscribeNotify(t *testing.T) {
	s := newTestService()

	notifyCh1, unsub1 := s.Subscribe()
	notifyCh2, unsub2 := s.Subscribe()
	defer unsub2()

	// 手动广播，两个订阅者都应收到
	s.notifySubscribers()

	select {
	case <-notifyCh1:
	default:
		t.Error("subscriber 1 did not receive notification")
	}
	select {
	case <-notifyCh2:
	default:
		t.Error("subscriber 2 did not receive notification")
	}

	// 退订后不再收到
	unsub1()
	s.notifySubscribers()
	select {
	case <-notifyCh1:
		t.Error("unsubscribed subscriber still receives notifications")
	default:
	}
}

// TestSubscribeStopLoop verifies loop stops when last subscriber leaves.
// 用一个可观察的信号验证：循环停止后不再刷新（通过 sampledAt 不再变化间接验证成本高，
// 这里验证退订后 refreshCancel 被清空、再次订阅能重新启动）。
func TestSubscribeStopLoop(t *testing.T) {
	s := newTestService()

	_, unsub := s.Subscribe()
	if s.refreshCancel == nil {
		t.Fatal("refresh loop not started after first subscribe")
	}

	unsub()
	// 需要等退订后的锁释放
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.subMu.Lock()
		stopped := s.refreshCancel == nil
		s.subMu.Unlock()
		if stopped {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if s.refreshCancel != nil {
		t.Error("refresh loop not stopped after last unsubscribe")
	}

	// 再次订阅应重新启动循环
	_, unsub2 := s.Subscribe()
	defer unsub2()
	if s.refreshCancel == nil {
		t.Error("refresh loop not restarted on second subscribe")
	}
}

// TestLoopInterval verifies the loop interval picks the minimum TTL.
func TestLoopInterval(t *testing.T) {
	tests := []struct {
		name      string
		node, job time.Duration
		want      time.Duration
	}{
		{"默认 60s 最短", 60 * time.Second, 10 * time.Minute, 60 * time.Second},
		{"node 配 3600s 用 10m 兜底", 3600 * time.Second, 10 * time.Minute, 10 * time.Minute},
		{"node 配 15s 下限保护", 15 * time.Second, 10 * time.Minute, 15 * time.Second},
		{"node 配 5s 低于下限抬到 15s", 5 * time.Second, 10 * time.Minute, 15 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := loopMinTTL(tt.node, tt.job, 30*time.Minute)
			if got != tt.want {
				t.Errorf("loopMinTTL(%v, %v) = %v, want %v", tt.node, tt.job, got, tt.want)
			}
		})
	}
}
