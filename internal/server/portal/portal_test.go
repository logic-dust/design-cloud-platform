package portal

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
)

func TestStartServerTLSFailureDoesNotSignalReady(t *testing.T) {
	port := unusedTCPPort(t)
	e := echo.New()
	ready := make(chan struct{})
	err := StartServer(context.Background(), e, config.Portal{
		Host: "127.0.0.1", Port: strconv.Itoa(port), TLS: true,
		Cert: "/missing/portal.crt", Key: "/missing/portal.key",
		ReadHeaderTimeout: 8, ReadTimeout: 60, IdleTimeout: 60,
	}, ready)
	if err == nil {
		t.Fatal("StartServer() error = nil, want TLS certificate loading error")
	}
	select {
	case <-ready:
		t.Fatal("ready was signaled before TLS certificate loading succeeded")
	default:
	}
}

func TestStartServerWaitsForActiveRequestsOnShutdown(t *testing.T) {
	port := unusedTCPPort(t)
	e := echo.New()
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	e.GET("/hold", func(c *echo.Context) error {
		close(requestStarted)
		<-releaseRequest
		return c.NoContent(http.StatusOK)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- StartServer(ctx, e, config.Portal{
			Host: "127.0.0.1", Port: strconv.Itoa(port),
			ReadHeaderTimeout: 8, ReadTimeout: 60, IdleTimeout: 60,
		}, ready)
	}()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not signal listener readiness")
	}

	requestDone := make(chan error, 1)
	go func() {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/hold", port))
		if err == nil {
			_ = resp.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("request handler did not start")
	}

	cancel()
	select {
	case err := <-serverDone:
		t.Fatalf("server returned before active request completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseRequest)
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatalf("HTTP request failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP request did not finish")
	}
	select {
	case err := <-serverDone:
		if err != http.ErrServerClosed {
			t.Fatalf("StartServer() error = %v, want http.ErrServerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StartServer did not return after active request completed")
	}
}

func unusedTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve TCP port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

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

func TestNewIPExtractorDisabled(t *testing.T) {
	t.Parallel()

	extractor, err := newIPExtractor(&config.Portal{BehindReverseProxy: false})
	if err != nil {
		t.Fatalf("newIPExtractor() error = %v", err)
	}
	if extractor != nil {
		t.Fatalf("newIPExtractor() = %v, want nil when not behind reverse proxy", extractor)
	}
}

func TestNewIPExtractorInvalidCIDR(t *testing.T) {
	t.Parallel()

	_, err := newIPExtractor(&config.Portal{BehindReverseProxy: true, TrustedProxyCIDR: "not-a-cidr"})
	if err == nil {
		t.Fatal("newIPExtractor() error = nil, want CIDR parse error")
	}
}

// TestNewIPExtractorXFFTrust 验证加固后的 XFF 提取：仅 trusted_proxy_cidr 网段内的
// 代理跳被信任，私网/环回默认不再可信，直接连接（不可信来源）的 XFF 头被忽略。
func TestNewIPExtractorXFFTrust(t *testing.T) {
	t.Parallel()

	extractor, err := newIPExtractor(&config.Portal{BehindReverseProxy: true, TrustedProxyCIDR: "10.0.0.0/8"})
	if err != nil {
		t.Fatalf("newIPExtractor() error = %v", err)
	}

	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"via trusted proxy", "10.0.0.1:1234", "203.0.113.9", "203.0.113.9"},
		{"multi-hop trusted chain", "10.0.0.1:1234", "10.0.0.2, 203.0.113.9", "203.0.113.9"},
		// 攻击者在 XFF 前置伪造私网 IP：默认 echo 配置会信任私网返回伪造值，加固后返回真实客户端
		{"spoofed private prefix ignored", "10.0.0.1:1234", "192.168.99.99, 203.0.113.9", "203.0.113.9"},
		// 绕过代理直接连接：来源不可信，XFF 整体忽略，取 TCP 远端地址
		{"direct untrusted connection ignores xff", "198.51.100.7:1234", "203.0.113.9", "198.51.100.7"},
		// 环回同样不再默认可信：本地直连不读 XFF
		{"loopback direct ignores xff", "127.0.0.1:1234", "203.0.113.9", "127.0.0.1"},
		{"no xff falls back to remote addr", "10.0.0.1:1234", "", "10.0.0.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set(echo.HeaderXForwardedFor, tt.xff)
			}
			if got := extractor(req); got != tt.want {
				t.Errorf("extractor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeURI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"/api/v1/users", "/api/v1/users"},
		{"/api/overview/cluster/stream?token=secret123", "/api/overview/cluster/stream?token=***"},
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
