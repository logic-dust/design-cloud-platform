package common

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
)

func TestDomainErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantHTTP   int
		wantGRPC   codes.Code
		wantMsg    string
		wantSubErr bool
	}{
		{
			name:       "NotFound",
			err:        NewNotFoundError("user not found"),
			wantHTTP:   http.StatusNotFound,
			wantGRPC:   codes.NotFound,
			wantMsg:    "user not found",
			wantSubErr: false,
		},
		{
			name:       "NotFoundWithCause",
			err:        NewNotFoundError("user not found", fmt.Errorf("id=123")),
			wantHTTP:   http.StatusNotFound,
			wantGRPC:   codes.NotFound,
			wantMsg:    "user not found",
			wantSubErr: true,
		},
		{
			name:       "InvalidParam",
			err:        NewInvalidParamError("invalid port number"),
			wantHTTP:   http.StatusBadRequest,
			wantGRPC:   codes.InvalidArgument,
			wantMsg:    "invalid port number",
			wantSubErr: false,
		},
		{
			name:       "Unauthorized",
			err:        NewUnauthorizedError("unauthorized request"),
			wantHTTP:   http.StatusUnauthorized,
			wantGRPC:   codes.Unauthenticated,
			wantMsg:    "unauthorized request",
			wantSubErr: false,
		},
		{
			name:       "Forbidden",
			err:        NewForbiddenError("access denied"),
			wantHTTP:   http.StatusForbidden,
			wantGRPC:   codes.PermissionDenied,
			wantMsg:    "access denied",
			wantSubErr: false,
		},
		{
			name:       "QuotaExceeded",
			err:        NewQuotaExceededError("max sessions reached"),
			wantHTTP:   http.StatusTooManyRequests,
			wantGRPC:   codes.ResourceExhausted,
			wantMsg:    "max sessions reached",
			wantSubErr: false,
		},
		{
			name:       "Conflict",
			err:        NewConflictError("already exists"),
			wantHTTP:   http.StatusConflict,
			wantGRPC:   codes.AlreadyExists,
			wantMsg:    "already exists",
			wantSubErr: false,
		},
		{
			name:       "Validation",
			err:        NewValidationError("validation failed"),
			wantHTTP:   http.StatusUnprocessableEntity,
			wantGRPC:   codes.InvalidArgument,
			wantMsg:    "validation failed",
			wantSubErr: false,
		},
		{
			name:       "Unavailable",
			err:        NewUnavailableError("node offline"),
			wantHTTP:   http.StatusServiceUnavailable,
			wantGRPC:   codes.Unavailable,
			wantMsg:    "node offline",
			wantSubErr: false,
		},
		{
			name:       "Timeout",
			err:        NewTimeoutError("request timed out"),
			wantHTTP:   http.StatusGatewayTimeout,
			wantGRPC:   codes.DeadlineExceeded,
			wantMsg:    "request timed out",
			wantSubErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var de DomainError
			if !errors.As(tc.err, &de) {
				t.Fatalf("expected error to implement DomainError: %T", tc.err)
			}
			if got := de.HTTPStatus(); got != tc.wantHTTP {
				t.Errorf("HTTPStatus() = %d, want %d", got, tc.wantHTTP)
			}
			if got := de.GRPCCode(); got != tc.wantGRPC {
				t.Errorf("GRPCCode() = %v, want %v", got, tc.wantGRPC)
			}
			if got := de.Message(); got != tc.wantMsg {
				t.Errorf("Message() = %q, want %q", got, tc.wantMsg)
			}
			if tc.wantSubErr && de.Unwrap() == nil {
				t.Errorf("expected non-nil Unwrap() cause")
			}
		})
	}
}

func TestDomainError_IsSentinel(t *testing.T) {
	err := NewNotFoundError("missing resource")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected NewNotFoundError to match errors.Is(ErrNotFound)")
	}
}
