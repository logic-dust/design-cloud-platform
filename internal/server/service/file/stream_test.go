package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCopyDownloadStreamCopiesKnownSize(t *testing.T) {
	var dst bytes.Buffer
	written, err := copyDownloadStream(context.Background(), &dst, strings.NewReader("hello trailing"), 5)
	if err != nil {
		t.Fatalf("copy stream: %v", err)
	}
	if written != 5 {
		t.Fatalf("written = %d, want 5", written)
	}
	if dst.String() != "hello" {
		t.Fatalf("copied %q, want %q", dst.String(), "hello")
	}
}

func TestCopyDownloadStreamUnexpectedEOF(t *testing.T) {
	var dst bytes.Buffer
	written, err := copyDownloadStream(context.Background(), &dst, strings.NewReader("hi"), 3)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want %v", err, io.ErrUnexpectedEOF)
	}
	if written != 2 {
		t.Fatalf("written = %d, want 2", written)
	}
	if dst.String() != "hi" {
		t.Fatalf("copied %q, want %q", dst.String(), "hi")
	}
}

func TestCopyDownloadStreamReturnsWrittenBytesOnWriteError(t *testing.T) {
	written, err := copyDownloadStream(context.Background(), failAfterWriter{limit: 2}, strings.NewReader("hello"), 5)
	if err == nil {
		t.Fatal("copy stream returned nil, want write error")
	}
	if written != 2 {
		t.Fatalf("written = %d, want 2", written)
	}
}

type failAfterWriter struct {
	limit   int64
	written int64
}

func (w failAfterWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.written
	if remaining <= 0 {
		return 0, errTestWrite
	}
	if int64(len(p)) > remaining {
		return int(remaining), errTestWrite
	}
	return len(p), nil
}

var errTestWrite = errors.New("test write failed")
