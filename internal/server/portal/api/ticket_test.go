package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	ticketsvc "design-cloud-engine/internal/server/service/ticket"

	"github.com/labstack/echo/v5"
)

func ticketQueryContext(target string) *echo.Context {
	e := echo.New()
	return e.NewContext(httptest.NewRequest(http.MethodGet, target, nil), httptest.NewRecorder())
}

func TestParseTicketListQuery(t *testing.T) {
	t.Parallel()
	q, err := parseTicketListQuery(ticketQueryContext("/api/tickets?status=todo,inProgress&status=done&type=bug&priority=high&assigneeId=none&q=%20login%20&sort=dueDate&order=asc&limit=10&offset=20&projectId=3"))
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Statuses) != 3 || q.Statuses[2] != model.TicketStatusDone || len(q.Types) != 1 || len(q.Priorities) != 1 {
		t.Fatalf("enums: %+v", q)
	}
	if !q.Unassigned || q.Q != "login" || q.Sort != "dueDate" || q.Order != "asc" || q.Limit != 10 || q.Offset != 20 || *q.ProjectID != 3 {
		t.Fatalf("query: %+v", q)
	}

	def, err := parseTicketListQuery(ticketQueryContext("/api/tickets"))
	if err != nil || def.Limit != httputil.DefaultPageLimit || def.Offset != 0 {
		t.Fatalf("defaults: %+v %v", def, err)
	}

	for target, msg := range map[string]string{
		"/api/tickets?status=0":                             "invalid status",
		"/api/tickets?type=epic":                            "invalid type",
		"/api/tickets?priority=urgent":                      "invalid priority",
		"/api/tickets?limit=0":                              "invalid limit",
		"/api/tickets?limit=abc":                            "invalid limit",
		"/api/tickets?limit=2001":                           "invalid limit",
		"/api/tickets?offset=-1":                            "invalid offset",
		"/api/tickets?offset=2000":                          "invalid offset",
		"/api/tickets?sort=title":                           "invalid sort",
		"/api/tickets?order=up":                             "invalid order",
		"/api/tickets?assigneeId=x":                         "invalid assigneeId",
		"/api/tickets?projectId=0":                          "invalid projectId",
		"/api/tickets?afterSort=10":                         "invalid afterId",
		"/api/tickets?afterId=1":                            "invalid afterSort",
		"/api/tickets?afterSort=x&afterId=1":                "invalid afterSort",
		"/api/tickets?afterSort=10&afterId=0":               "invalid afterId",
		"/api/tickets?sort=updatedAt&afterSort=1&afterId=1": "invalid afterSort",
	} {
		_, err := parseTicketListQuery(ticketQueryContext(target))
		he, ok := err.(*echo.HTTPError)
		if !ok || he.Code != http.StatusUnprocessableEntity || he.Message != msg {
			t.Errorf("%s: got %v, want 422 %q", target, err, msg)
		}
	}

	ks, err := parseTicketListQuery(ticketQueryContext("/api/tickets?sort=sortOrder&order=asc&limit=200&offset=400&afterSort=10.5&afterId=9"))
	if err != nil {
		t.Fatal(err)
	}
	if ks.Limit != 200 || ks.Offset != 400 || ks.AfterSort == nil || *ks.AfterSort != 10.5 || ks.AfterID == nil || *ks.AfterID != 9 {
		t.Fatalf("keyset query: %+v", ks)
	}
	if !ks.HasKeyset() {
		t.Fatal("expected HasKeyset")
	}
}

func TestBindStrict(t *testing.T) {
	t.Parallel()
	e := echo.New()
	for body, ok := range map[string]bool{
		`{"title":"x","version":1}`:       true,
		`{"title":"x","sortOrder":1}`:     false,
		`{"title":`:                       false,
		`{"title":"x"} {"title":"y"}`:     false,
		`{"assigneeId":null,"version":1}`: true,
	} {
		c := e.NewContext(httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(body)), httptest.NewRecorder())
		var req ticketsvc.UpdateTicketRequest
		err := bindStrict(c, &req)
		if (err == nil) != ok {
			t.Errorf("%s: err=%v, want ok=%v", body, err, ok)
		}
		if ok && strings.Contains(body, "assigneeId") && (!req.AssigneeID.Set || req.AssigneeID.Value != nil) {
			t.Errorf("assigneeId null not captured: %+v", req.AssigneeID)
		}
	}
}

func TestRespondTicketBatchError(t *testing.T) {
	t.Parallel()
	e := echo.New()
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec)
	err := &ticketsvc.BatchError{Status: http.StatusNotFound, Msg: "tickets not found", Key: "notFound", IDs: []uint{3, 7}}
	if err := respondTicketError(c, err); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"details":{"notFound":[3,7]}`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func serveTicketAssignees(t *testing.T, h *ticketHandler, userID uint, target string) int {
	t.Helper()
	e := echo.New()
	e.GET("/api/tickets/assignees", func(c *echo.Context) error {
		c.Set(httputil.CtxSessionKey, &model.Session{UserID: userID, Username: "u"})
		return h.listTicketAssignees(c)
	})
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec.Code
}

func TestListTicketAssigneesHotWithoutQuery(t *testing.T) {
	t.Parallel()
	h := newTicketHandler(nil, nil)
	for _, target := range []string{
		"/api/tickets/assignees",
		"/api/tickets/assignees?q=",
		"/api/tickets/assignees?q=%20a%20",
		"/api/tickets/assignees?q=%E4%B8%AD",
	} {
		if code := serveTicketAssignees(t, h, 1, target); code != http.StatusOK {
			t.Fatalf("%s: status=%d, want 200", target, code)
		}
	}
}

func TestListTicketAssigneesSearchRequiresMinRunes(t *testing.T) {
	t.Parallel()
	h := newTicketHandler(nil, nil)
	if code := serveTicketAssignees(t, h, 1, "/api/tickets/assignees?q=ab"); code != http.StatusOK {
		t.Fatalf("search: status=%d, want 200", code)
	}
}

func TestListTicketAssigneesPerUserRateLimit(t *testing.T) {
	t.Parallel()
	h := newTicketHandler(nil, nil)
	limited := false
	for range 20 {
		if serveTicketAssignees(t, h, 7, "/api/tickets/assignees?q=ab") == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("expected 429 after burst for the same user")
	}
	if code := serveTicketAssignees(t, h, 8, "/api/tickets/assignees?q=ab"); code == http.StatusTooManyRequests {
		t.Fatalf("other user: status=%d, want not limited", code)
	}
}
