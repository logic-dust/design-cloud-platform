package httputil

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"

	"github.com/labstack/echo/v5"
)

// SuccessResponse is the sole JSON envelope for all non-empty 2xx Portal API responses.
// Data intentionally has no omitempty/omitzero tag so "data" is never omitted.
type SuccessResponse struct {
	Data any `json:"data"`
}

// PageData is the standard paginated payload inside SuccessResponse.Data.
type PageData struct {
	Items any `json:"items"`
	Total any `json:"total"`
}

// ErrorResponse is the sole JSON envelope for all 4xx/5xx Portal API error responses.
type ErrorResponse struct {
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// RespondOK writes a 200 OK response wrapped in {"data": data}.
func RespondOK(c *echo.Context, data any) error {
	return c.JSON(http.StatusOK, SuccessResponse{Data: data})
}

// RespondCreated writes a 201 Created response wrapped in {"data": data}.
func RespondCreated(c *echo.Context, data any) error {
	return c.JSON(http.StatusCreated, SuccessResponse{Data: data})
}

// RespondAccepted writes a 202 Accepted response wrapped in {"data": data}.
func RespondAccepted(c *echo.Context, data any) error {
	return c.JSON(http.StatusAccepted, SuccessResponse{Data: data})
}

// RespondPage writes a standard paginated envelope: {"data": {"items": items, "total": total}}.
func RespondPage(c *echo.Context, items any, total any) error {
	return c.JSON(http.StatusOK, SuccessResponse{
		Data: PageData{
			Items: items,
			Total: total,
		},
	})
}

// RespondNoContent writes an empty 204 No Content response.
func RespondNoContent(c *echo.Context) error {
	return c.NoContent(http.StatusNoContent)
}

// RespondError is the single entry point for mapping Go errors to HTTP error responses.
// For every error path through the API it:
//  1. classifies the error into an HTTP status code, public message, and optional details
//  2. logs the full error with request context (request_id / method / path / status)
//  3. returns an ErrorResponse{} envelope to the caller
func RespondError(c *echo.Context, err error) error {
	statusCode, publicMessage, details := ClassifyError(err)
	return writeErrorResponse(c, statusCode, publicMessage, details, err)
}

// RespondHTTPError writes a structured error response for an explicit HTTP status code and public message.
func RespondHTTPError(c *echo.Context, statusCode int, message string) error {
	return RespondError(c, echo.NewHTTPError(statusCode, message))
}

// RespondErrorDetails writes an error response with structured details.
func RespondErrorDetails(c *echo.Context, statusCode int, message string, details map[string]any) error {
	return writeErrorResponse(c, statusCode, message, details, errors.New(message))
}

func writeErrorResponse(c *echo.Context, statusCode int, publicMessage string, details map[string]any, rawErr error) error {
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
		slog.Any("error", rawErr),
	)

	if c.Request().Method == http.MethodHead {
		return c.NoContent(statusCode)
	}
	return c.JSON(statusCode, ErrorResponse{
		Message: publicMessage,
		Details: details,
	})
}

// HTTPErrorHandler is the centralized Echo error handler that delegates uncommitted
// errors (from Echo router, built-in middlewares, or handlers returning error directly)
// to RespondError so all error responses share one envelope and structured log path.
func HTTPErrorHandler(c *echo.Context, err error) {
	if err == nil {
		return
	}
	if r, _ := echo.UnwrapResponse(c.Response()); r != nil && r.Committed {
		return
	}
	_ = RespondError(c, err)
}

// ClassifyError determines the HTTP status code, public message, and optional structured
// details for an error. Only domain/contract/HTTP errors receive 4xx/5xx status-coded
// responses; everything else becomes 500 "internal server error".
func ClassifyError(err error) (statusCode int, publicMessage string, details map[string]any) {
	if err == nil {
		return http.StatusOK, "", nil
	}

	if domainErr, ok := errors.AsType[contract.DomainError](err); ok {
		var det map[string]any
		if de, ok := errors.AsType[contract.DetailsError](err); ok {
			det = de.Details()
		}
		return domainErr.HTTPStatus(), domainErr.Message(), det
	}

	if httpErr, ok := errors.AsType[*echo.HTTPError](err); ok {
		return httpErr.Code, fmt.Sprint(httpErr.Message), nil
	}

	if codeStatus := echo.StatusCode(err); codeStatus != 0 {
		return codeStatus, http.StatusText(codeStatus), nil
	}

	switch {
	case errors.Is(err, contract.ErrNotFound):
		return http.StatusNotFound, err.Error(), nil
	case errors.Is(err, schema.ErrMalformed):
		return http.StatusBadRequest, err.Error(), nil
	default:
		slog.Warn("unclassified_error=true", slog.String("error", err.Error()), slog.String("type", fmt.Sprintf("%T", err)))
		return http.StatusInternalServerError, "internal server error", nil
	}
}

// InitSSEResponse writes the shared SSE response headers and performs the
// initial flush so the client receives the handshake immediately. When retrySec
// is positive it also writes a `retry: NNNN` reconnect hint (SSE spec) before
// flushing. Returns the flusher for writing subsequent frames.
func InitSSEResponse(w http.ResponseWriter, retrySec int) (http.Flusher, error) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if retrySec > 0 {
		_, _ = fmt.Fprintf(w, "retry: %d\n\n", retrySec*1000)
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}
	flusher.Flush()
	return flusher, nil
}
