package api

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"

	"design-cloud-engine/internal/contract"
	"github.com/labstack/echo/v5"
)

// ============================================================================
// respondError
// ============================================================================

func TestRespondError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"not found → 404", contract.ErrNotFound, http.StatusNotFound},
		{"forbidden → 403", contract.NewForbiddenError("no perm"), http.StatusForbidden},
		{"validation → 422", contract.NewValidationError("bad input"), http.StatusUnprocessableEntity},
		{"unknown → 500", errors.New("db connection lost"), http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := httputil.RespondError(c, tt.err)
			if err != nil {
				t.Fatalf("respondError returned error: %v", err)
			}

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			// Verify the response body has message field
			body := rec.Body.String()
			if !strings.Contains(body, `"message"`) {
				t.Errorf("response missing 'message' field: %s", body)
			}
		})
	}
}

func TestRespondErrorSafeMessage(t *testing.T) {
	t.Parallel()

	// Unknown error must NOT leak the raw message to client.
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	rawErr := errors.New("postgres: duplicate key value violates unique constraint")
	if err := httputil.RespondError(c, rawErr); err != nil {
		t.Fatalf("respondError: %v", err)
	}

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}

	body := rec.Body.String()
	// Must NOT leak raw db error
	if strings.Contains(body, "postgres") || strings.Contains(body, "duplicate key") {
		t.Errorf("response body leaked raw error: %s", body)
	}
	// Must contain safe generic message
	if !strings.Contains(body, "internal server error") {
		t.Errorf("response body missing safe message: %s", body)
	}
}

// ============================================================================
// parseUintParam
// ============================================================================

func TestParseUintParam(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		param     string
		value     string
		wantErr   bool
		wantValue uint
	}{
		{"valid", "id", "42", false, 42},
		{"zero returns error", "id", "0", true, 0},
		{"negative returns error", "id", "-1", true, 0},
		{"empty returns error", "id", "", true, 0},
		{"non-numeric returns error", "id", "abc", true, 0},
		{"large uint", "id", "4294967295", false, 4294967295},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Set path param using echo v5 PathValues API
			if tt.value != "" {
				c.SetPathValues(echo.PathValues{{Name: tt.param, Value: tt.value}})
			}

			val, err := httputil.ParseUintParam(c, tt.param)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (val=%d)", val)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if val != tt.wantValue {
				t.Errorf("val = %d, want %d", val, tt.wantValue)
			}
		})
	}
}

// ============================================================================
// parseOptionalIntQuery
// ============================================================================

func TestParseOptionalIntQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		wantErr bool
		wantVal int
	}{
		{"valid", "10", false, 10},
		{"empty returns 0", "", false, 0},
		{"zero returns error", "0", true, 0},
		{"negative returns error", "-5", true, 0},
		{"non-numeric returns error", "abc", true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/?limit="+tt.value, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			val, err := httputil.ParseOptionalIntQuery(c, "limit")
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (val=%d)", val)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if val != tt.wantVal {
				t.Errorf("val = %d, want %d", val, tt.wantVal)
			}
		})
	}
}

// ============================================================================
// parseOptionalUintQuery
// ============================================================================

func TestParseOptionalUintQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		wantErr bool
		wantVal uint
	}{
		{"valid", "10", false, 10},
		{"empty returns 0", "", false, 0},
		{"zero returns error", "0", true, 0},
		{"negative returns error", "-5", true, 0},
		{"non-numeric returns error", "abc", true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/?limit="+tt.value, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			val, err := httputil.ParseOptionalUintQuery(c, "limit")
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (val=%d)", val)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if val != tt.wantVal {
				t.Errorf("val = %d, want %d", val, tt.wantVal)
			}
		})
	}
}

// ============================================================================
// requireSession
// ============================================================================

