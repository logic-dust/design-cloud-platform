package httputil

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"design-cloud-engine/internal/contract"
)

// Standard-library filesystem sentinels are not treated as domain errors.
// Only contract errors receive a status-coded public message; everything else is 500.
func TestClassifyError_StandardLibrarySentinelsAreNotClassified(t *testing.T) {
	t.Parallel()
	sentinels := map[string]error{
		"fs.ErrInvalid":    fs.ErrInvalid,
		"fs.ErrNotExist":   fs.ErrNotExist,
		"fs.ErrPermission": fs.ErrPermission,
	}
	for name, err := range sentinels {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status, msg := ClassifyError(err)
			if status != http.StatusInternalServerError {
				t.Errorf("%s: expected status 500, got %d", name, status)
			}
			if msg != "internal server error" {
				t.Errorf("%s: expected generic message, got %q", name, msg)
			}
		})
	}
}

func TestClassifyError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		err          error
		wantStatus   int
		wantContains string
		wantExact    bool
	}{
		{
			name:         "contract.ErrNotFound → 404",
			err:          contract.ErrNotFound,
			wantStatus:   http.StatusNotFound,
			wantContains: contract.ErrNotFound.Error(),
		},
		{
			name:         "contract.NewNotFoundError → 404",
			err:          contract.NewNotFoundError("file missing"),
			wantStatus:   http.StatusNotFound,
			wantContains: "file missing",
		},
		{
			name:         "contract.NewForbiddenError → 403",
			err:          contract.NewForbiddenError("read-only"),
			wantStatus:   http.StatusForbidden,
			wantContains: "read-only",
		},
		{
			name:         "contract.NewValidationError → 422",
			err:          contract.NewValidationError("name is reserved"),
			wantStatus:   http.StatusUnprocessableEntity,
			wantContains: "name is reserved",
		},
		{
			name:         "contract.NewValidationError exact message → 422",
			err:          contract.NewValidationError("invalid trash name"),
			wantStatus:   http.StatusUnprocessableEntity,
			wantContains: "invalid trash name",
			wantExact:    true,
		},
		{
			name:       "unknown error → 500 + safe message",
			err:        errors.New("raw db driver error: connection refused"),
			wantStatus: http.StatusInternalServerError,
			// should NOT contain the raw error
			wantContains: "internal server error",
		},
		{
			name:         "wrapped ErrNotFound → 404",
			err:          errors.New("get user 42: " + contract.ErrNotFound.Error()),
			wantStatus:   http.StatusInternalServerError,
			wantContains: "internal server error",
		},
		{
			name:         "wrapped via %w contract.ErrNotFound → 404",
			err:          fmt.Errorf("%s: %w", "record not found", contract.ErrNotFound),
			wantStatus:   http.StatusNotFound,
			wantContains: "record not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			statusCode, publicMessage := ClassifyError(tt.err)

			if statusCode != tt.wantStatus {
				t.Errorf("statusCode = %d, want %d", statusCode, tt.wantStatus)
			}
			if tt.wantExact {
				if publicMessage != tt.wantContains {
					t.Errorf("publicMessage = %q, want %q", publicMessage, tt.wantContains)
				}
				return
			}
			if tt.wantContains != "" && !strings.Contains(publicMessage, tt.wantContains) {
				t.Errorf("publicMessage = %q, want to contain %q", publicMessage, tt.wantContains)
			}
		})
	}
}
