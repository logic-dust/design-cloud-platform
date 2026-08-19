package httputil

import (
	"github.com/labstack/echo/v5"
)

// RequestID extracts the X-Request-ID from the response or request headers.
func RequestID(c *echo.Context) string {
	if c == nil {
		return ""
	}
	if id := c.Response().Header().Get(echo.HeaderXRequestID); id != "" {
		return id
	}
	return c.Request().Header.Get(echo.HeaderXRequestID)
}
