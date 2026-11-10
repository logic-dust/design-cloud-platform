package ticket

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/push"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	svc     *Service
	repos   *db.Repos
	project uint
	alice   Actor // 报告人
	bob     Actor // 指派人
	carol   Actor // 无关用户
	admin   Actor // 持有 tickets 管理范围
}

func setup(t *testing.T) *fixture {
	t.Helper()
	store, err := db.NewStore(config.Database{Type: config.DatabaseTypeSQLite, Path: filepath.Join(t.TempDir(), "test.db")}, slog.Default())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	require.NoError(t, store.Migrate())
	repos := db.NewRepos(store)

	tmp := t.TempDir()
	auditSvc, err := audit.NewService(audit.Config{PrimaryLogPath: filepath.Join(tmp, "audit.log"), SecurityLogPath: filepath.Join(tmp, "security.log")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = auditSvc.Close() })

	resolver := func(_ context.Context, location, _, _ string) error {
		if location != "home" {
			return errors.New("unknown location")
		}
		return nil
	}
	f := &fixture{
		svc:   NewService(repos.Ticket, repos.TicketProject, repos.User, auditSvc, slog.Default(), push.NoopPublisher{}, WithAttachmentResolver(resolver)),
		repos: repos,
	}
	ctx := context.Background()
	p := model.TicketProject{Name: "P"}
	require.NoError(t, repos.TicketProject.Create(ctx, &p))
	f.project = p.ID
	mk := func(name string, role model.Role, scopes model.AdminScopes, status model.UserStatusCode) Actor {
		u := model.User{Username: name, Role: role, AdminScopes: scopes, Status: status}
		require.NoError(t, repos.User.Create(ctx, &u))
		return Actor{ID: u.ID, Username: name, Role: role, Scopes: scopes}
	}
	f.alice = mk("alice", model.RoleNormal, 0, model.UserActive)
	f.bob = mk("bob", model.RoleNormal, 0, model.UserActive)
	f.carol = mk("carol", model.RoleNormal, 0, model.UserActive)
	f.admin = mk("ops", model.RoleNormal, model.AdminScopes(model.ScopeTickets), model.UserActive)
	return f
}

//go:fix inline
func ptr[T any](v T) *T { return new(v) }

func status(err error) int {
	if be, ok := errors.AsType[*BatchError](err); ok {
		return be.Status
	}
	if de, ok := errors.AsType[contract.DomainError](err); ok {
		return de.HTTPStatus()
	}
	return 0
}

func (f *fixture) create(t *testing.T, req CreateTicketRequest) TicketDTO {
	t.Helper()
	if req.ProjectID == 0 {
		req.ProjectID = f.project
	}
	if req.Title == "" {
		req.Title = "t"
	}
	got, err := f.svc.Create(context.Background(), f.alice, req)
	require.NoError(t, err)
	return got
}

func TestCreateDefaultsAndDTO(t *testing.T) {
	t.Parallel()
	f := setup(t)
	got := f.create(t, CreateTicketRequest{Title: "  login  ", AssigneeID: &f.bob.ID,
		AcceptanceCriteria: &model.TicketAcceptanceCriteria{Checklist: []model.TicketChecklistItem{{Text: "a"}}},
		Attachments:        []model.TicketAttachment{{Location: "home", Path: "docs/./a.png"}}})

	assert.Equal(t, "login", got.Title)
	assert.Equal(t, model.TicketTypeTask, got.Type)
	assert.Equal(t, model.TicketPriorityMedium, got.Priority)
	assert.Equal(t, model.TicketStatusToDo, got.Status)
	assert.Equal(t, ProjectRef{ID: f.project, Name: "P"}, got.Project)
	assert.Equal(t, UserRef{ID: f.alice.ID, Username: "alice"}, got.Reporter)
	require.NotNil(t, got.Assignee)
	assert.Equal(t, "bob", got.Assignee.Username)
	assert.Equal(t, 1, got.Version)
	assert.Equal(t, sortGap, got.SortOrder)
	assert.Equal(t, "docs/a.png", got.Attachments[0].Path)
	assert.Equal(t, "a.png", got.Attachments[0].Name)
	assert.Nil(t, got.DeletedAt)

	second := f.create(t, CreateTicketRequest{Status: new("done")})
	assert.Equal(t, sortGap, second.SortOrder, "first in its own column")
	assert.NotNil(t, second.CompletedAt)
	assert.Nil(t, second.Assignee)
	assert.Equal(t, []model.TicketAttachment{}, second.Attachments)

	third := f.create(t, CreateTicketRequest{})
	assert.Equal(t, 2*sortGap, third.SortOrder)
}

