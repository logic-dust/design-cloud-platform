package schema

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"net/http"
	"testing"

	"design-cloud-engine/internal/contract"
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
	if _, err := DecodeForStore(DomainCert, raw); err == nil {
		t.Fatalf("invalid autoRenewMode must fail, got nil error")
	} else {
		var contractErr contract.DomainError
		if !errors.As(err, &contractErr) {
			t.Fatalf("expected contract.DomainError, got %T: %v", err, err)
		}
		if contractErr.HTTPStatus() != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422, got %d: %v", contractErr.HTTPStatus(), err)
		}
	}

	emptyBackoff := valid
	emptyBackoff.BackoffScheduleHours = nil
	raw, _ = json.Marshal(emptyBackoff)
	if _, err := DecodeForStore(DomainCert, raw); err == nil {
		t.Fatal("empty backoff schedule must be rejected")
	}

	belowMinBackoff := valid
	belowMinBackoff.BackoffScheduleHours = []int{0}
	raw, _ = json.Marshal(belowMinBackoff)
	if _, err := DecodeForStore(DomainCert, raw); err == nil {
		t.Fatal("backoffScheduleHours element 0 must be rejected (min=1)")
	}

	aboveMaxBackoff := valid
	aboveMaxBackoff.BackoffScheduleHours = []int{169}
	raw, _ = json.Marshal(aboveMaxBackoff)
	if _, err := DecodeForStore(DomainCert, raw); err == nil {
		t.Fatal("backoffScheduleHours element 169 must be rejected (max=168)")
	}

	zeroDays := valid
	zeroDays.RenewBeforeDays = 0
	raw, _ = json.Marshal(zeroDays)
	if _, err := DecodeForStore(DomainCert, raw); err == nil {
		t.Fatal("renewBeforeDays=0 must be rejected")
	}
}
