package scheduler

import (
	"errors"
	"net/http"
	"testing"

	"design-cloud-engine/internal/contract"
)

func assertInvalidParam(t *testing.T, err error, input string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error for %q", input)
	}
	de, ok := errors.AsType[contract.DomainError](err)
	if !ok || de.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("expected 400 InvalidParam for %q, got %v", input, err)
	}
}

func TestValidateJobID(t *testing.T) {
	for _, bad := range []string{"0", "00", " 1", "1 ", "-1", "+1", "1a", "123[1]", "123_1", "1_0", "", "18446744073709551616"} {
		assertInvalidParam(t, ValidateJobID(bad), bad)
	}
	for _, good := range []string{"1", "123456", "007"} {
		if err := ValidateJobID(good); err != nil {
			t.Fatalf("ValidateJobID(%q) = %v", good, err)
		}
	}
}

func TestValidateArg(t *testing.T) {
	for _, bad := range []string{"", "  ", "-q", " -x"} {
		assertInvalidParam(t, validateArg("queue", bad), bad)
	}
	for _, good := range []string{"normal", "my-job", "a"} {
		if err := validateArg("queue", good); err != nil {
			t.Fatalf("validateArg(%q) = %v", good, err)
		}
	}
}