func TestCreateValidation(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	disabled := model.User{Username: "gone", Status: model.UserDisabled}
	require.NoError(t, f.repos.User.Create(ctx, &disabled))
	archived := model.TicketProject{Name: "old"}
	require.NoError(t, f.repos.TicketProject.Create(ctx, &archived))
	require.NoError(t, f.repos.TicketProject.UpdateFields(ctx, archived.ID, map[string]any{"is_archived": true}))

	cases := []struct {
		name string
		req  CreateTicketRequest
		code int
		msg  string
	}{
		{"no title", CreateTicketRequest{ProjectID: f.project, Title: "  "}, 422, "title is required"},
		{"long title", CreateTicketRequest{ProjectID: f.project, Title: repeat("x", 257)}, 422, "title is too long"},
		{"bad type", CreateTicketRequest{ProjectID: f.project, Title: "t", Type: new("epic")}, 422, "invalid type"},
		{"bad priority", CreateTicketRequest{ProjectID: f.project, Title: "t", Priority: new("9")}, 422, "invalid priority"},
		{"bad status", CreateTicketRequest{ProjectID: f.project, Title: "t", Status: new("closed")}, 422, "invalid status"},
		{"disabled assignee", CreateTicketRequest{ProjectID: f.project, Title: "t", AssigneeID: &disabled.ID}, 422, "invalid assignee"},
		{"admin assignee", CreateTicketRequest{ProjectID: f.project, Title: "t", AssigneeID: &f.admin.ID}, 422, "invalid assignee"},
		{"missing assignee", CreateTicketRequest{ProjectID: f.project, Title: "t", AssigneeID: new(uint(999))}, 422, "invalid assignee"},
		{"no project", CreateTicketRequest{Title: "t"}, 422, "projectId is required"},
		{"missing project", CreateTicketRequest{ProjectID: 999, Title: "t"}, 404, "project not found"},
		{"archived project", CreateTicketRequest{ProjectID: archived.ID, Title: "t"}, 409, "project is archived"},
		{"empty checklist item", CreateTicketRequest{ProjectID: f.project, Title: "t", AcceptanceCriteria: &model.TicketAcceptanceCriteria{Checklist: []model.TicketChecklistItem{{Text: " "}}}}, 422, "invalid acceptanceCriteria"},
		{"absolute attachment", CreateTicketRequest{ProjectID: f.project, Title: "t", Attachments: []model.TicketAttachment{{Location: "home", Path: "/etc/passwd"}}}, 422, "invalid attachment"},
		{"escaping attachment", CreateTicketRequest{ProjectID: f.project, Title: "t", Attachments: []model.TicketAttachment{{Location: "home", Path: "a/../../x"}}}, 422, "invalid attachment"},
		{"unknown location", CreateTicketRequest{ProjectID: f.project, Title: "t", Attachments: []model.TicketAttachment{{Location: "scratch", Path: "x"}}}, 422, "invalid attachment"},
	}
	for _, tc := range cases {
		_, err := f.svc.Create(ctx, f.alice, tc.req)
		require.Error(t, err, tc.name)
		assert.Equal(t, tc.code, status(err), tc.name)
		assert.Equal(t, tc.msg, err.Error(), tc.name)
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for range n {
		out = append(out, s...)
	}
	return string(out)
}

func TestUpdatePermissionsVersionAndEvents(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	tk := f.create(t, CreateTicketRequest{AssigneeID: &f.bob.ID})

	// 无关用户不能编辑
	_, err := f.svc.Update(ctx, f.carol, tk.ID, UpdateTicketRequest{Version: new(1), Title: new("x")})
	assert.Equal(t, http.StatusForbidden, status(err))
	// 缺版本号
	_, err = f.svc.Update(ctx, f.bob, tk.ID, UpdateTicketRequest{Title: new("x")})
	assert.Equal(t, "version is required", err.Error())
	// 指派人可编辑字段
	got, err := f.svc.Update(ctx, f.bob, tk.ID, UpdateTicketRequest{Version: new(1), Title: new("x"), Priority: new("critical"),
		AcceptanceCriteria: Nullable[model.TicketAcceptanceCriteria]{Set: true, Value: &model.TicketAcceptanceCriteria{Description: "d", Checklist: []model.TicketChecklistItem{{Text: "c", Done: true}}}},
		Attachments:        &[]model.TicketAttachment{{Location: "home", Path: "f.txt"}}})
	require.NoError(t, err)
	assert.Equal(t, 2, got.Version)
	assert.Equal(t, model.TicketPriorityCritical, got.Priority)
	require.NotNil(t, got.AcceptanceCriteria)
	assert.True(t, got.AcceptanceCriteria.Checklist[0].Done)
	assert.Equal(t, "f.txt", got.Attachments[0].Path)
	// 旧版本号 → 409
	_, err = f.svc.Update(ctx, f.bob, tk.ID, UpdateTicketRequest{Version: new(1), Title: new("y")})
	assert.Equal(t, http.StatusConflict, status(err))
	// 指派人不能改指派
	_, err = f.svc.Update(ctx, f.bob, tk.ID, UpdateTicketRequest{Version: new(2), AssigneeID: Nullable[uint]{Set: true}})
	assert.Equal(t, http.StatusForbidden, status(err))
	// 报告人取消指派、清空验收标准与附件
	got, err = f.svc.Update(ctx, f.alice, tk.ID, UpdateTicketRequest{Version: new(2), AssigneeID: Nullable[uint]{Set: true},
		AcceptanceCriteria: Nullable[model.TicketAcceptanceCriteria]{Set: true}, Attachments: &[]model.TicketAttachment{}})
	require.NoError(t, err)
	assert.Nil(t, got.Assignee)
	assert.Nil(t, got.AcceptanceCriteria)
	assert.Empty(t, got.Attachments)
	// 相同值不产生变更
	same, err := f.svc.Update(ctx, f.alice, tk.ID, UpdateTicketRequest{Version: new(3), Title: new("x")})
	require.NoError(t, err)
	assert.Equal(t, 3, same.Version)
	// 管理员可编辑、可指派
	got, err = f.svc.Update(ctx, f.admin, tk.ID, UpdateTicketRequest{Version: new(3), AssigneeID: Nullable[uint]{Set: true, Value: &f.carol.ID}, Status: new("done")})
	require.NoError(t, err)
	assert.Equal(t, "carol", got.Assignee.Username)
	assert.NotNil(t, got.CompletedAt)
	assert.Equal(t, model.TicketStatusDone, got.Status)
	got, err = f.svc.Update(ctx, f.carol, tk.ID, UpdateTicketRequest{Version: new(4), Status: new("inReview")})
	require.NoError(t, err)
	assert.Nil(t, got.CompletedAt)

	events, err := f.svc.ListEvents(ctx, tk.ID, 50, 0)
	require.NoError(t, err)
	var fields []string
	for _, e := range events.Items {
		fields = append(fields, string(e.Action)+":"+e.Field)
	}
	assert.Equal(t, []string{
		"created:", "updated:title", "updated:priority", "updated:acceptanceCriteria", "updated:attachments",
		"updated:assignee", "updated:acceptanceCriteria", "updated:attachments",
		"updated:assignee", "updated:status", "updated:status",
	}, fields)
	last := events.Items[len(events.Items)-1]
	assert.Equal(t, "carol", last.Actor.Username)
	assert.Equal(t, "done", *last.From)
	assert.Equal(t, "inReview", *last.To)
}

func TestDeleteSemantics(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	a := f.create(t, CreateTicketRequest{})
	b := f.create(t, CreateTicketRequest{})
	other, err := f.svc.Create(ctx, f.carol, CreateTicketRequest{ProjectID: f.project, Title: "c"})
	require.NoError(t, err)

	err = f.svc.Delete(ctx, f.alice, 999)
	assert.Equal(t, http.StatusNotFound, status(err))
	err = f.svc.Delete(ctx, f.alice, other.ID)
	assert.Equal(t, http.StatusForbidden, status(err))

	err = f.svc.Delete(ctx, f.alice, a.ID, b.ID, other.ID)
	var be *BatchError
	require.ErrorAs(t, err, &be)
	assert.Equal(t, "forbidden", be.Key)
	assert.Equal(t, []uint{other.ID}, be.IDs)
	_, err = f.svc.Get(ctx, a.ID)
	require.NoError(t, err, "all-or-nothing: nothing deleted")

	err = f.svc.Delete(ctx, f.alice, a.ID, 998, 999)
	require.ErrorAs(t, err, &be)
	assert.Equal(t, "notFound", be.Key)
	assert.Equal(t, []uint{998, 999}, be.IDs)

	require.NoError(t, f.svc.Delete(ctx, f.alice, a.ID, b.ID))
	_, err = f.svc.Get(ctx, a.ID)
	assert.Equal(t, http.StatusNotFound, status(err))
	assert.Equal(t, http.StatusNotFound, status(f.svc.Delete(ctx, f.alice, a.ID)), "already deleted")
	_, err = f.svc.Update(ctx, f.alice, a.ID, UpdateTicketRequest{Version: new(1), Title: new("x")})
	assert.Equal(t, http.StatusNotFound, status(err), "deleted ticket is not editable")

	require.NoError(t, f.svc.Delete(ctx, f.admin, other.ID), "admin may delete any")

	// 管理员恢复与永久删除
	restored, err := f.svc.AdminRestore(ctx, f.admin, a.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, restored.Version)
	_, err = f.svc.AdminRestore(ctx, f.admin, a.ID)
	assert.Equal(t, http.StatusConflict, status(err))
	require.NoError(t, f.svc.AdminPermanentDelete(ctx, f.admin, a.ID, b.ID))
	_, err = f.svc.AdminGet(ctx, b.ID)
	assert.Equal(t, http.StatusNotFound, status(err))
	assert.Equal(t, http.StatusUnprocessableEntity, status(f.svc.Delete(ctx, f.alice)))
}

func TestListQuery(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	f.create(t, CreateTicketRequest{Title: "Alpha", AssigneeID: &f.bob.ID})
	f.create(t, CreateTicketRequest{Title: "beta", Status: new("inProgress")})
	f.create(t, CreateTicketRequest{Title: "gamma"})

	page, err := f.svc.List(ctx, ListQuery{Limit: 2})
	require.NoError(t, err)
	assert.EqualValues(t, 3, page.TotalCount)
	assert.True(t, page.HasMore)
	assert.Len(t, page.Items, 2)
	require.NotNil(t, page.NextAfterSort)
	require.NotNil(t, page.NextAfterID)
	assert.Equal(t, page.Items[1].SortOrder, *page.NextAfterSort)
	assert.Equal(t, page.Items[1].ID, *page.NextAfterID)

	page, _ = f.svc.List(ctx, ListQuery{Limit: 50, Unassigned: true, Sort: "createdAt", Order: "asc"})
	require.Len(t, page.Items, 2)
	assert.Equal(t, "beta", page.Items[0].Title)
	assert.Nil(t, page.NextAfterSort)

	page, _ = f.svc.List(ctx, ListQuery{Limit: 50, Q: "ALP"})
	require.Len(t, page.Items, 1)

	page, _ = f.svc.List(ctx, ListQuery{Limit: 50, Statuses: []model.TicketStatus{model.TicketStatusToDo}})
	assert.Len(t, page.Items, 2)
}

func TestListKeysetMatchesOffset(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	var created []TicketDTO
	for _, title := range []string{"a", "b", "c", "d", "e"} {
		created = append(created, f.create(t, CreateTicketRequest{Title: title}))
	}

	page0, err := f.svc.List(ctx, ListQuery{Limit: 2, Sort: "sortOrder", Order: "asc"})
	require.NoError(t, err)
	require.Len(t, page0.Items, 2)
	assert.True(t, page0.HasMore)
	assert.EqualValues(t, 5, page0.TotalCount)

	page1Keyset, err := f.svc.List(ctx, ListQuery{
		Limit: 2, Sort: "sortOrder", Order: "asc",
		AfterSort: page0.NextAfterSort, AfterID: page0.NextAfterID,
	})
	require.NoError(t, err)
	assert.EqualValues(t, -1, page1Keyset.TotalCount) // keyset 不重算 COUNT
	page1Offset, err := f.svc.List(ctx, ListQuery{Limit: 2, Offset: 2, Sort: "sortOrder", Order: "asc"})
	require.NoError(t, err)
	require.Equal(t, idsOf(page1Offset.Items), idsOf(page1Keyset.Items))
	assert.Equal(t, page1Offset.HasMore, page1Keyset.HasMore)
	assert.Equal(t, 0, page1Keyset.Offset)

	// 游标与 offset 同时出现：游标优先
	page1Mixed, err := f.svc.List(ctx, ListQuery{
		Limit: 2, Offset: 0, Sort: "sortOrder", Order: "asc",
		AfterSort: page0.NextAfterSort, AfterID: page0.NextAfterID,
	})
	require.NoError(t, err)
	assert.Equal(t, idsOf(page1Keyset.Items), idsOf(page1Mixed.Items))

	page2, err := f.svc.List(ctx, ListQuery{
		Limit: 2, Sort: "sortOrder", Order: "asc",
		AfterSort: page1Keyset.NextAfterSort, AfterID: page1Keyset.NextAfterID,
	})
	require.NoError(t, err)
	require.Len(t, page2.Items, 1)
	assert.False(t, page2.HasMore)
	assert.Equal(t, created[4].ID, page2.Items[0].ID)

	_, err = f.svc.List(ctx, ListQuery{
		Limit: 2, Sort: "updatedAt",
		AfterSort: page0.NextAfterSort, AfterID: page0.NextAfterID,
	})
	assert.ErrorIs(t, err, ErrKeysetSortUnsupported)
}

func idsOf(items []TicketDTO) []uint {
	out := make([]uint, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestMoveAndRebalance(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	a := f.create(t, CreateTicketRequest{Title: "a"})
	b := f.create(t, CreateTicketRequest{Title: "b"})
	c := f.create(t, CreateTicketRequest{Title: "c"})

	order := func(st model.TicketStatus) []string {
		page, err := f.svc.List(ctx, ListQuery{Limit: 50, Statuses: []model.TicketStatus{st}})
		require.NoError(t, err)
		var out []string
		for _, it := range page.Items {
			out = append(out, it.Title)
		}
		return out
	}

	// c 放到 a 与 b 之间
	moved, err := f.svc.Move(ctx, f.alice, c.ID, MoveTicketRequest{Version: new(1), AfterID: &a.ID, BeforeID: &b.ID})
	require.NoError(t, err)
	assert.Equal(t, 2, moved.Version)
	assert.Equal(t, []string{"a", "c", "b"}, order(model.TicketStatusToDo))

	// 非相邻 → 422；他人 → 403；旧版本 → 409
	_, err = f.svc.Move(ctx, f.alice, a.ID, MoveTicketRequest{Version: new(1), AfterID: &b.ID, BeforeID: &c.ID})
	assert.Equal(t, "invalid position", err.Error())
	_, err = f.svc.Move(ctx, f.carol, a.ID, MoveTicketRequest{Version: new(1)})
	assert.Equal(t, http.StatusForbidden, status(err))
	_, err = f.svc.Move(ctx, f.alice, c.ID, MoveTicketRequest{Version: new(1)})
	assert.Equal(t, http.StatusConflict, status(err))

	// 跨列移动到 done
	moved, err = f.svc.Move(ctx, f.alice, b.ID, MoveTicketRequest{Version: new(1), Status: new("done")})
	require.NoError(t, err)
	assert.Equal(t, model.TicketStatusDone, moved.Status)
	assert.NotNil(t, moved.CompletedAt)
	assert.Equal(t, []string{"b"}, order(model.TicketStatusDone))

	// 反复把列尾工单插到前两张之间，间距每次减半，耗尽后整列重排，顺序始终正确
	ids := map[string]uint{"a": a.ID, "c": c.ID}
	d := f.create(t, CreateTicketRequest{Title: "d"})
	ids["d"] = d.ID
	cur := []string{"a", "c", "d"}
	for i := range 80 {
		last := cur[2]
		tk, err := f.svc.Get(ctx, ids[last])
		require.NoError(t, err)
		_, err = f.svc.Move(ctx, f.alice, tk.ID, MoveTicketRequest{Version: new(tk.Version), AfterID: new(ids[cur[0]]), BeforeID: new(ids[cur[1]])})
		require.NoError(t, err, "iteration %d", i)
		cur = []string{cur[0], last, cur[1]}
		require.Equal(t, cur, order(model.TicketStatusToDo), "iteration %d", i)
	}
}

func TestComments(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	tk := f.create(t, CreateTicketRequest{})

	_, err := f.svc.AddComment(ctx, f.carol, tk.ID, CommentRequest{Body: "  "})
	assert.Equal(t, "body is required", err.Error())
	cm, err := f.svc.AddComment(ctx, f.carol, tk.ID, CommentRequest{Body: "hello"})
	require.NoError(t, err)
	assert.Equal(t, "carol", cm.Author.Username)

	_, err = f.svc.EditComment(ctx, f.alice, tk.ID, cm.ID, CommentRequest{Body: "x"})
	assert.Equal(t, http.StatusForbidden, status(err))
	edited, err := f.svc.EditComment(ctx, f.carol, tk.ID, cm.ID, CommentRequest{Body: "hi"})
	require.NoError(t, err)
	assert.Equal(t, "hi", edited.Body)

	assert.Equal(t, http.StatusForbidden, status(f.svc.DeleteComment(ctx, f.alice, tk.ID, cm.ID)))
	assert.Equal(t, http.StatusNotFound, status(f.svc.DeleteComment(ctx, f.admin, tk.ID+1, cm.ID)))
	require.NoError(t, f.svc.DeleteComment(ctx, f.admin, tk.ID, cm.ID))
	page, err := f.svc.ListComments(ctx, tk.ID, 50, 0)
	require.NoError(t, err)
	assert.Empty(t, page.Items)

	require.NoError(t, f.svc.Delete(ctx, f.alice, tk.ID))
	_, err = f.svc.AddComment(ctx, f.carol, tk.ID, CommentRequest{Body: "late"})
	assert.Equal(t, http.StatusNotFound, status(err))
}

func TestSummaryTimezone(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	f.create(t, CreateTicketRequest{})
	f.create(t, CreateTicketRequest{Status: new("done")})

	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	s, err := f.svc.GetSummary(ctx, 0, 3, loc)
	require.NoError(t, err)
	assert.Equal(t, "Asia/Shanghai", s.Timezone)
	assert.Equal(t, 2, s.TotalTasks)
	assert.Equal(t, 1, s.CompletedTasks)
	assert.InDelta(t, 50.0, s.OverallCompletionRate, 0.001)
	assert.Equal(t, map[string]int{"todo": 1, "inProgress": 0, "inReview": 0, "done": 1}, s.ByStatus)
	require.Len(t, s.Trends, 3)
	today := s.Trends[2]
	assert.Equal(t, time.Now().In(loc).Format(time.DateOnly), today.Date)
	assert.Equal(t, 2, today.Created)
	assert.Equal(t, 1, today.Completed)
}

func TestProjects(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()

	_, err := f.svc.CreateProject(ctx, f.admin, CreateProjectRequest{Name: "P"})
	assert.Equal(t, http.StatusConflict, status(err))
	q, err := f.svc.CreateProject(ctx, f.admin, CreateProjectRequest{Name: " Q "})
	require.NoError(t, err)
	assert.Equal(t, "Q", q.Name)

	upd, err := f.svc.UpdateProject(ctx, f.admin, q.ID, UpdateProjectRequest{IsArchived: new(true), Description: new("d")})
	require.NoError(t, err)
	assert.True(t, upd.IsArchived)
	_, err = f.svc.UpdateProject(ctx, f.admin, q.ID, UpdateProjectRequest{Name: new("P")})
	assert.Equal(t, http.StatusConflict, status(err))

	page, err := f.svc.ListProjects(ctx, 50, 0, false, false)
	require.NoError(t, err)
	assert.Len(t, page.Items, 1)

	f.create(t, CreateTicketRequest{})
	assert.Equal(t, http.StatusConflict, status(f.svc.DeleteProject(ctx, f.admin, f.project)))
	require.NoError(t, f.svc.DeleteProject(ctx, f.admin, q.ID))
	assert.Equal(t, http.StatusNotFound, status(f.svc.DeleteProject(ctx, f.admin, q.ID)))
	_, err = f.svc.CreateProject(ctx, f.admin, CreateProjectRequest{Name: "Q"})
	require.NoError(t, err, "name reusable after delete")
}

func TestActorIsTicketAdmin(t *testing.T) {
	t.Parallel()
	assert.True(t, Actor{Role: model.RoleSuperAdmin}.IsTicketAdmin())
	assert.True(t, Actor{Role: model.RoleSystemAdmin}.IsTicketAdmin())
	assert.False(t, Actor{Role: model.RoleAuditAdmin}.IsTicketAdmin())
	assert.False(t, Actor{Role: model.RoleNormal}.IsTicketAdmin())
	assert.True(t, Actor{Role: model.RoleNormal, Scopes: model.AdminScopes(model.ScopeTickets)}.IsTicketAdmin())
	assert.False(t, Actor{Role: model.RoleNormal, Scopes: model.AdminScopes(model.ScopeNodes)}.IsTicketAdmin())
}
