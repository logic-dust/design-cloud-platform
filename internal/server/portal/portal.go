package portal

import (
	"cmp"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/platform/logger"
	"design-cloud-engine/internal/server/portal/api"
	"design-cloud-engine/internal/server/portal/httputil"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// CreateEchoServer creates and configures the Echo server with all middleware and routes.
// The second return value is an io.Closer for the access log writer; callers must
// call Close() when the server is shut down to flush and release file handles.
// The closer may be nil if the log path is empty (logs go to stdout).
func CreateEchoServer(log *slog.Logger, cfg *config.Config, deps api.Dependencies) (*echo.Echo, io.Closer, error) {
	e := echo.New()

	// 若 Portal 前端有反向代理（Nginx / HAProxy / 云 LB），则从 X-Forwarded-For 头获取
	extractor, err := newIPExtractor(&cfg.Portal)
	if err != nil {
		return nil, nil, err
	}
	if extractor != nil {
		e.IPExtractor = extractor
	}

	requestLogger, logCloser, err := createRequestLogger(&cfg.Log, cfg.Runtime.NodeID)
	if err != nil {
		return nil, nil, err
	}

	// Middleware
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(middleware.RequestLoggerWithConfig(genRequestLoggerConfig(requestLogger)))

	if cfg.Portal.TLS {
		e.Use(middleware.Secure())
	}

	appLogger := log.With("component", "portal_server")
	// 嵌入式 Web 静态页面（主页 + Overview 大屏）——须在 API 路由前注册
	RegisterWebRoutes(e, deps.Settings)
	api.NewHandler(deps, cfg, appLogger).InitRouter(e)

	return e, logCloser, nil
}

// newIPExtractor 构造客户端 IP 提取器。未启用 behind_reverse_proxy 时返回 nil
// （echo 默认直接使用 TCP 远端地址）；启用时仅信任 trusted_proxy_cidr 网段内的代理跳——
// echo 默认信任 loopback/link-local/私网，意味着任意私网来源都能在 X-Forwarded-For
// 前置伪造 IP 绕过 IP 封禁/限流，因此这里显式收窄信任边界。
func newIPExtractor(portalCfg *config.Portal) (echo.IPExtractor, error) {
	if !portalCfg.BehindReverseProxy {
		return nil, nil
	}
	_, proxyNet, err := net.ParseCIDR(portalCfg.TrustedProxyCIDR)
	if err != nil {
		return nil, fmt.Errorf("portal.trusted_proxy_cidr invalid CIDR: %w", err)
	}
	return echo.ExtractIPFromXFFHeader(
		echo.TrustLoopback(false),
		echo.TrustLinkLocal(false),
		echo.TrustPrivateNet(false),
		echo.TrustIPRange(proxyNet),
	), nil
}

// StartServer 启动 Portal 的 HTTP/HTTPS 服务。
// 这里显式调用 net.Listen 的目的有两个：
// 1. 先完成端口绑定，再通过 ready 通知上层“监听已经就绪”
// 2. HTTP 与 WebSocket 共用同一个 TCP listener，WebSocket 只是 HTTP upgrade，不需要单独监听端口
func StartServer(ctx context.Context, e *echo.Echo, portalCfg config.Portal, ready chan<- struct{}) error {
	addr := portalCfg.Host + ":" + portalCfg.Port

	srv := &http.Server{
		Addr:              addr,
		Handler:           e,
		ReadHeaderTimeout: time.Duration(portalCfg.ReadHeaderTimeout) * time.Second,
		ReadTimeout:       time.Duration(portalCfg.ReadTimeout) * time.Second,
		IdleTimeout:       time.Duration(portalCfg.IdleTimeout) * time.Second,
		// WriteTimeout is intentionally omitted: a global write deadline would abort
		// large file downloads mid-transfer. Per-request context timeouts (ContextTimeoutWithConfig)
		// already guard API response latency.
		//
		// ReadTimeout defaults to 0 (disabled) for the same reason: a global read
		// deadline would abort large file uploads mid-transfer. ReadHeaderTimeout
		// protects against slow-header attacks; IdleTimeout guards idle connections.
	}

	// 先绑定底层 TCP 监听，再交给 http.Server.Serve 使用。
	// 这样 manager 可以在“端口已成功监听”这一刻收到 ready 信号，而不是等到 ListenAndServe 内部完成。
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	listenerOwned := true
	defer func() {
		// 如果后续因为加载证书等原因提前返回，Serve 尚未接管 listener，
		// 这里负责兜底关闭，避免启动失败时泄漏底层 fd。
		if listenerOwned {
			_ = listener.Close()
		}
	}()

	var serveListener net.Listener = listener
	if portalCfg.TLS {
		cert, err := tls.LoadX509KeyPair(portalCfg.Cert, portalCfg.Key)
		if err != nil {
			return fmt.Errorf("load https key pair: %w", err)
		}
		serveListener = tls.NewListener(listener, &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		})
	}

	// ready 表示监听器及 TLS 配置均已准备完成；证书加载失败不会提前报告就绪。
	if ready != nil {
		close(ready)
	}

	shutdownDone := make(chan struct{})
	var shutdownOnce sync.Once
	shutdown := func() {
		shutdownOnce.Do(func() {
			go func() {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := srv.Shutdown(shutdownCtx); err != nil {
					// Shutdown's timeout leaves active connections open. Force-close them
					// so the server cannot outlive the owning services during teardown.
					_ = srv.Close()
				}
				close(shutdownDone)
			}()
		})
	}
	go func() {
		<-ctx.Done()
		shutdown()
	}()

	// Serve returns as soon as Shutdown closes the listener, before Shutdown has
	// necessarily drained active requests. Wait here so the owner can safely close
	// services and the database only after HTTP shutdown has completed.
	listenerOwned = false
	serveErr := srv.Serve(serveListener)
	shutdown()
	<-shutdownDone
	return serveErr
}

