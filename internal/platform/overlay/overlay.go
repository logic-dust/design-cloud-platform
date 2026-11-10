package overlay

import (
	"bytes"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	footerLen = 12 // 4 bytes length + 8 bytes magic
)

var (
	// MagicMarker is the 8-byte trailing signature identifying the overlay payload.
	MagicMarker = []byte("DC_PAYLD")

	// ErrNoOverlay indicates that the executable does not contain an overlay payload.
	ErrNoOverlay = errors.New("no overlay payload found")

	// ErrCorruptOverlay indicates that the payload length or data is corrupted.
	ErrCorruptOverlay = errors.New("corrupt overlay payload")
)

// Payload defines the embedded metadata stored in the executable overlay.
type Payload struct {
	CACertPEM string `json:"caCertPem,omitempty"`
	ServerURL string `json:"serverUrl,omitempty"`
}

// ReadFrom reads the overlay payload from a ReadSeeker (e.g. an open executable file).
func ReadFrom(r io.ReadSeeker) (*Payload, error) {
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, fmt.Errorf("seek end: %w", err)
	}

	if size < footerLen {
		return nil, ErrNoOverlay
	}

	if _, err := r.Seek(-footerLen, io.SeekEnd); err != nil {
		return nil, fmt.Errorf("seek footer: %w", err)
	}

	var footer [footerLen]byte
	if _, err := io.ReadFull(r, footer[:]); err != nil {
		return nil, fmt.Errorf("read footer: %w", err)
	}

	magic := footer[4:]
	if !bytes.Equal(magic, MagicMarker) {
		return nil, ErrNoOverlay
	}

	payloadLen := binary.BigEndian.Uint32(footer[:4])
	if int64(payloadLen) > size-footerLen || payloadLen == 0 {
		return nil, ErrCorruptOverlay
	}

	offset := size - footerLen - int64(payloadLen)
	if _, err := r.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek payload: %w", err)
	}

	payloadBytes := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payloadBytes); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}

	var p Payload
	if err := json.Unmarshal(payloadBytes, &p); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}

	return &p, nil
}

// ReadExecutable reads the overlay payload from the currently running executable.
func ReadExecutable() (*Payload, error) {
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("get executable path: %w", err)
	}
	return ReadFile(exePath)
}

// ReadFile reads the overlay payload from the specified file path.
func ReadFile(path string) (*Payload, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ReadFrom(f)
}

// BaseSize returns the size of the binary without any overlay payload.
// If no overlay is present, it returns the total file size.
func BaseSize(r io.ReadSeeker) (int64, error) {
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, fmt.Errorf("seek end: %w", err)
	}

	if size < footerLen {
		return size, nil
	}

	if _, err := r.Seek(-footerLen, io.SeekEnd); err != nil {
		return 0, fmt.Errorf("seek footer: %w", err)
	}

	var footer [footerLen]byte
	if _, err := io.ReadFull(r, footer[:]); err != nil {
		return 0, fmt.Errorf("read footer: %w", err)
	}

	if !bytes.Equal(footer[4:], MagicMarker) {
		return size, nil
	}

	payloadLen := binary.BigEndian.Uint32(footer[:4])
	if int64(payloadLen) > size-footerLen || payloadLen == 0 {
		return size, nil
	}

	return size - footerLen - int64(payloadLen), nil
}

// Inject writes the payload into the output file by copying input (stripping any existing overlay)
// and appending the new overlay payload.
func Inject(inputPath, outputPath string, payload *Payload) error {
	if payload == nil {
		return errors.New("payload cannot be nil")
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	in, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("open input executable %q: %w", inputPath, err)
	}
	defer func() { _ = in.Close() }()

	baseSize, err := BaseSize(in)
	if err != nil {
		return fmt.Errorf("compute base binary size: %w", err)
	}

	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek start: %w", err)
	}

	out, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create output executable %q: %w", outputPath, err)
	}
	defer func() { _ = out.Close() }()

	// Copy base binary (without any old overlay)
	if _, err := io.CopyN(out, in, baseSize); err != nil {
		return fmt.Errorf("copy base binary: %w", err)
	}

	// Append payload
	if _, err := out.Write(payloadBytes); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}

	// Append 4 bytes length
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payloadBytes)))
	if _, err := out.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("write payload length: %w", err)
	}

	// Append 8 bytes magic marker
	if _, err := out.Write(MagicMarker); err != nil {
		return fmt.Errorf("write magic marker: %w", err)
	}

	return nil
}
