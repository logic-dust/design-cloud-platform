package connection

import (
	"log/slog"
	"slices"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

//go:fix inline
func intPtr(n int) *int {
	return new(n)
}

//go:fix inline
func uintPtr(n uint) *uint {
	return new(n)
}

func TestMatchConnectionResource_Pure(t *testing.T) {
	node01 := "node01"
	edaGroup := "eda-group"

	resFixed1 := model.ConnectionResource{
		ID:         1,
		Name:       "Fixed-Display-1",
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{node01},
		Display:    new(1),
		MaxPerUser: 1,
		Public:     true,
	}

	resDynamicPool := model.ConnectionResource{
		ID:         2,
		Name:       "EDA-Dynamic-Pool",
		Protocol:   contract.VNCProtocol,
		NodeGroups: []string{edaGroup},
		Display:    nil,
		MaxPerUser: 2,
		Public:     true,
	}

	resDynamicSingleNode := model.ConnectionResource{
		ID:         3,
		Name:       "Node01-Dynamic",
		Protocol:   contract.VNCProtocol,
		Nodes:      []string{node01},
		Display:    nil,
		MaxPerUser: 1,
		Public:     true,
	}

	resPrivatePool := model.ConnectionResource{
		ID:         4,
		Name:       "Private-Dynamic-Pool",
		Protocol:   contract.VNCProtocol,
		NodeGroups: []string{edaGroup},
		Display:    nil,
		MaxPerUser: 2,
		Public:     false,
	}

	t.Run("fixed display beats dynamic pool", func(t *testing.T) {
		in := resourceMatchInput{
			Resources:  []model.ConnectionResource{resDynamicPool, resFixed1},
			NodeID:     node01,
			NodeGroups: []string{edaGroup},
			Protocol:   contract.VNCProtocol,
			Display:    new(1),
		}
		res, tied, ok := matchConnectionResource(in)
		if !ok || len(tied) > 0 {
			t.Fatalf("expected match, got ok=%v tied=%d", ok, len(tied))
		}
		if res.ID != resFixed1.ID {
			t.Fatalf("expected res %d, got %d", resFixed1.ID, res.ID)
		}
	})

	t.Run("dynamic pool matches display in range 1..99", func(t *testing.T) {
		in := resourceMatchInput{
			Resources:  []model.ConnectionResource{resDynamicPool, resFixed1},
			NodeID:     node01,
			NodeGroups: []string{edaGroup},
			Protocol:   contract.VNCProtocol,
			Display:    new(2),
		}
		res, tied, ok := matchConnectionResource(in)
		if !ok || len(tied) > 0 {
			t.Fatalf("expected match, got ok=%v tied=%d", ok, len(tied))
		}
		if res.ID != resDynamicPool.ID {
			t.Fatalf("expected res %d, got %d", resDynamicPool.ID, res.ID)
		}
	})

	t.Run("node direct hit beats node groups", func(t *testing.T) {
		in := resourceMatchInput{
			Resources:  []model.ConnectionResource{resDynamicPool, resDynamicSingleNode},
			NodeID:     node01,
			NodeGroups: []string{edaGroup},
			Protocol:   contract.VNCProtocol,
			Display:    new(5),
		}
		res, _, ok := matchConnectionResource(in)
		if !ok || res.ID != resDynamicSingleNode.ID {
			t.Fatalf("expected single node res %d, got ok=%v res=%+v", resDynamicSingleNode.ID, ok, res)
		}
	})

	t.Run("public auth beats private when other keys equal", func(t *testing.T) {
		in := resourceMatchInput{
			Resources:  []model.ConnectionResource{resPrivatePool, resDynamicPool},
			NodeID:     node01,
			NodeGroups: []string{edaGroup},
			Protocol:   contract.VNCProtocol,
			Display:    new(5),
		}
		// 公开规格优先：无归属实例不会先消耗私有规格的配额。
		res, _, ok := matchConnectionResource(in)
		if !ok || res.ID != resDynamicPool.ID {
			t.Fatalf("expected public pool %d, got ok=%v res=%+v", resDynamicPool.ID, ok, res)
		}
	})

	t.Run("four keys tied returns false and tied list", func(t *testing.T) {
		tied1 := model.ConnectionResource{
			ID:         10,
			Name:       "Tied-Pool-A",
			Protocol:   contract.VNCProtocol,
			NodeGroups: []string{edaGroup},
			Display:    nil,
			Public:     true,
		}
		tied2 := model.ConnectionResource{
			ID:         20,
			Name:       "Tied-Pool-B",
			Protocol:   contract.VNCProtocol,
			NodeGroups: []string{edaGroup},
			Display:    nil,
			Public:     true,
		}
		in := resourceMatchInput{
			Resources:  []model.ConnectionResource{tied1, tied2},
			NodeID:     node01,
			NodeGroups: []string{edaGroup},
			Protocol:   contract.VNCProtocol,
			Display:    new(3),
		}
		_, tied, ok := matchConnectionResource(in)
		if ok {
			t.Fatalf("expected tied false, got ok=true")
		}
		if len(tied) != 2 {
			t.Fatalf("expected 2 tied resources, got %d", len(tied))
		}
	})

	t.Run("display outside 1..99 not claimed by dynamic pool", func(t *testing.T) {
		inDynamic := resourceMatchInput{
			Resources:  []model.ConnectionResource{resDynamicPool},
			NodeID:     node01,
			NodeGroups: []string{edaGroup},
			Protocol:   contract.VNCProtocol,
			Display:    new(0),
		}
		if _, _, ok := matchConnectionResource(inDynamic); ok {
			t.Fatalf("display 0 must not be claimed by dynamic pool")
		}
	})

	t.Run("protocol mismatch or display nil or node not matching", func(t *testing.T) {
		inNil := resourceMatchInput{
			Resources:  []model.ConnectionResource{resFixed1},
			NodeID:     node01,
			NodeGroups: []string{edaGroup},
			Protocol:   contract.VNCProtocol,
			Display:    nil,
		}
		if _, _, ok := matchConnectionResource(inNil); ok {
			t.Fatalf("nil display should not match")
		}

		inProto := resourceMatchInput{
			Resources:  []model.ConnectionResource{resFixed1},
			NodeID:     node01,
			NodeGroups: []string{edaGroup},
			Protocol:   contract.RDPProtocol,
			Display:    new(1),
		}
		if _, _, ok := matchConnectionResource(inProto); ok {
			t.Fatalf("proto mismatch should not match")
		}

		inNode := resourceMatchInput{
			Resources:  []model.ConnectionResource{resFixed1},
			NodeID:     "node99",
			NodeGroups: nil,
			Protocol:   contract.VNCProtocol,
			Display:    new(1),
		}
		if _, _, ok := matchConnectionResource(inNode); ok {
			t.Fatalf("node mismatch should not match")
		}
	})
}

func TestWarnTiedCandidateResources_Deduplication(t *testing.T) {
	svc := &Service{
		logger: slog.Default(),
	}
	disp := 1
	tied := []model.ConnectionResource{
		{ID: 10},
		{ID: 20},
	}
	// 首次记录并打 Warn
	svc.warnTiedCandidateResources("node01", "alice", &disp, tied)

	svc.tiedWarnMu.Lock()
	val, ok := svc.tiedWarned["node01:alice:1"]
	svc.tiedWarnMu.Unlock()
	if !ok || val != "10,20" {
		t.Fatalf("expected recorded key 10,20, got %q", val)
	}

	// 第二次调用相同的 tied 集合，应被去重拦截（内部直接 return）
	svc.warnTiedCandidateResources("node01", "alice", &disp, slices.Clone(tied))
}
