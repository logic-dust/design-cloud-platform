package api

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/server/service/scheduler"

	"github.com/labstack/echo/v5"
)

type dummySchedulerExecutor struct{}

func (dummySchedulerExecutor) Execute(context.Context, *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	return nil, nil
}

type staticSchedulerSettingsReader struct {
	st schema.SchedulerSettings
}

func (r staticSchedulerSettingsReader) GetSchedulerSettings(context.Context) (schema.SchedulerSettings, error) {
	return r.st, nil
}

func newTestSchedulerService(name schema.JobScheduler) *scheduler.Service {
	svc, _ := scheduler.NewService(dummySchedulerExecutor{}, staticSchedulerSettingsReader{st: schema.SchedulerSettings{Type: name}}, slog.Default(), nil)
	return svc
}

func TestRespondJobQueryError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{
			name:       "job not found",
			err:        fmt.Errorf("scheduler query: %w", scheduler.ErrJobNotFound),
			wantStatus: http.StatusNotFound,
			wantBody:   "job not found",
		},
		{
			name:       "invalid job id",
			err:        scheduler.ValidateJobID("123[1]"),
			wantStatus: http.StatusBadRequest,
			wantBody:   "job id must be a positive integer: 123[1]",
		},
		{
			name:       "scheduler failure",
			err:        errors.New("scheduler connection failed"),
			wantStatus: http.StatusInternalServerError,
			wantBody:   "internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			rec := httptest.NewRecorder()
			c := e.NewContext(httptest.NewRequest(http.MethodGet, "/api/scheduler/jobs/123", nil), rec)

			if err := respondJobQueryError(c, tt.err); err != nil {
				t.Fatalf("respondJobQueryError: %v", err)
			}
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("response = %q, want it to contain %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestParseJobOpAcceptsAllMetaActions(t *testing.T) {
	t.Parallel()
	for _, name := range []schema.JobScheduler{schema.JobSchedulerLSF, schema.JobSchedulerSlurm} {
		svc := newTestSchedulerService(name)
		if svc == nil {
			t.Fatalf("newTestSchedulerService(%q) returned nil", name)
		}
		meta, err := svc.Meta(context.Background())
		if err != nil {
			t.Fatalf("Meta() failed: %v", err)
		}
		for action := range meta.Actions {
			op, ok := scheduler.ParseJobOp(string(action))
			if !ok || op != action {
				t.Errorf("ParseJobOp(%q) = %q, %v", action, op, ok)
			}
		}
	}
	if _, ok := scheduler.ParseJobOp("top"); ok {
		t.Error("ParseJobOp accepted top")
	}
}

func TestSchedulerGetMetaHandler(t *testing.T) {
	t.Parallel()

	for _, name := range []schema.JobScheduler{schema.JobSchedulerLSF, schema.JobSchedulerSlurm, schema.JobSchedulerOpenLava} {
		t.Run(string(name), func(t *testing.T) {
			t.Parallel()
			svc := newTestSchedulerService(name)
			if svc == nil {
				t.Fatalf("newTestSchedulerService(%q) returned nil", name)
			}
			h := newSchedulerHandler(svc)

			for _, path := range []string{"/api/scheduler/meta", "/api/admin/system/scheduler/meta"} {
				e := echo.New()
				rec := httptest.NewRecorder()
				c := e.NewContext(httptest.NewRequest(http.MethodGet, path, nil), rec)

				if err := h.getMeta(c); err != nil {
					t.Fatalf("getMeta(%s) failed: %v", path, err)
				}
				if rec.Code != http.StatusOK {
					t.Fatalf("getMeta(%s) status = %d, want 200", path, rec.Code)
				}

				var resp struct {
					Data scheduler.SchedulerMeta `json:"data"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("json.Unmarshal failed: %v, body = %s", err, rec.Body.String())
				}

				if resp.Data.Scheduler != name {
					t.Errorf("resp.Data.Scheduler = %q, want %q", resp.Data.Scheduler, name)
				}
				if len(resp.Data.Fields.Job) == 0 || len(resp.Data.Fields.Host) == 0 || len(resp.Data.Fields.Queue) == 0 {
					t.Errorf("resp.Data.Fields incomplete: %+v", resp.Data.Fields)
				}
				if resp.Data.Keys.ID == "" || resp.Data.Keys.User == "" || resp.Data.Keys.Status == "" || resp.Data.Keys.SubmitTime == "" {
					t.Errorf("resp.Data.Keys is incomplete: %+v", resp.Data.Keys)
				}
				if len(resp.Data.Actions) == 0 {
					t.Errorf("resp.Data.Actions is empty")
				}
			}
		})
	}
}
