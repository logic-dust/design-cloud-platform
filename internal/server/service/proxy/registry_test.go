package proxy

import (
	"testing"

	"design-cloud-engine/internal/contract"
)

func TestProtocolRegistryContainsAllBuiltinProtocols(t *testing.T) {
	t.Parallel()

	r := newProtocolRegistry()

	for _, proto := range []contract.Protocol{contract.VNCProtocol, contract.RDPProtocol, contract.SSHProtocol, contract.XRDPProtocol} {
		h, err := r.Get(proto)
		if err != nil {
			t.Fatalf("protocol %q not registered: %v", proto, err)
		}
		if h.Name() != proto.String() {
			t.Fatalf("handler name = %q, want %q", h.Name(), proto.String())
		}
	}
}

func TestProtocolRegistryRejectsUnknownProtocol(t *testing.T) {
	t.Parallel()

	r := newProtocolRegistry()

	if _, err := r.Get(contract.Protocol(99)); err == nil {
		t.Fatal("expected error for unknown protocol, got nil")
	}
}
