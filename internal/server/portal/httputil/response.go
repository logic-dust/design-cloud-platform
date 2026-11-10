package httputil

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"design-cloud-engine/internal/contract/schema"

	"design-cloud-engine/internal/contract"
	"github.com/labstack/echo/v5"
)

// Response represents the standard JSON API response envelope used by all
// portal handlers (success + error). Data is omitted when empty, so error
// responses serialize as {"message":"..."} and success responses as
// {"data":...}.
type Response struct {
	Data    any            `json:"data,omitempty"`
	Message string         `json:"message,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// RespondPage writes a standard paginated envelope: {"data": {"items": items, "total": total}}.
func RespondPage(c *echo.Context, items any, total any) error {
	return c.JSON(http.StatusOK, Response{
		Data: map[string]any{
			"items": items,
			"total": total,
		},
	})
}

// RespondData writes a standard JSON success response: {"data": data}.
func RespondData(c *echo.Context, data any) error {
	return c.JSON(http.StatusOK, Response{Data: data})
}

// RespondError is the single entry point for mapping errors to HTTP responses.
// For every error path through the API it:
//  1. classifies the error into an HTTP status code + a safe public message
//  2. logs the full error with request context (request_id / method / path / status) so
//     operators can debug, even when the client only receives a generic message
//  3. returns a safe Response{} envelope to the caller
//
// Only domain contract errors (contract.DomainError, contract.ErrNotFound,
// schema.ErrMalformed) and echo.HTTPError get status-coded responses. Everything
// else — including standard-library sentinels such as io/fs errors — becomes 500
// with a WARN log marked unclassified_error=true, so that leaking filesystem
// sentinels through the HTTP layer is detectable in production.
func RespondError(c *echo.Context, err error) error {
	statusCode, publicMessage := ClassifyError(err)

	// Server-side log — always record the full error so operators can debug.
	// 4xx errors are warnings (client mistakes); 5xx are errors (server faults).
	level := slog.LevelError
	if statusCode >= 400 && statusCode < 500 {
		level = slog.LevelWarn
	}
	slog.LogAttrs(
		c.Request().Context(),
		level,
		"api error response",
		slog.String("request_id", RequestID(c)),
		slog.String("method", c.Request().Method),
		slog.String("path", c.Request().URL.Path),
		slog.Int("http_status", statusCode),
		slog.String("client_message", publicMessage),
		slog.Any("error", err),
	)

	return c.JSON(statusCode, Response{Message: publicMessage})
}

// ClassifyError determines the HTTP status code and public message for an error.
// Only domain contract errors get status-coded responses; everything else
// becomes 500 "internal server error" so that no driver-internal detail leaks
// to the client. Unclassified errors are marked unclassified_error=true in a
// WARN log so operators can detect fs.Err* leaking through the HTTP layer.
func ClassifyError(err error) (statusCode int, publicMessage string) {
	if err == nil {
		return http.StatusOK, ""
	}

	if domainErr, ok := errors.AsType[contract.DomainError](err); ok {
		return domainErr.HTTPStatus(), domainErr.Message()
	}

	if httpErr, ok := errors.AsType[*echo.HTTPError](err); ok {
		// httpErr.Message 是 any（string 或 error 包装），fmt.Sprint 统一转字符串。
		return httpErr.Code, fmt.Sprint(httpErr.Message)
	}

	switch {
	case errors.Is(err, contract.ErrNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, schema.ErrMalformed):
		// JSON 语法/结构错误 → 400（语法层）。
		return http.StatusBadRequest, err.Error()
	default:
		slog.Warn("unclassified_error=true", slog.String("error", err.Error()), slog.String("type", fmt.Sprintf("%T", err)))
		return http.StatusInternalServerError, "internal server error"
	}
}
