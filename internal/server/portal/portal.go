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
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/logger"
	"design-cloud-engine/internal/server/portal/api"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// CreateEchoServer creates and configures the Echo server with all middleware and routes.
// The second return value is an io.Closer for the access log writer; callers must
// call Close() when the server is shut down to flush and release file handles.
// The closer may be nil if the log path is empty (logs go to stdout).
func CreateEchoServer(log *slog.Logger, cfg *config.Config, serviceReg *service.Container) (*echo.Echo, io.Closer, error) {
	e := echo.New()

	// 若 Portal 前端有反向代理（Nginx / HAProxy / 云 LB），则从 X-Forwarded-For 头获取
	if cfg.Portal.BehindReverseProxy {
		e.IPExtractor = echo.ExtractIPFromXFFHeader(echo.TrustLoopback(true))
	}

	requestLogger, logCloser, err := createRequestLogger(&cfg.Log, cfg.AccessLogPath(), cfg.Runtime.NodeID)
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

	// 初始化服务管理器实例（依赖注入的根）
	appLogger := log.With("component", "portal_server")
	// 嵌入式 Web 静态页面（主页 + Dashboard 大屏）——须在 API 路由前注册
	RegisterWebRoutes(e, serviceReg.Settings)
	api.NewHandler(serviceReg, cfg, appLogger).InitRouter(e)

	return e, logCloser, nil
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

	if ready != nil {
		// ready 表示“监听 socket 已经绑定成功”，并不代表已经处理了第一个请求。
		close(ready)
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if portalCfg.TLS {
		cert, err := tls.LoadX509KeyPair(portalCfg.Cert, portalCfg.Key)
		if err != nil {
			return fmt.Errorf("load https key pair: %w", err)
		}
		tlsListener := tls.NewListener(listener, &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		})
		// 从这里开始 listener 的生命周期由 http.Server.Serve 管理。
		listenerOwned = false
		return srv.Serve(tlsListener)
	}
	// 非 TLS 模式下同样交由 Serve 接管 listener 生命周期。
	listenerOwned = false
	return srv.Serve(listener)
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

// createRequestLogger 创建用于 Portal API 请求的日志记录器
// 返回的 io.Closer 用于在服务器关闭时关闭底层文件句柄；当 logPath 为空时为 nil。
func createRequestLogger(logCfg *config.Log, logPath, nodeID string) (*slog.Logger, io.Closer, error) {
	rotateCfg := logger.RotateConfig{
		MaxSize:     logCfg.MaxSize,
		MaxAge:      logCfg.MaxAge,
		MaxBackups:  logCfg.MaxBackups,
		Compression: "zstd",
	}
	logWriter, err := logger.NewLogWriter(logPath, &rotateCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("create log writer error: %w", err)
	}

	var logCloser io.Closer
	if c, ok := logWriter.(io.Closer); ok {
		logCloser = c
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
	})), nodeID), logCloser, nil
}
