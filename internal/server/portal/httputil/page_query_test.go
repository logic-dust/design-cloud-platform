package httputil

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestParsePageQuery_modes(t *testing.T) {
	t.Parallel()
	e := echo.New()

	t.Run("clamp", func(t *testing.T) {
		c := e.NewContext(httptest.NewRequest(http.MethodGet, "/?limit=5000&offset=-3", nil), httptest.NewRecorder())
		p, err := ParsePageQuery(c, DefaultPageQueryOpts(PageQueryClamp))
		if err != nil || p.Limit != MaxPageLimit || p.Offset != 0 {
			t.Fatalf("clamp: p=%+v err=%v", p, err)
		}
	})

	t.Run("strict422", func(t *testing.T) {
		c := e.NewContext(httptest.NewRequest(http.MethodGet, "/?limit=0", nil), httptest.NewRecorder())
		_, err := ParsePageQuery(c, DefaultPageQueryOpts(PageQueryStrict422))
		he, ok := err.(*echo.HTTPError)
		if !ok || he.Code != http.StatusUnprocessableEntity || he.Message != "invalid limit" {
			t.Fatalf("strict limit: %v", err)
		}
		c2 := e.NewContext(httptest.NewRequest(http.MethodGet, "/?offset=20000", nil), httptest.NewRecorder())
		_, err = ParsePageQuery(c2, DefaultPageQueryOpts(PageQueryStrict422))
		he, ok = err.(*echo.HTTPError)
		if !ok || he.Message != "invalid offset" {
			t.Fatalf("strict offset: %v", err)
		}
	})

	t.Run("optional", func(t *testing.T) {
		c := e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
		p, err := ParseOptionalListPage(c)
		if err != nil || p.Limit != 0 || p.Offset != 0 {
			t.Fatalf("optional empty: p=%+v err=%v", p, err)
		}
		c2 := e.NewContext(httptest.NewRequest(http.MethodGet, "/?limit=100&offset=5", nil), httptest.NewRecorder())
		p, err = ParseOptionalListPage(c2)
		if err != nil || p.Limit != 100 || p.Offset != 5 {
			t.Fatalf("optional set: p=%+v err=%v", p, err)
		}
		c3 := e.NewContext(httptest.NewRequest(http.MethodGet, "/?limit=x", nil), httptest.NewRecorder())
		_, err = ParseOptionalListPage(c3)
		he, ok := err.(*echo.HTTPError)
		if !ok || he.Code != http.StatusBadRequest {
			t.Fatalf("optional bad: %v", err)
		}
	})
}
