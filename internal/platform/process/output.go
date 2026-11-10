// Package process holds support utilities for capturing the output of external
// processes. It is shared by the server, the agent and the PrivilegedExec, so it must
// stay free of business and transport imports.
package process

import (
	"cmp"
	"fmt"
)

// DefaultMaxCommandOutputBytes is the default maximum number of bytes captured
// from a single command's stdout or stderr.
const DefaultMaxCommandOutputBytes = 4 * 1024 * 1024 // 4 MiB

// BoundedBuffer is a write-only byte sink that caps captured output at a
// configurable limit (defaulting to DefaultMaxCommandOutputBytes for zero-value
// instances). Excess writes are discarded; after the limit is exceeded the
// buffer is marked as truncated.
//
// BoundedBuffer is NOT safe for concurrent writes.
type BoundedBuffer struct {
	buf       []byte
	limit     int // 0 means use DefaultMaxCommandOutputBytes
	truncated bool
}

// NewBoundedBuffer creates a BoundedBuffer with a custom size limit.
// Use DefaultMaxCommandOutputBytes for the standard 4 MiB cap.
func NewBoundedBuffer(limit int) *BoundedBuffer {
	return &BoundedBuffer{limit: limit}
}

// effectiveLimit returns the active limit, falling back to
// DefaultMaxCommandOutputBytes for zero-value instances (limit == 0).
func (b *BoundedBuffer) effectiveLimit() int {
	return cmp.Or(b.limit, DefaultMaxCommandOutputBytes)
}

// Write implements io.Writer.
// Bytes beyond the limit are silently dropped.
func (b *BoundedBuffer) Write(p []byte) (int, error) {
	if b.truncated {
		return len(p), nil
	}
	lim := b.effectiveLimit()
	remaining := lim - len(b.buf)
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	n := min(len(p), remaining)
	b.buf = append(b.buf, p[:n]...)
	if n < len(p) {
		b.truncated = true
	}
	return len(p), nil
}

// Bytes returns the raw captured output (without any truncation notice).
func (b *BoundedBuffer) Bytes() []byte {
	return b.buf
}

// Truncated reports whether any output was dropped due to the size limit.
func (b *BoundedBuffer) Truncated() bool {
	return b.truncated
}

// String returns the captured output.
// If the buffer was truncated, a notice line is appended.
func (b *BoundedBuffer) String() string {
	if !b.truncated {
		return string(b.buf)
	}
	return string(b.buf) + fmt.Sprintf("\n[output truncated at %d bytes]", b.effectiveLimit())
}

// Grow pre-allocates the underlying buffer to capacity n.
// It ensures that the capacity does not exceed the buffer's effective limit.
func (b *BoundedBuffer) Grow(n int) {
	if n <= 0 {
		return
	}
	lim := b.effectiveLimit()
	n = min(n, lim)
	if cap(b.buf)-len(b.buf) < n {
		newBuf := make([]byte, len(b.buf), len(b.buf)+n)
		copy(newBuf, b.buf)
		b.buf = newBuf
	}
}
