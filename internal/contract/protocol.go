package contract

import (
	"database/sql/driver"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"strings"
)

// Protocol is a persisted connection/proxy protocol (DB smallint; JSON string).
type Protocol int

const (
	ProtocolUnknown Protocol = 0
	VNCProtocol     Protocol = 1
	RDPProtocol     Protocol = 2 // Windows / generic RDP dial (no X display lifecycle)
	SSHProtocol     Protocol = 3
	XRDPProtocol    Protocol = 4 // Linux xrdp (RDP wire + X display seat; probe/kill via connection-probe)
)

func (p Protocol) Valid() bool {
	switch p {
	case VNCProtocol, RDPProtocol, SSHProtocol, XRDPProtocol:
		return true
	default:
		return false
	}
}

// UsesXDisplay reports whether resources may carry an X display reservation.
func (p Protocol) UsesXDisplay() bool {
	return p == VNCProtocol || p == XRDPProtocol
}

// DialDefaultPort is the TCP port used when the resource leaves port unset.
func (p Protocol) DialDefaultPort() uint {
	switch p {
	case RDPProtocol, XRDPProtocol:
		return 3389
	case SSHProtocol:
		return 22
	default:
		return 0
	}
}

func (p Protocol) String() string {
	switch p {
	case VNCProtocol:
		return "VNC"
	case RDPProtocol:
		return "RDP"
	case SSHProtocol:
		return "SSH"
	case XRDPProtocol:
		return "XRDP"
	default:
		return ""
	}
}

func ParseProtocol(s string) (Protocol, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "VNC":
		return VNCProtocol, nil
	case "RDP":
		return RDPProtocol, nil
	case "SSH":
		return SSHProtocol, nil
	case "XRDP":
		return XRDPProtocol, nil
	case "":
		return ProtocolUnknown, nil
	default:
		return ProtocolUnknown, fmt.Errorf("unknown protocol: %q", s)
	}
}

func (p Protocol) MarshalJSON() ([]byte, error) {
	if !p.Valid() {
		return json.Marshal("")
	}
	return json.Marshal(p.String())
}

func (p *Protocol) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err == nil {
		parsed, err := ParseProtocol(strVal)
		if err != nil {
			return err
		}
		*p = parsed
		return nil
	}
	var intVal int
	if err := json.Unmarshal(data, &intVal); err != nil {
		return fmt.Errorf("invalid Protocol: %s", string(data))
	}
	*p = Protocol(intVal)
	if !p.Valid() && *p != ProtocolUnknown {
		return fmt.Errorf("invalid protocol value: %d", intVal)
	}
	return nil
}

// Value implements driver.Valuer for GORM/PostgreSQL.
func (p Protocol) Value() (driver.Value, error) {
	if !p.Valid() {
		return nil, fmt.Errorf("invalid protocol: %d", p)
	}
	return int64(p), nil
}

// Scan implements sql.Scanner for GORM/PostgreSQL.
func (p *Protocol) Scan(value any) error {
	if value == nil {
		*p = ProtocolUnknown
		return nil
	}
	switch v := value.(type) {
	case int64:
		*p = Protocol(v)
	case int32:
		*p = Protocol(v)
	case int:
		*p = Protocol(v)
	case []byte:
		return p.Scan(string(v))
	case string:
		parsed, err := ParseProtocol(v)
		if err != nil {
			return err
		}
		*p = parsed
		return nil
	default:
		return fmt.Errorf("unsupported protocol column type: %T", value)
	}
	if !p.Valid() && *p != ProtocolUnknown {
		return fmt.Errorf("invalid protocol value: %d", *p)
	}
	return nil
}
