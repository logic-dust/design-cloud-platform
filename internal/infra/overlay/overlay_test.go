package overlay

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOverlayRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	dummyExe := filepath.Join(tmpDir, "app.exe")
	outExe := filepath.Join(tmpDir, "app_injected.exe")

	rawContent := []byte("MZ...This is a mock Windows PE executable header and code sections...")
	if err := os.WriteFile(dummyExe, rawContent, 0755); err != nil {
		t.Fatalf("write dummy exe: %v", err)
	}

	// 1. Verify no overlay initially
	_, err := ReadFile(dummyExe)
	if !errors.Is(err, ErrNoOverlay) {
		t.Fatalf("expected ErrNoOverlay, got: %v", err)
	}

	// 2. Inject payload
	p := &Payload{
		CACertPEM: "-----BEGIN CERTIFICATE-----\nMIIB...fake...\n-----END CERTIFICATE-----",
		ServerURL: "https://dc.corp.internal:1234",
	}

	if err := Inject(dummyExe, outExe, p); err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	// 3. Read injected payload
	readP, err := ReadFile(outExe)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	if readP.CACertPEM != p.CACertPEM {
		t.Errorf("CACertPEM mismatch: got %q, want %q", readP.CACertPEM, p.CACertPEM)
	}
	if readP.ServerURL != p.ServerURL {
		t.Errorf("ServerURL mismatch: got %q, want %q", readP.ServerURL, p.ServerURL)
	}

	// 4. Test re-injection with different payload (strips old overlay)
	p2 := &Payload{
		CACertPEM: "NEW_CA_CERT",
		ServerURL: "https://dc2.corp.internal:8443",
	}
	reInjectedExe := filepath.Join(tmpDir, "app_reinjected.exe")
	if err := Inject(outExe, reInjectedExe, p2); err != nil {
		t.Fatalf("Re-inject failed: %v", err)
	}

	readP2, err := ReadFile(reInjectedExe)
	if err != nil {
		t.Fatalf("ReadFile re-injected failed: %v", err)
	}
	if readP2.CACertPEM != p2.CACertPEM || readP2.ServerURL != p2.ServerURL {
		t.Errorf("re-injected payload mismatch: got %+v, want %+v", readP2, p2)
	}

	// 5. Verify the base binary size was preserved
	f, err := os.Open(reInjectedExe)
	if err != nil {
		t.Fatalf("open reInjectedExe: %v", err)
	}
	defer func() { _ = f.Close() }()

	baseSize, err := BaseSize(f)
	if err != nil {
		t.Fatalf("BaseSize failed: %v", err)
	}
	if baseSize != int64(len(rawContent)) {
		t.Errorf("baseSize mismatch: got %d, want %d", baseSize, len(rawContent))
	}
}

func TestOverlayCorruptFooter(t *testing.T) {
	// A stream ending with MagicMarker but with an invalid large length
	buf := bytes.NewReader([]byte{0x00, 0x00, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 'D', 'C', '_', 'P', 'A', 'Y', 'L', 'D'})
	_, err := ReadFrom(buf)
	if !errors.Is(err, ErrCorruptOverlay) {
		t.Fatalf("expected ErrCorruptOverlay, got: %v", err)
	}
}
