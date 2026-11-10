//go:build !windows

package main

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// InstallCACertificate stub for non-Windows platforms.
func InstallCACertificate(pemData []byte) error {
	remaining := pemData
	found := false
	for len(remaining) > 0 {
		var block *pem.Block
		block, remaining = pem.Decode(remaining)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" || len(block.Bytes) == 0 {
			continue
		}
		parsed, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return err
		}
		fmt.Printf("[Dry-run Non-Windows] Would install CA: %s (SHA256: %s)\n",
			parsed.Subject.CommonName, formatFingerprint(parsed))
		found = true
	}
	if !found {
		return errors.New("no valid CERTIFICATE block found in PEM data")
	}
	return nil
}

func formatFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	encoded := hex.EncodeToString(sum[:])
	var sb strings.Builder
	for i := 0; i < len(encoded); i += 2 {
		if i > 0 {
			sb.WriteByte(':')
		}
		sb.WriteString(strings.ToUpper(encoded[i : i+2]))
	}
	return sb.String()
}
