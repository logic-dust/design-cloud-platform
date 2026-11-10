//go:build windows

package main

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modCrypt32 = windows.NewLazySystemDLL("crypt32.dll")

	procCertOpenStore                    = modCrypt32.NewProc("CertOpenStore")
	procCertAddEncodedCertificateToStore = modCrypt32.NewProc("CertAddEncodedCertificateToStore")
	procCertCloseStore                   = modCrypt32.NewProc("CertCloseStore")
)

const (
	CERT_STORE_PROV_SYSTEM_W = 10
	X509_ASN_ENCODING        = 0x00000001
	PKCS_7_ASN_ENCODING      = 0x00010000
	CERT_ENCODING_TYPE       = X509_ASN_ENCODING | PKCS_7_ASN_ENCODING

	CERT_SYSTEM_STORE_CURRENT_USER  = 0x00010000
	CERT_SYSTEM_STORE_LOCAL_MACHINE = 0x00020000

	CERT_STORE_ADD_REPLACE_EXISTING = 3
)

// InstallCACertificate installs the PEM-encoded CA certificate(s) into the Windows
// system Root Certificate Store ("ROOT"). It attempts LocalMachine first (if running as admin),
// and seamlessly falls back to CurrentUser if access is restricted.
func InstallCACertificate(pemData []byte) error {
	if len(pemData) == 0 {
		return errors.New("empty certificate data")
	}

	// Try LocalMachine first, fallback to CurrentUser
	storeName, err := windows.UTF16PtrFromString("ROOT")
	if err != nil {
		return fmt.Errorf("encode store name: %w", err)
	}

	hStore, _, _ := procCertOpenStore.Call(
		uintptr(CERT_STORE_PROV_SYSTEM_W),
		0,
		0,
		uintptr(CERT_SYSTEM_STORE_LOCAL_MACHINE),
		uintptr(unsafe.Pointer(storeName)),
	)

	isLocalMachine := true
	if hStore == 0 {
		// Fallback to CurrentUser
		isLocalMachine = false
		hStore, _, err = procCertOpenStore.Call(
			uintptr(CERT_STORE_PROV_SYSTEM_W),
			0,
			0,
			uintptr(CERT_SYSTEM_STORE_CURRENT_USER),
			uintptr(unsafe.Pointer(storeName)),
		)
		if hStore == 0 {
			return fmt.Errorf("failed to open ROOT certificate store: %w", err)
		}
	}
	defer func() {
		_, _, _ = procCertCloseStore.Call(hStore, 0)
	}()

	installedCount := 0
	remaining := pemData
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
			return fmt.Errorf("parse certificate: %w", err)
		}

		r1, _, err := procCertAddEncodedCertificateToStore.Call(
			hStore,
			uintptr(CERT_ENCODING_TYPE),
			uintptr(unsafe.Pointer(&block.Bytes[0])),
			uintptr(len(block.Bytes)),
			uintptr(CERT_STORE_ADD_REPLACE_EXISTING),
			0,
		)
		if r1 == 0 {
			return fmt.Errorf("add certificate %q to store: %w", parsed.Subject.CommonName, err)
		}

		scope := "CurrentUser"
		if isLocalMachine {
			scope = "LocalMachine"
		}
		fmt.Printf("Installed CA certificate to %s\\ROOT: %s (SHA256: %s)\n",
			scope, parsed.Subject.CommonName, formatFingerprint(parsed))
		installedCount++
	}

	if installedCount == 0 {
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
