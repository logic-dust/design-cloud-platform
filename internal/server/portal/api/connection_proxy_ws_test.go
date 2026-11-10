package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/proxy"
	"design-cloud-engine/internal/server/service/settings"

	"github.com/coder/websocket"
	"github.com/labstack/echo/v5"
)

func TestHandleConnectionProxyWS_EchoRoundTrip(t *testing.T) {
	t.Parallel()

	proxySvc, connRepo, nodeRepo, _, cleanup := newConnProxyTestStack(t)
	defer cleanup()

	echoLn, echoAddr := startTCPEchoServer(t)
	defer echoLn.Close()

	loopbackIP := echoAddr.IP.String()
	if err := nodeRepo.Create(context.Background(), &model.Node{
		NodeID:           "echo-node",
		DialHost:         loopbackIP,
		AllowConnections: true,
		Status:           model.NodeStatusOnline,
	}); err != nil {
		t.Fatalf("create node: %v", err)
	}
	conn := model.Connection{
		Username: "hpcadmin",
		NodeID:   "echo-node",
		Port:     uint(echoAddr.Port),
		Status:   model.ConnectionRunning,
		Protocol: contract.VNCProtocol,
	}
	if err := connRepo.Create(context.Background(), &conn); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	handler := newConnProxyHandler(proxySvc, nil, slog.Default(), "")
	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Set(httputil.CtxSessionKey, &model.Session{Username: "hpcadmin"})
			return next(c)
		}
	})
	e.GET("/api/connections/:id/proxy-connection", handler.handleConnectionProxyConnection)

	server := httptest.NewServer(e)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/connections/" + strconv.FormatUint(uint64(conn.ID), 10) + "/proxy-connection"
	wsConn, resp, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Upgrade": []string{"websocket"},
		},
	})
	if err != nil {
		t.Fatalf("dial ws: %v (http=%v)", err, resp)
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "")

	payload := []byte("ping-proxy")
	if err := wsConn.Write(context.Background(), websocket.MessageBinary, payload); err != nil {
		t.Fatalf("write ws: %v", err)
	}
	msgType, data, err := wsConn.Read(context.Background())
	if err != nil {
		t.Fatalf("read ws: %v", err)
	}
	if msgType != websocket.MessageBinary || string(data) != "ping-proxy" {
		t.Fatalf("unexpected echo: type=%v data=%q", msgType, data)
	}
}

func TestHandleConnectionProxyWS_ForbiddenOriginBeforeDial(t *testing.T) {
	t.Parallel()

	proxySvc, connRepo, nodeRepo, _, cleanup := newConnProxyTestStack(t)
	defer cleanup()

	if err := nodeRepo.Create(context.Background(), &model.Node{
		NodeID:           "node-1",
		DialHost:         "10.0.0.1",
		AllowConnections: true,
		Status:           model.NodeStatusOnline,
	}); err != nil {
		t.Fatalf("create node: %v", err)
	}
	conn := model.Connection{
		Username: "hpcadmin",
		NodeID:   "node-1",
		Port:     5901,
		Status:   model.ConnectionRunning,
		Protocol: contract.VNCProtocol,
	}
	if err := connRepo.Create(context.Background(), &conn); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	handler := newConnProxyHandler(proxySvc, nil, slog.Default(), "portal.example.com")
	e := echo.New()
	e.GET("/api/connections/:id/proxy-connection", handler.handleConnectionProxyConnection)

	req := httptest.NewRequest(http.MethodGet, "/api/connections/"+strconv.FormatUint(uint64(conn.ID), 10)+"/proxy-connection", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Host", "portal.example.com")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/connections/:id/proxy-connection")
	c.SetPathValues(echo.PathValues{{Name: "id", Value: strconv.FormatUint(uint64(conn.ID), 10)}})
	c.Set(httputil.CtxSessionKey, &model.Session{Username: "hpcadmin"})

	if err := handler.handleConnectionProxyConnection(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}

	if _, err := proxySvc.GetActiveByConnectionIDAndRequester(conn.ID, "hpcadmin"); err == nil {
		t.Fatal("expected no active session after origin rejection")
	}
}

func newConnProxyTestStack(t *testing.T) (*proxy.Service, *db.ConnectionRepo, *db.NodeRepo, *settings.Service, func()) {
	t.Helper()
	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := settings.EnsureDefaults(store); err != nil {
		t.Fatalf("seed defaults: %v", err)
	}

	repos := db.NewRepos(store)
	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  filepath.Join(t.TempDir(), "audit.log"),
		SecurityLogPath: filepath.Join(t.TempDir(), "security.log"),
		NodeID:          "test-node",
	})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}

	settingsSvc, err := settings.NewService(repos.Setting, slog.Default(), auditSvc, nil, repos.CacheVersion, repos.Node)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	proxySvc, err := proxy.NewService(settingsSvc, repos.Connection, repos.Node, slog.Default())
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}
	cleanup := func() {
		_ = auditSvc.Close()
		_ = store.Close()
	}
	return proxySvc, repos.Connection, repos.Node, settingsSvc, cleanup
}

func startTCPEchoServer(t *testing.T) (*net.TCPListener, *net.TCPAddr) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	tcpLn := ln.(*net.TCPListener)
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					n, readErr := c.Read(buf)
					if n > 0 {
						_, _ = c.Write(buf[:n])
					}
					if readErr != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return tcpLn, tcpLn.Addr().(*net.TCPAddr)
}

type stubSSHDialAPI struct {
	host string
	port uint
}

func (s stubSSHDialAPI) ResolveSSHDialTarget(ctx context.Context, username string, resourceID uint) (string, uint, error) {
	return s.host, s.port, nil
}

func TestHandleSSHProxyWS_EchoRoundTrip(t *testing.T) {
	t.Parallel()

	proxySvc, _, _, _, cleanup := newConnProxyTestStack(t)
	defer cleanup()

	echoLn, echoAddr := startTCPEchoServer(t)
	defer echoLn.Close()

	proxySvc.SetSSHDialResolver(stubSSHDialAPI{host: echoAddr.IP.String(), port: uint(echoAddr.Port)})

	handler := newConnProxyHandler(proxySvc, nil, slog.Default(), "")
	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Set(httputil.CtxSessionKey, &model.Session{Username: "hpcadmin"})
			return next(c)
		}
	})
	e.GET("/api/connections/ssh", handler.handleSSHProxy)

	server := httptest.NewServer(e)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/connections/ssh?resourceId=34"
	wsConn, resp, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Upgrade": []string{"websocket"}},
	})
	if err != nil {
		t.Fatalf("dial ws: %v (http=%v)", err, resp)
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "")

	payload := []byte("ssh-proxy")
	if err := wsConn.Write(context.Background(), websocket.MessageBinary, payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	msgType, data, err := wsConn.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msgType != websocket.MessageBinary || string(data) != "ssh-proxy" {
		t.Fatalf("unexpected echo: type=%v data=%q", msgType, data)
	}
}

func TestHandleSSHProxy_RequiresUpgrade(t *testing.T) {
	t.Parallel()

	proxySvc, _, _, _, cleanup := newConnProxyTestStack(t)
	defer cleanup()
	handler := newConnProxyHandler(proxySvc, nil, slog.Default(), "")
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/connections/ssh?resourceId=34", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(httputil.CtxSessionKey, &model.Session{Username: "hpcadmin"})
	if err := handler.handleSSHProxy(c); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
