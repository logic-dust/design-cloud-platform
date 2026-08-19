package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// defaultRDPPort is the RDP listener port assumed when the registry value is absent.
const defaultRDPPort uint32 = 3389

// rdpDialTimeout bounds the local 3389 handshake so a probe never stalls the heartbeat.
const rdpDialTimeout = 500 * time.Millisecond

// Probe primitives are package-level variables so windows_only tests can inject fake
// implementations instead of depending on the host's real policy/service/port state.
var (
	readRDPPolicyFn  = readRDPPolicy
	readRDPPortFn    = readRDPPort
	serviceRunningFn = termServiceRunning
	dialRDPPortFn    = dialRDPPort
)

// CollectRemoteConnections returns an empty slice on Windows.
// Windows remote desktop (RDP) is a single system service (TermService), not
// multi-process VNC scanning; RDP readiness is reported via heartbeat.
func CollectRemoteConnections(_ context.Context) ([]ConnectionSnapshot, error) {
	return nil, nil
}

// CollectRDPReadiness checks whether Remote Desktop can accept connections.
// Evaluation requires all three signals when policy allows connections:
//  1. Registry policy: HKLM\SYSTEM\CurrentControlSet\Control\Terminal Server\fDenyTSConnections == 0
//  2. SCM: TermService state is SERVICE_RUNNING
//  3. Network: local 127.0.0.1:<port> handshake succeeds
//
// Determinate is false only when neither the policy nor the service state can be read:
// the caller must then leave the server-side connection state untouched instead of
// reporting a state it did not actually observe.
func CollectRDPReadiness() RDPReadiness {
	port := readRDPPortFn()
	if port == 0 {
		port = defaultRDPPort
	}

	denied, policyErr := readRDPPolicyFn()
	if policyErr == nil && denied {
		return RDPReadiness{
			Determinate: true,
			Port:        port,
			Reason:      "policy denies RDP: fDenyTSConnections != 0",
		}
	}

	running, serviceErr := serviceRunningFn()
	portErr := dialRDPPortFn(port)

	// Indeterminate only when both policy and service probes failed to answer.
	if policyErr != nil && serviceErr != nil {
		return RDPReadiness{
			Port:   port,
			Reason: fmt.Sprintf("indeterminate: registry=%v, scm=%v", policyErr, serviceErr),
		}
	}

	// Ready only when policy allows (or is unread but service answered), service is
	// running, and the listener port accepts a local handshake.
	policyAllows := policyErr != nil || !denied
	ready := policyAllows && running && portErr == nil
	return RDPReadiness{
		Determinate: true,
		Available:   ready,
		Port:        port,
		Reason: fmt.Sprintf("policy_allows=%t, TermService running=%t (%v), tcp/%d reachable=%t (%v)",
			policyAllows, running, serviceErr, port, portErr == nil, portErr),
	}
}

// readRDPPolicy reports whether the RDP policy forbids connections.
// A missing fDenyTSConnections value means "not configured" and therefore not denied.
func readRDPPolicy() (bool, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Terminal Server`, registry.QUERY_VALUE)
	if err != nil {
		return false, fmt.Errorf("open Terminal Server key: %w", err)
	}
	defer key.Close()

	deny, _, err := key.GetIntegerValue("fDenyTSConnections")
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read fDenyTSConnections: %w", err)
	}
	return deny != 0, nil
}

// readRDPPort reads the configured RDP listener port from the RDP-Tcp WinStation.
// Any read failure or out-of-range value falls back to defaultRDPPort.
func readRDPPort() uint32 {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp`, registry.QUERY_VALUE)
	if err != nil {
		return defaultRDPPort
	}
	defer key.Close()

	port, _, err := key.GetIntegerValue("PortNumber")
	if err != nil || port == 0 || port > 65535 {
		return defaultRDPPort
	}
	return uint32(port)
}

// termServiceRunning reports whether the TermService (Remote Desktop Services) is running.
func termServiceRunning() (bool, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false, fmt.Errorf("open service control manager: %w", err)
	}
	defer windows.CloseServiceHandle(scm)

	name, err := windows.UTF16PtrFromString("TermService")
	if err != nil {
		return false, err
	}
	service, err := windows.OpenService(scm, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false, fmt.Errorf("open TermService: %w", err)
	}
	defer windows.CloseServiceHandle(service)

	var status windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(service, &status); err != nil {
		return false, fmt.Errorf("query TermService status: %w", err)
	}
	return status.CurrentState == windows.SERVICE_RUNNING, nil
}

// dialRDPPort attempts a local TCP handshake against the RDP listener port.
func dialRDPPort(port uint32) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), rdpDialTimeout)
	if err != nil {
		return err
	}
	return conn.Close()
}
