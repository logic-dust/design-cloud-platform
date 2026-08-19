package schema

import (
	"encoding/json"
	"errors"
	"io/fs"
	"testing"
)

func TestDefaultCertSettings(t *testing.T) {
	v, ok := DefaultSettings(DomainCert)
	if !ok {
		t.Fatal("DomainCert must have defaults")
	}
	cert, ok := v.(CertSettings)
	if !ok {
		t.Fatalf("unexpected type %T", v)
	}
	if cert.AutoRenewMode != CertAutoRenewApply {
		t.Fatalf("autoRenewMode = %q, want apply", cert.AutoRenewMode)
	}
	if cert.RenewBeforeDays != 30 || cert.MaxAttempts != 5 {
		t.Fatalf("thresholds drifted: %+v", cert)
	}
	if len(cert.BackoffScheduleHours) != 3 || cert.BackoffScheduleHours[0] != 1 {
		t.Fatalf("backoff = %v, want [1 6 24]", cert.BackoffScheduleHours)
	}
}

func TestCertDomainRegistration(t *testing.T) {
	if _, ok := ParseDomain("cert"); !ok {
		t.Fatal("ParseDomain(cert) must succeed")
	}
	found := false
	for _, d := range AllDomains() {
		if d == DomainCert {
			found = true
		}
	}
	if !found {
		t.Fatal("DomainCert must be part of AllDomains")
	}
}

func TestDecodeCertSettings(t *testing.T) {
	valid := CertSettings{
		AutoRenewMode:        CertAutoRenewObserve,
		RenewBeforeDays:      45,
		MaxAttempts:          3,
		BackoffScheduleHours: []int{2, 12},
	}
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	decoded, err := DecodeForStore(DomainCert, raw)
	if err != nil {
		t.Fatalf("DecodeForStore(valid): %v", err)
	}
	if got := decoded.(CertSettings); got.AutoRenewMode != CertAutoRenewObserve || got.RenewBeforeDays != 45 {
		t.Fatalf("decoded = %+v", got)
	}

	invalidMode := valid
	invalidMode.AutoRenewMode = "sometimes"
	raw, _ = json.Marshal(invalidMode)
	if _, err := DecodeForStore(DomainCert, raw); err == nil || !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("invalid autoRenewMode must fail with fs.ErrInvalid, got %v", err)
	}

	emptyBackoff := valid
	emptyBackoff.BackoffScheduleHours = nil
	raw, _ = json.Marshal(emptyBackoff)
	if _, err := DecodeForStore(DomainCert, raw); err == nil {
		t.Fatal("empty backoff schedule must be rejected")
	}

	zeroDays := valid
	zeroDays.RenewBeforeDays = 0
	raw, _ = json.Marshal(zeroDays)
	if _, err := DecodeForStore(DomainCert, raw); err == nil {
		t.Fatal("renewBeforeDays=0 must be rejected")
	}
}
