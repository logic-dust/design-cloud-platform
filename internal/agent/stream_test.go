package agent

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
)

func TestPrependUniqueAddress(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		addresses []string
		preferred string
		want      []string
	}{
		{
			name:      "empty preferred",
			addresses: []string{"10.0.0.1:52255", "10.0.0.2:52255"},
			preferred: "",
			want:      []string{"10.0.0.1:52255", "10.0.0.2:52255"},
		},
		{
			name:      "new preferred address",
			addresses: []string{"10.0.0.1:52255", "10.0.0.2:52255"},
			preferred: "10.0.0.3:52255",
			want:      []string{"10.0.0.3:52255", "10.0.0.1:52255", "10.0.0.2:52255"},
		},
		{
			name:      "existing preferred address moved to front and deduplicated",
			addresses: []string{"10.0.0.1:52255", "10.0.0.2:52255", "10.0.0.3:52255"},
			preferred: "10.0.0.2:52255",
			want:      []string{"10.0.0.2:52255", "10.0.0.1:52255", "10.0.0.3:52255"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := prependUniqueAddress(tc.addresses, tc.preferred)
			if len(got) != len(tc.want) {
				t.Fatalf("got len %d, want len %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestLeaderRedirectAddr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		clusterPort int
		leaderID    string
		leaderAddr  string
		want        string
	}{
		{
			name:        "direct leader address specified",
			clusterPort: 52255,
			leaderID:    "node-primary",
			leaderAddr:  "192.168.1.100:52255",
			want:        "192.168.1.100:52255",
		},
		{
			name:        "leader ID specified without leader address",
			clusterPort: 52255,
			leaderID:    "node-primary",
			leaderAddr:  "",
			want:        "node-primary:52255",
		},
		{
			name:        "both empty",
			clusterPort: 52255,
			leaderID:    "",
			leaderAddr:  "",
			want:        "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := leaderRedirectAddr(tc.clusterPort, tc.leaderID, tc.leaderAddr)
			if got != tc.want {
				t.Errorf("leaderRedirectAddr = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRedirectError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  redirectError
		want string
	}{
		{
			name: "with reason",
			err:  redirectError{reason: "leader_demoted", leaderAddr: "10.0.0.1:52255"},
			want: "leader_demoted",
		},
		{
			name: "with leaderAddr only",
			err:  redirectError{leaderAddr: "10.0.0.1:52255"},
			want: "redirect to leader 10.0.0.1:52255",
		},
		{
			name: "with leaderID only",
			err:  redirectError{leaderID: "node-2"},
			want: "redirect to leader node-2",
		},
		{
			name: "empty",
			err:  redirectError{},
			want: "redirect to leader",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandleRedirect(t *testing.T) {
	t.Parallel()

	agent := NewAgent(nil, Config{
		NodeID:      "agent-1",
		ClusterPort: 52255,
	}, slog.Default(), nil)

	// 1. Nil command returns ErrServerShutdown
	if err := agent.handleRedirect(nil); !errors.Is(err, ErrServerShutdown) {
		t.Errorf("handleRedirect(nil) = %v, want ErrServerShutdown", err)
	}

	// 2. Command with leaderAddr and reason
	cmd := &pb.RedirectCommand{
		LeaderNodeId: "node-leader",
		LeaderAddr:   "10.0.0.99:52255",
		Reason:       "leader_demoted",
	}
	err := agent.handleRedirect(cmd)
	if err == nil {
		t.Fatal("handleRedirect want error, got nil")
	}

	var redErr *redirectError
	if !errors.As(err, &redErr) {
		t.Fatalf("expected redirectError, got %T: %v", err, err)
	}
	if redErr.leaderID != "node-leader" || redErr.leaderAddr != "10.0.0.99:52255" || redErr.reason != "leader_demoted" {
		t.Errorf("redirectError = %+v, unexpected fields", redErr)
	}
}

func TestReconnectionBackoffProgression(t *testing.T) {
	t.Parallel()

	currentDelay := minReconnectDelay
	expected := []time.Duration{
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		32 * time.Second,
		60 * time.Second, // Capped at maxReconnectDelay
		60 * time.Second,
	}

	for i, want := range expected {
		if currentDelay != want {
			t.Errorf("step %d: delay = %v, want %v", i, currentDelay, want)
		}
		currentDelay = min(currentDelay*2, maxReconnectDelay)
	}
}
