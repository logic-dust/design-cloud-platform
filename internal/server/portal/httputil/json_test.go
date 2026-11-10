package httputil

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

func TestJSONCodecSerializer_SerializeContract(t *testing.T) {
	t.Parallel()

	e := echo.New()
	e.JSONSerializer = JSONCodecSerializer{}

	type payload struct {
		Items []string          `json:"items"`
		Meta  map[string]string `json:"meta"`
	}

	e.GET("/nil", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, payload{})
	})
	e.GET("/pretty", func(c *echo.Context) error {
		return c.JSONPretty(http.StatusOK, map[string]int{"b": 2, "a": 1}, "  ")
	})

	req := httptest.NewRequest(http.MethodGet, "/nil", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got, want := rec.Body.String(), `{"items":[],"meta":{}}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}

	reqPretty := httptest.NewRequest(http.MethodGet, "/pretty", nil)
	recPretty := httptest.NewRecorder()
	e.ServeHTTP(recPretty, reqPretty)
	if recPretty.Code != http.StatusOK {
		t.Fatalf("pretty status = %d, want 200", recPretty.Code)
	}
	if !strings.Contains(recPretty.Body.String(), "\n  \"a\": 1") {
		t.Fatalf("expected deterministic indented output, got %q", recPretty.Body.String())
	}
}

func TestJSONCodecSerializer_DeserializeAndBodyLimit(t *testing.T) {
	t.Parallel()

	e := echo.New()
	e.JSONSerializer = JSONCodecSerializer{}
	e.HTTPErrorHandler = HTTPErrorHandler
	e.Use(middleware.BodyLimit(16))

	e.POST("/bind", func(c *echo.Context) error {
		var req struct {
			Name string `json:"name"`
		}
		if err := c.Bind(&req); err != nil {
			return err
		}
		return c.JSON(http.StatusOK, req)
	})

	// Valid JSON
	reqOK := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(`{"name":"ok"}`))
	reqOK.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recOK := httptest.NewRecorder()
	e.ServeHTTP(recOK, reqOK)
	if recOK.Code != http.StatusOK || recOK.Body.String() != `{"name":"ok"}` {
		t.Fatalf("valid bind: status=%d body=%q", recOK.Code, recOK.Body.String())
	}

	// Malformed JSON -> 400
	reqBad := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(`{bad`))
	reqBad.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recBad := httptest.NewRecorder()
	e.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("malformed json: status=%d, want 400; body=%q", recBad.Code, recBad.Body.String())
	}

	// Oversized body -> 413 (not downgraded to 400)
	reqBig := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(`{"name":"exceeds-sixteen-bytes"}`))
	reqBig.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recBig := httptest.NewRecorder()
	e.ServeHTTP(recBig, reqBig)
	if recBig.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: status=%d, want 413; body=%q", recBig.Code, recBig.Body.String())
	}
}
