package common

import (
	"bytes"
	"strings"
	"testing"
)

func TestBoundedBufferWriteWithinLimit(t *testing.T) {
	t.Parallel()

	buf := NewBoundedBuffer(64)
	data := []byte("hello world")
	n, err := buf.Write(data)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if n != len(data) {
		t.Fatalf("Write returned %d, want %d", n, len(data))
	}
	if buf.Truncated() {
		t.Fatal("buffer should not be truncated")
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Fatalf("Bytes() = %q, want %q", buf.Bytes(), data)
	}
}

func TestBoundedBufferDropsExcessBytes(t *testing.T) {
	t.Parallel()

	buf := NewBoundedBuffer(4)
	n, err := buf.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	// Write must always report the full length consumed (io.Writer contract).
	if n != 5 {
		t.Fatalf("Write returned %d, want 5", n)
	}
	if !buf.Truncated() {
		t.Fatal("buffer should be truncated")
	}
	if !bytes.Equal(buf.Bytes(), []byte("hell")) {
		t.Fatalf("Bytes() = %q, want %q", buf.Bytes(), "hell")
	}
}

func TestBoundedBufferExactLimitIsNotTruncated(t *testing.T) {
	t.Parallel()

	buf := NewBoundedBuffer(5)
	_, _ = buf.Write([]byte("hello")) //nolint:errcheck
	if buf.Truncated() {
		t.Fatal("write of exactly limit bytes should not be truncated")
	}
	if !bytes.Equal(buf.Bytes(), []byte("hello")) {
		t.Fatalf("Bytes() = %q, want %q", buf.Bytes(), "hello")
	}
}

func TestBoundedBufferCumulativeWritesTruncate(t *testing.T) {
	t.Parallel()

	buf := NewBoundedBuffer(4)
	_, _ = buf.Write([]byte("ab")) //nolint:errcheck
	_, _ = buf.Write([]byte("cd")) //nolint:errcheck
	if buf.Truncated() {
		t.Fatal("two writes totalling exactly the limit should not truncate")
	}
	_, _ = buf.Write([]byte("e")) //nolint:errcheck
	if !buf.Truncated() {
		t.Fatal("write past limit should truncate")
	}
	if !bytes.Equal(buf.Bytes(), []byte("abcd")) {
		t.Fatalf("Bytes() = %q, want %q", buf.Bytes(), "abcd")
	}
}

func TestBoundedBufferBytesIsRawWithoutTruncationNotice(t *testing.T) {
	t.Parallel()

	buf := NewBoundedBuffer(3)
	_, _ = buf.Write([]byte("hello")) //nolint:errcheck

	if bytes.Contains(buf.Bytes(), []byte("truncated")) {
		t.Fatalf("Bytes() should not contain truncation notice, got: %q", buf.Bytes())
	}
	notice := buf.String()
	if !strings.Contains(notice, "truncated") {
		t.Fatalf("String() should contain truncation notice, got: %q", notice)
	}
}

func TestBoundedBufferZeroValueUsesDefaultLimit(t *testing.T) {
	t.Parallel()

	var buf BoundedBuffer
	// Writing exactly DefaultMaxCommandOutputBytes should not truncate.
	_, _ = buf.Write(make([]byte, DefaultMaxCommandOutputBytes)) //nolint:errcheck
	if buf.Truncated() {
		t.Fatalf("zero-value BoundedBuffer should accept %d bytes without truncation", DefaultMaxCommandOutputBytes)
	}
	// One more byte should trigger truncation.
	_, _ = buf.Write([]byte{0}) //nolint:errcheck
	if !buf.Truncated() {
		t.Fatal("byte past default limit should truncate")
	}
}

func TestNewBoundedBufferUsesCustomLimit(t *testing.T) {
	t.Parallel()

	buf := NewBoundedBuffer(10)
	_, _ = buf.Write(bytes.Repeat([]byte("x"), 10)) //nolint:errcheck
	if buf.Truncated() {
		t.Fatal("write exactly at custom limit should not truncate")
	}
	_, _ = buf.Write([]byte("y")) //nolint:errcheck
	if !buf.Truncated() {
		t.Fatal("write past custom limit should truncate")
	}
}

func TestBoundedBufferStringWithoutTruncationHasNoNotice(t *testing.T) {
	t.Parallel()

	buf := NewBoundedBuffer(64)
	_, _ = buf.Write([]byte("ok")) //nolint:errcheck
	if got := buf.String(); got != "ok" {
		t.Fatalf("String() = %q, want %q", got, "ok")
	}
}
