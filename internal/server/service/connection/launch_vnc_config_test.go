package connection

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"log/slog"
	"slices"
	"testing"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
	"design-cloud-engine/internal/model"
)

type stubLaunchSettings struct {
	agent schema.AgentSettings
}

func (s stubLaunchSettings) MaxPerUserCap(ctx context.Context) (int, error) {
	return 16, nil
}

func (s stubLaunchSettings) GetConnectionSettings(ctx context.Context) (schema.ConnectionSettings, error) {
	return schema.ConnectionSettings{MaxPerUserCap: 16}, nil
}

func (s stubLaunchSettings) GetEffectiveAgentSettingsForNode(ctx context.Context, nodeID string) (schema.AgentSettings, error) {
	return s.agent, nil
}

type capturingExecutor struct {
	req  *execution.ExecuteRequest
	port int
}

func (c *capturingExecutor) Execute(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	c.req = req
	port := c.port
	if port == 0 {
		port = 5901
	}
	out, err := json.Marshal(payload.VNCControlResult{Port: port})
	if err != nil {
		return nil, err
	}
	return &execution.ExecuteResponse{Success: true, Stdout: string(out)}, nil
}

func newLaunchArgsService(t *testing.T, serverArgs []string, exec *capturingExecutor) *Service {
	t.Helper()
	return &Service{
		settingsSvc: stubLaunchSettings{agent: schema.AgentSettings{
			VNC: schema.VNCConfig{
				BinDir:     "/opt/vnc",
				ServerArgs: serverArgs,
			},
		}},
		execSvc: exec,
		logger:  slog.Default(),
	}
}

func testLaunchDcExecVNC(ctx context.Context, svc *Service, node model.Node, username string, display int, connectionID uint, vncConfig json.RawMessage) (uint, uint, error) {
	fixed := display
	_, port, pid, err := svc.launchVNCOnNode(ctx, node, username, connectionID, &fixed, nil, vncConfig)
	return port, pid, err
}

// 资源 vncConfig 必须与节点默认 ServerArgs 合并，而不是整体替换。
func TestLaunchDcExecVNC_MergesResourceConfigWithNodeArgs(t *testing.T) {
	t.Parallel()

	exec := &capturingExecutor{}
	svc := newLaunchArgsService(t, []string{"-wm", "2d"}, exec)

	vncConfig := json.RawMessage(`{"resolution":"1600x900","startOptions":"-alwaysshared"}`)
	port, _, err := testLaunchDcExecVNC(context.Background(), svc, model.Node{NodeID: "node-1"}, "alice", 1, 42, vncConfig)
	if err != nil {
		t.Fatalf("launch vnc: %v", err)
	}
	if port != 5901 {
		t.Fatalf("port = %d, want 5901", port)
	}

	vncPayload, err := execution.GetPayload[payload.VNCControl](exec.req)
	if err != nil {
		t.Fatalf("get payload: %v", err)
	}
	want := []string{"-wm", "2d", "-geometry", "1600x900", "-alwaysshared"}
	if !slices.Equal(vncPayload.ServerArgs, want) {
		t.Fatalf("serverArgs = %v, want %v", vncPayload.ServerArgs, want)
	}
}

func TestLaunchDcExecVNC_NoResourceConfigKeepsNodeArgs(t *testing.T) {
	t.Parallel()

	exec := &capturingExecutor{}
	svc := newLaunchArgsService(t, []string{"-wm", "2d"}, exec)

	if _, _, err := testLaunchDcExecVNC(context.Background(), svc, model.Node{NodeID: "node-1"}, "alice", 1, 42, nil); err != nil {
		t.Fatalf("launch vnc: %v", err)
	}

	vncPayload, err := execution.GetPayload[payload.VNCControl](exec.req)
	if err != nil {
		t.Fatalf("get payload: %v", err)
	}
	if !slices.Equal(vncPayload.ServerArgs, []string{"-wm", "2d"}) {
		t.Fatalf("serverArgs = %v, want node defaults", vncPayload.ServerArgs)
	}
	if vncPayload.StartOptions != "" {
		t.Fatalf("startOptions = %q, want empty", vncPayload.StartOptions)
	}
}

// 非法 vncConfig 必须显式报错，不能静默回退到节点默认参数。
func TestLaunchDcExecVNC_InvalidResourceConfigReturnsError(t *testing.T) {
	t.Parallel()

	exec := &capturingExecutor{}
	svc := newLaunchArgsService(t, []string{"-wm", "2d"}, exec)

	_, _, err := testLaunchDcExecVNC(context.Background(), svc, model.Node{NodeID: "node-1"}, "alice", 1, 42, json.RawMessage(`{"resolution":`))
	if err == nil {
		t.Fatal("expected error for invalid vncConfig")
	}
	if exec.req != nil {
		t.Fatal("PrivilegedExec must not be invoked with invalid vncConfig")
	}
}
