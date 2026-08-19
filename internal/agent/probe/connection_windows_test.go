package probe

import (
	"errors"
	"testing"
)

// rdpProbeStub wires the injectable probe primitives to fixed values and records the
// port the TCP handshake was attempted on.
type rdpProbeStub struct {
	policyDenied bool
	policyErr    error
	serviceUp    bool
	serviceErr   error
	configured   uint32
	dialErr      error

	dialedPort uint32
}

func (s *rdpProbeStub) install(t *testing.T) {
	t.Helper()
	readRDPPolicyFn = func() (bool, error) { return s.policyDenied, s.policyErr }
	readRDPPortFn = func() uint32 { return s.configured }
	serviceRunningFn = func() (bool, error) { return s.serviceUp, s.serviceErr }
	dialRDPPortFn = func(port uint32) error {
		s.dialedPort = port
		return s.dialErr
	}
	t.Cleanup(func() {
		readRDPPolicyFn = readRDPPolicy
		readRDPPortFn = readRDPPort
		serviceRunningFn = termServiceRunning
		dialRDPPortFn = dialRDPPort
	})
}

func TestCollectRDPReadiness_PolicyDenies(t *testing.T) {
	stub := &rdpProbeStub{policyDenied: true, serviceUp: true, configured: 4567}
	stub.install(t)

	got := CollectRDPReadiness()
	if !got.Determinate {
		t.Fatalf("Determinate = false, want true (policy gave a definite answer)")
	}
	if got.Available {
		t.Errorf("Available = true, want false when fDenyTSConnections != 0")
	}
	if got.Port != 4567 {
		t.Errorf("Port = %d, want the configured 4567", got.Port)
	}
	if got.Reason == "" {
		t.Errorf("Reason must explain the verdict")
	}
}

func TestCollectRDPReadiness_AllThreeSignalsRequired(t *testing.T) {
	stub := &rdpProbeStub{serviceUp: true, configured: 4567}
	stub.install(t)

	got := CollectRDPReadiness()
	if !got.Determinate || !got.Available {
		t.Fatalf("got %+v, want determinate + available when policy allows, service runs, and port answers", got)
	}
	if stub.dialedPort != 4567 {
		t.Errorf("dialed port = %d, want the discovered port 4567 (not hardcoded 3389)", stub.dialedPort)
	}
}

func TestCollectRDPReadiness_ServiceAloneIsNotEnough(t *testing.T) {
	stub := &rdpProbeStub{serviceUp: true, configured: 4567, dialErr: errors.New("connection refused")}
	stub.install(t)

	got := CollectRDPReadiness()
	if !got.Determinate {
		t.Fatalf("Determinate = false, want true when probes answered")
	}
	if got.Available {
		t.Fatalf("Available = true, want false when TermService runs but the port handshake fails")
	}
}

func TestCollectRDPReadiness_OpenPortAloneIsNotEnough(t *testing.T) {
	stub := &rdpProbeStub{serviceUp: false, configured: 3390}
	stub.install(t)

	got := CollectRDPReadiness()
	if !got.Determinate {
		t.Fatalf("Determinate = false, want true when probes answered")
	}
	if got.Available {
		t.Fatalf("Available = true, want false when the port answers but TermService is stopped")
	}
	if got.Port != 3390 {
		t.Errorf("Port = %d, want 3390", got.Port)
	}
}

func TestCollectRDPReadiness_NotReady(t *testing.T) {
	stub := &rdpProbeStub{serviceUp: false, configured: 3389, dialErr: errors.New("connection refused")}
	stub.install(t)

	got := CollectRDPReadiness()
	if !got.Determinate {
		t.Fatalf("Determinate = false, want true when both probes answered")
	}
	if got.Available {
		t.Errorf("Available = true, want false when service is stopped and the port is closed")
	}
}

func TestCollectRDPReadiness_IndeterminateWhenBothProbesFail(t *testing.T) {
	stub := &rdpProbeStub{
		policyErr:  errors.New("registry unavailable"),
		serviceErr: errors.New("scm unavailable"),
		configured: 3389,
		dialErr:    errors.New("connection refused"),
	}
	stub.install(t)

	got := CollectRDPReadiness()
	if got.Determinate {
		t.Fatalf("got %+v, want Determinate=false so the server keeps the previous state", got)
	}
}

func TestCollectRDPReadiness_ZeroPortFallsBackToDefault(t *testing.T) {
	stub := &rdpProbeStub{serviceUp: true}
	stub.install(t)

	got := CollectRDPReadiness()
	if got.Port != defaultRDPPort {
		t.Errorf("Port = %d, want the default %d when the registry value is absent", got.Port, defaultRDPPort)
	}
	if stub.dialedPort != defaultRDPPort {
		t.Errorf("dialed port = %d, want %d", stub.dialedPort, defaultRDPPort)
	}
	if !got.Available {
		t.Errorf("Available = false, want true when all three signals succeed with default port")
	}
}
