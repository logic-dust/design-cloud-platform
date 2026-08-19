package portal

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
)

func TestHTTPRequestIDPrefersResponseHeader(t *testing.T) {
	t.Parallel()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderXRequestID, "client-request")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Response().Header().Set(echo.HeaderXRequestID, "server-request")

	if got := httputil.RequestID(c); got != "server-request" {
		t.Fatalf("http request id = %q, want response header value", got)
	}
}

func TestHTTPRequestIDFallsBackToRequestHeader(t *testing.T) {
	t.Parallel()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderXRequestID, "client-request")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if got := httputil.RequestID(c); got != "client-request" {
		t.Fatalf("http request id = %q, want request header value", got)
	}
}

func TestSanitizeURI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"/api/v1/users", "/api/v1/users"},
		{"/api/v1/dashboard/events?token=secret123", "/api/v1/dashboard/events?token=***"},
		{"/api/v1/login?password=mysecret&user=alice", "/api/v1/login?password=***&user=alice"},
		{"/api/v1/auth?otp=987654&foo=bar", "/api/v1/auth?foo=bar&otp=***"},
	}

	for _, tt := range tests {
		got := sanitizeURI(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeURI(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
