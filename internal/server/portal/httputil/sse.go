package httputil

import (
	"errors"
	"fmt"
	"net/http"
)

// InitSSEResponse writes the shared SSE response headers and performs the
// initial flush so the client receives the handshake immediately. When retrySec
// is positive it also writes a `retry: NNNN` reconnect hint (SSE spec) before
// flushing. Returns the flusher for writing subsequent frames.
func InitSSEResponse(w http.ResponseWriter, retrySec int) (http.Flusher, error) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if retrySec > 0 {
		_, _ = fmt.Fprintf(w, "retry: %d\n\n", retrySec*1000)
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}
	flusher.Flush()
	return flusher, nil
}