// genRequestLoggerConfig generates the request logger configuration
func genRequestLoggerConfig(requestLogger *slog.Logger) middleware.RequestLoggerConfig {
	return middleware.RequestLoggerConfig{
		LogStatus: true,
		LogMethod: true,
		LogURI:    true,
		LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
			ip := cmp.Or(c.RealIP(), c.Request().RemoteAddr)

			attrs := []slog.Attr{
				slog.String("request_id", httputil.RequestID(c)),
				slog.String("uri", sanitizeURI(v.URI)),
				slog.Int("status", v.Status),
				slog.String("method", v.Method),
				slog.String("ip", ip),
			}

			level := slog.LevelInfo
			msg := "" // Omit redundant message, as method and uri are already in fields
			if v.Error != nil {
				level = slog.LevelError
			}

			// Slow request detection: > 500ms
			if v.Latency > 500*time.Millisecond {
				attrs = append(attrs, slog.Bool("slow", true))
				if level < slog.LevelWarn {
					level = slog.LevelWarn
				}
			}

			requestLogger.LogAttrs(context.Background(), level, msg, attrs...)
			return nil
		},
	}
}

var sensitiveQueryParams = []string{"token", "password", "secret", "otp"}

func sanitizeURI(rawURI string) string {
	u, err := url.ParseRequestURI(rawURI)
	if err != nil {
		return rawURI
	}
	if u.RawQuery == "" {
		return rawURI
	}
	q := u.Query()
	modified := false
	for _, param := range sensitiveQueryParams {
		if q.Has(param) {
			q.Set(param, "***")
			modified = true
		}
	}
	if !modified {
		return rawURI
	}
	u.RawQuery = strings.ReplaceAll(q.Encode(), "%2A", "*")
	return u.String()
}

// createRequestLogger 创建用于 Portal API 请求的日志记录器（始终写 stdout，由 journald/采集器接管）。
func createRequestLogger(logCfg *config.Log, nodeID string) (*slog.Logger, io.Closer, error) {
	logWriter, err := logger.NewLogWriter("")
	if err != nil {
		return nil, nil, fmt.Errorf("create log writer error: %w", err)
	}

	return logger.WithGlobalAttrs(slog.New(slog.NewJSONHandler(logWriter, &slog.HandlerOptions{
		Level: logCfg.AccessLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Apply default transformations (masking, error stringify)
			a = logger.MaskAttr(groups, a)
			if a.Key == slog.MessageKey && a.Value.String() == "" {
				return slog.Attr{}
			}
			return a
		},
	})), nodeID), nil, nil
}
