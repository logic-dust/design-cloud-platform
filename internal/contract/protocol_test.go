package contract

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"testing"
)

func TestProtocolJSONRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want Protocol
	}{
		{name: "RDP", in: `"RDP"`, want: RDPProtocol},
		{name: "XRDP", in: `"XRDP"`, want: XRDPProtocol},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var p Protocol
			if err := json.Unmarshal([]byte(tt.in), &p); err != nil {
				t.Fatal(err)
			}
			if p != tt.want {
				t.Fatalf("got %v want %s", p, tt.name)
			}
			out, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != tt.in {
				t.Fatalf("marshal = %s", out)
			}
		})
	}
}

func TestParseProtocol(t *testing.T) {
	t.Parallel()
	p, err := ParseProtocol("vnc")
	if err != nil || p != VNCProtocol {
		t.Fatalf("parse vnc: %v %v", p, err)
	}
	x, err := ParseProtocol("XRDP")
	if err != nil || x != XRDPProtocol {
		t.Fatalf("parse xrdp: %v %v", x, err)
	}
	if !x.UsesXDisplay() || x.DialDefaultPort() != 3389 {
		t.Fatalf("xrdp helpers: usesDisplay=%v port=%d", x.UsesXDisplay(), x.DialDefaultPort())
	}
}