func TestRequireSession(t *testing.T) {
	t.Parallel()

	t.Run("no session in context → 401 error", func(t *testing.T) {
		t.Parallel()

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		session, err := httputil.RequireSession(c)
		if session != nil {
			t.Errorf("expected nil session, got %+v", session)
		}
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		httpErr, ok := errors.AsType[*echo.HTTPError](err)
		if !ok {
			t.Fatalf("expected *echo.HTTPError, got %T", err)
		}
		if httpErr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", httpErr.Code)
		}
		if !strings.Contains(fmt.Sprint(httpErr.Message), "no active session") {
			t.Errorf("unexpected message: %v", httpErr.Message)
		}
	})

	t.Run("wrong type in context → 401 error", func(t *testing.T) {
		t.Parallel()

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.Set(httputil.CtxSessionKey, "not-a-session")

		session, err := httputil.RequireSession(c)
		if session != nil {
			t.Errorf("expected nil session, got %+v", session)
		}
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		httpErr, ok := errors.AsType[*echo.HTTPError](err)
		if !ok {
			t.Fatalf("expected *echo.HTTPError, got %T", err)
		}
		if httpErr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", httpErr.Code)
		}
		if !strings.Contains(fmt.Sprint(httpErr.Message), "session type mismatch") {
			t.Errorf("unexpected message: %v", httpErr.Message)
		}
	})

	t.Run("valid session returns session", func(t *testing.T) {
		t.Parallel()

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		expectedSession := &model.Session{
			ID:       1,
			Username: "testuser",
			UserID:   100,
		}
		c.Set(httputil.CtxSessionKey, expectedSession)

		session, err := httputil.RequireSession(c)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if session == nil {
			t.Fatal("expected non-nil session")
		}
		if session.Username != expectedSession.Username {
			t.Errorf("username = %q, want %q", session.Username, expectedSession.Username)
		}
	})
}

// ============================================================================
// parseUintParam — name-specific error messages
// ============================================================================

func TestParseUintParamErrorMessage(t *testing.T) {
	t.Parallel()

	t.Run("missing param", func(t *testing.T) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		// Don't set the param at all

		_, err := httputil.ParseUintParam(c, "missing")
		if err == nil {
			t.Fatal("expected error")
		}
		httpErr, ok := errors.AsType[*echo.HTTPError](err)
		if !ok {
			t.Fatalf("expected *echo.HTTPError, got %T", err)
		}
		if httpErr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", httpErr.Code)
		}
		msg := httpErr.Message
		if !strings.Contains(msg, "missing") {
			t.Errorf("message should contain param name: %v", httpErr.Message)
		}
	})
}

