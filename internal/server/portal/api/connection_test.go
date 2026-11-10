package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"design-cloud-engine/internal/server/portal/httputil"
	connsvc "design-cloud-engine/internal/server/service/connection"
	"design-cloud-engine/internal/server/service/proxy"

	"github.com/labstack/echo/v5"
)

func TestHandleConnectionError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{
			name:       "no healthy online nodes -> 503",
			err:        connsvc.ErrNoHealthyNode,
			wantStatus: http.StatusServiceUnavailable,
			wantMsg:    connsvc.ErrNoHealthyNode.Error(),
		},
		{
			name:       "all nodes occupied -> 409",
			err:        connsvc.ErrAllNodesOccupied,
			wantStatus: http.StatusConflict,
			wantMsg:    connsvc.ErrAllNodesOccupied.Error(),
		},
		{
			name:       "node limit reached -> 409",
			err:        connsvc.ErrNodeConnectionLimit,
			wantStatus: http.StatusConflict,
			wantMsg:    connsvc.ErrNodeConnectionLimit.Error(),
		},
		{
			name:       "target node unavailable -> 503",
			err:        connsvc.ErrTargetNodeUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantMsg:    connsvc.ErrTargetNodeUnavailable.Error(),
		},
		{
			name:       "candidate node set empty -> 422",
			err:        connsvc.ErrCandidateSetEmpty,
			wantStatus: http.StatusUnprocessableEntity,
			wantMsg:    connsvc.ErrCandidateSetEmpty.Error(),
		},
		{
			name:       "resource in use -> 409",
			err:        connsvc.ErrResourceInUse,
			wantStatus: http.StatusConflict,
			wantMsg:    connsvc.ErrResourceInUse.Error(),
		},
		{
			name:       "resource name conflict -> 409",
			err:        connsvc.ErrResourceNameConflict,
			wantStatus: http.StatusConflict,
			wantMsg:    connsvc.ErrResourceNameConflict.Error(),
		},
		{
			name:       "display occupied -> 409",
			err:        connsvc.ErrDisplayOccupied,
			wantStatus: http.StatusConflict,
			wantMsg:    connsvc.ErrDisplayOccupied.Error(),
		},
		{
			name:       "display conflict -> 409",
			err:        connsvc.ErrDisplayConflict,
			wantStatus: http.StatusConflict,
			wantMsg:    connsvc.ErrDisplayConflict.Error(),
		},
		{
			name:       "still starting -> 409 stable message",
			err:        fmt.Errorf("wrapped: %w", connsvc.ErrConnectionStillStarting),
			wantStatus: http.StatusConflict,
			wantMsg:    connsvc.ErrConnectionStillStarting.Error(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/api/connections", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			if err := handleConnectionError(c, tc.err); err != nil {
				t.Fatalf("unexpected error returned by handler: %v", err)
			}

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			var body httputil.Response
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Message != tc.wantMsg {
				t.Errorf("message = %q, want %q", body.Message, tc.wantMsg)
			}
		})
	}
}

func TestHandleProxyError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "proxy node in maintenance -> 503",
			err:        proxy.ErrNodeInMaintenance,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "proxy node connections disallowed -> 503",
			err:        proxy.ErrNodeConnectionsDisallowed,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "proxy service disabled -> 503",
			err:        proxy.ErrProxyServiceDisabled,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "proxy connection forbidden -> 403",
			err:        proxy.ErrProxyConnectionForbidden,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "proxy connection not found -> 404",
			err:        proxy.ErrConnectionNotFound,
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/api/connections/1/proxy-connection", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			if err := handleProxyError(c, tc.err); err != nil {
				t.Fatalf("unexpected error returned by handler: %v", err)
			}

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}
