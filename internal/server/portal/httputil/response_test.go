package httputil

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"design-cloud-engine/internal/contract"

	"github.com/labstack/echo/v5"
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
			status, msg, details := ClassifyError(err)
			if status != http.StatusInternalServerError {
				t.Errorf("%s: expected status 500, got %d", name, status)
			}
			if msg != "internal server error" {
				t.Errorf("%s: expected generic message, got %q", name, msg)
			}
			if details != nil {
				t.Errorf("%s: expected nil details, got %+v", name, details)
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
			name:         "unknown error → 500 + safe message",
			err:          errors.New("raw db driver error: connection refused"),
			wantStatus:   http.StatusInternalServerError,
			wantContains: "internal server error",
		},
		{
			name:         "wrapped ErrNotFound string → 500",
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

			statusCode, publicMessage, _ := ClassifyError(tt.err)

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

func TestClassifyError_EchoHTTPStatusCoder(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"ErrNotFound", echo.ErrNotFound, http.StatusNotFound},
		{"ErrMethodNotAllowed", echo.ErrMethodNotAllowed, http.StatusMethodNotAllowed},
		{"ErrStatusRequestEntityTooLarge", echo.ErrStatusRequestEntityTooLarge, http.StatusRequestEntityTooLarge},
		{"ErrTooManyRequests", echo.ErrTooManyRequests, http.StatusTooManyRequests},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, msg, _ := ClassifyError(tc.err)
			if status != tc.wantStatus {
				t.Errorf("status = %d, want %d", status, tc.wantStatus)
			}
			if msg == "" {
				t.Errorf("expected non-empty public message")
			}
		})
	}
}

func TestHTTPErrorHandler_EnvelopeAndHeadAndCommitted(t *testing.T) {
	t.Parallel()

	e := echo.New()
	e.JSONSerializer = JSONCodecSerializer{}
	e.HTTPErrorHandler = HTTPErrorHandler

	e.GET("/boom", func(c *echo.Context) error {
		return contract.NewValidationError("bad input")
	})
	e.GET("/nil-slice", func(c *echo.Context) error {
		var items []string
		return RespondOK(c, items)
	})
	e.HEAD("/boom", func(c *echo.Context) error {
		return echo.ErrNotFound
	})
	e.GET("/committed", func(c *echo.Context) error {
		if err := c.String(http.StatusCreated, "already sent"); err != nil {
			return err
		}
		return errors.New("late error")
	})

	// GET error -> {"message":"bad input"}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("GET /boom status = %d, want 422", rec.Code)
	}
	if rec.Body.String() != `{"message":"bad input"}` {
		t.Fatalf("GET /boom body = %q", rec.Body.String())
	}

	// GET nil slice -> {"data":[]} (never {} and never null)
	recSlice := httptest.NewRecorder()
	e.ServeHTTP(recSlice, httptest.NewRequest(http.MethodGet, "/nil-slice", nil))
	if recSlice.Code != http.StatusOK {
		t.Fatalf("GET /nil-slice status = %d, want 200", recSlice.Code)
	}
	if recSlice.Body.String() != `{"data":[]}` {
		t.Fatalf("GET /nil-slice body = %q", recSlice.Body.String())
	}

	// HEAD error -> status only, empty body
	recHead := httptest.NewRecorder()
	e.ServeHTTP(recHead, httptest.NewRequest(http.MethodHead, "/boom", nil))
	if recHead.Code != http.StatusNotFound {
		t.Fatalf("HEAD /boom status = %d, want 404", recHead.Code)
	}
	if recHead.Body.Len() != 0 {
		t.Fatalf("HEAD /boom expected empty body, got %q", recHead.Body.String())
	}

	// Already committed -> untouched
	recCommitted := httptest.NewRecorder()
	e.ServeHTTP(recCommitted, httptest.NewRequest(http.MethodGet, "/committed", nil))
	if recCommitted.Code != http.StatusCreated || recCommitted.Body.String() != "already sent" {
		t.Fatalf("committed response mutated: status=%d body=%q", recCommitted.Code, recCommitted.Body.String())
	}
}
