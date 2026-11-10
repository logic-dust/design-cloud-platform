package agent

import (
	"testing"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/execution"

	"google.golang.org/protobuf/types/known/durationpb"
)

func TestFromCommandRequestRequiresJSONObjectPayload(t *testing.T) {
	t.Parallel()

	base := func(payload string) *pb.CommandRequest {
		return &pb.CommandRequest{
			TraceId:     "trace-1",
			ExecutionId: "request-1",
			ActionName:  string(execution.ActionUserList),
			PayloadJson: payload,
			Timeout:     durationpb.New(30 * time.Second),
		}
	}

	if _, err := fromCommandRequest(base(`{"username":"alice"}`)); err != nil {
		t.Fatalf("valid object payload: %v", err)
	}
	if _, err := fromCommandRequest(base(`["alice"]`)); err == nil {
		t.Fatalf("expected non-object payload error")
	}
	if _, err := fromCommandRequest(base(`{`)); err == nil {
		t.Fatalf("expected invalid JSON error")
	}
}