func TestIDList_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		json    string
		want    httputil.IDList
		wantErr bool
	}{
		{
			name: "numbers array with duplicates and zero",
			json: `{"ids": [1, 2, 2, 0, 3]}`,
			want: httputil.IDList{1, 2, 3},
		},
		{
			name: "strings array with spaces and duplicates",
			json: `{"ids": [" 1 ", "2", "2", "", "3 "]}`,
			want: httputil.IDList{1, 2, 3},
		},
		{
			name:    "invalid non-numeric string",
			json:    `{"ids": ["abc"]}`,
			wantErr: true,
		},
		{
			name:    "negative string",
			json:    `{"ids": ["-5"]}`,
			wantErr: true,
		},
		{
			name:    "invalid type",
			json:    `{"ids": "1,2,3"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var req struct {
				IDs httputil.IDList `json:"ids"`
			}
			err := json.Unmarshal([]byte(tt.json), &req)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (IDs=%v)", req.IDs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(req.IDs) != len(tt.want) {
				t.Fatalf("len(IDs) = %d, want %d (%v vs %v)", len(req.IDs), len(tt.want), req.IDs, tt.want)
			}
			for i := range req.IDs {
				if req.IDs[i] != tt.want[i] {
					t.Errorf("IDs[%d] = %d, want %d", i, req.IDs[i], tt.want[i])
				}
			}
		})
	}
}

func TestParsePagination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		query        string
		defaultLimit int
		maxLimit     int
		wantLimit    int
		wantOffset   int
	}{
		{
			name:         "defaults",
			query:        "",
			defaultLimit: httputil.DefaultPageLimit,
			maxLimit:     httputil.MaxPageLimit,
			wantLimit:    httputil.DefaultPageLimit,
			wantOffset:   0,
		},
		{
			name:         "custom within bounds",
			query:        "?limit=100&offset=20",
			defaultLimit: httputil.DefaultPageLimit,
			maxLimit:     httputil.MaxPageLimit,
			wantLimit:    100,
			wantOffset:   20,
		},
		{
			name:         "limit clamped to max",
			query:        "?limit=5000&offset=10",
			defaultLimit: httputil.DefaultPageLimit,
			maxLimit:     httputil.MaxPageLimit,
			wantLimit:    2000,
			wantOffset:   10,
		},
		{
			name:         "limit clamped to min 1 on negative",
			query:        "?limit=-5&offset=-10",
			defaultLimit: httputil.DefaultPageLimit,
			maxLimit:     httputil.MaxPageLimit,
			wantLimit:    1,
			wantOffset:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/"+tt.query, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			p := httputil.ParsePagination(c, tt.defaultLimit, tt.maxLimit)
			if p.Limit != tt.wantLimit {
				t.Errorf("p.Limit = %d, want %d", p.Limit, tt.wantLimit)
			}
			if p.Offset != tt.wantOffset {
				t.Errorf("p.Offset = %d, want %d", p.Offset, tt.wantOffset)
			}
		})
	}
}

func TestRespondPage_And_RespondData(t *testing.T) {
	t.Parallel()

	e := echo.New()

	// Test RespondPage
	rec1 := httptest.NewRecorder()
	c1 := e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec1)
	err := httputil.RespondPage(c1, []string{"a", "b"}, 2)
	if err != nil {
		t.Fatalf("RespondPage error = %v", err)
	}
	if rec1.Code != http.StatusOK {
		t.Errorf("RespondPage status = %d, want 200", rec1.Code)
	}

	// Test RespondData
	rec2 := httptest.NewRecorder()
	c2 := e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec2)
	err = httputil.RespondData(c2, map[string]string{"foo": "bar"})
	if err != nil {
		t.Fatalf("RespondData error = %v", err)
	}
	if rec2.Code != http.StatusOK {
		t.Errorf("RespondData status = %d, want 200", rec2.Code)
	}
}

func TestDefaultRestrictedSessionAllowedPaths(t *testing.T) {
	t.Parallel()

	allowedMap := make(map[string]struct{}, len(defaultRestrictedSessionAllowedPaths))
	for _, p := range defaultRestrictedSessionAllowedPaths {
		allowedMap[p] = struct{}{}
	}

	required := []string{
		"/api/users/me/password",
		"/api/users/me",
		"/api/sessions/current",
		"/api/sessions",
	}
	for _, p := range required {
		if _, ok := allowedMap[p]; !ok {
			t.Errorf("expected %q in defaultRestrictedSessionAllowedPaths", p)
		}
	}

	blocked := []string{
		"/api/overview/cluster",
		"/api/apps",
		"/api/files",
		"/api/admin/system/status",
	}
	for _, p := range blocked {
		if _, ok := allowedMap[p]; ok {
			t.Errorf("expected %q NOT in defaultRestrictedSessionAllowedPaths", p)
		}
	}
}

func TestMarkCurrentSession(t *testing.T) {
	t.Parallel()

	out := markCurrentSession([]model.Session{{ID: 1}, {ID: 2}}, 2)
	if out[0].Current || !out[1].Current {
		t.Fatalf("current flags = %v/%v, want false/true", out[0].Current, out[1].Current)
	}

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := strings.Count(string(b), `"current":true`); got != 1 {
		t.Fatalf("current:true occurrences = %d, want 1 in %s", got, b)
	}
	if strings.Contains(string(b), `"current":false`) {
		t.Fatalf("non-current entries should omit current: %s", b)
	}
	if !strings.Contains(string(b), `"hostname"`) || !strings.Contains(string(b), `"platform"`) {
		t.Fatalf("session fields should be flattened: %s", b)
	}
}
