package connection

import (
	"net/http"
	"testing"
)

func TestPublicHTTP_Sentinels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err        error
		wantStatus int
		wantMsg    string
	}{
		{ErrCandidateSetEmpty, http.StatusUnprocessableEntity, "candidate node set is empty"},
		{ErrNoHealthyNode, http.StatusServiceUnavailable, "no healthy online nodes"},
		{ErrAllNodesOccupied, http.StatusConflict, "all candidate nodes are already occupied"},
		{ErrNodeConnectionLimit, http.StatusConflict, "node connection limit reached"},
		{ErrTargetNodeUnavailable, http.StatusServiceUnavailable, "target node is not available"},
		{ErrDisplayOccupied, http.StatusConflict, "display is already occupied"},
		{ErrConnectionStillStarting, http.StatusConflict, "connection is still starting"},
	}
	for _, tc := range tests {
		status, msg, ok := PublicHTTP(tc.err)
		if !ok {
			t.Fatalf("%v: expected ok", tc.err)
		}
		if status != tc.wantStatus || msg != tc.wantMsg {
			t.Fatalf("%v: got (%d, %q)", tc.err, status, msg)
		}
	}
}
