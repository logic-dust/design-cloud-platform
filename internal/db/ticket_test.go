package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
)

func newTicketRepoForTest(t *testing.T) (*TicketRepo, *TicketProjectRepo, *UserRepo) {
	t.Helper()
	store, err := NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewRepos(store)
	return repos.Ticket, repos.TicketProject, repos.User
}

func seedTicketProject(ctx context.Context, t *testing.T, repo *TicketProjectRepo, name string) uint {
	t.Helper()
	project := &model.TicketProject{Name: name}
	if err := repo.Create(ctx, project); err != nil {
		t.Fatalf("create ticket project: %v", err)
	}
	return project.ID
}

func seedTicketUser(ctx context.Context, t *testing.T, repo *UserRepo, name string) uint {
	t.Helper()
	user := &model.User{Username: name, Status: model.UserActive}
	if err := repo.Create(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user.ID
}

func seedTicket(ctx context.Context, t *testing.T, repo *TicketRepo, tk model.Ticket) uint {
	t.Helper()
	if tk.Title == "" {
		tk.Title = "t"
	}
	if err := repo.Create(ctx, &tk); err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	return tk.ID
}

func TestTicketRepo_GetPreloadsAndHidesDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, projects, users := newTicketRepoForTest(t)
	pid := seedTicketProject(ctx, t, projects, "p")
	alice := seedTicketUser(ctx, t, users, "alice")
	bob := seedTicketUser(ctx, t, users, "bob")
	id := seedTicket(ctx, t, repo, model.Ticket{ProjectID: pid, ReporterID: alice, AssigneeID: &bob,
		Attachments: []model.TicketAttachment{{Location: "home", Path: "a.txt", Name: "a.txt"}}})

	got, err := repo.Get(ctx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Project.Name != "p" || got.Reporter.Username != "alice" || got.Assignee == nil || got.Assignee.Username != "bob" {
		t.Fatalf("refs not preloaded: %+v", got)
	}
	if got.Reporter.Status != 0 {
		t.Fatal("reporter preload must only select id and username")
	}
	if len(got.Attachments) != 1 || got.Attachments[0].Path != "a.txt" || got.Version != 1 {
		t.Fatalf("unexpected ticket: %+v", got)
	}

	if err := repo.SoftDelete(ctx, []uint{id}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, id, false); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("deleted ticket visible: %v", err)
	}
	if _, err := repo.Get(ctx, id, true); err != nil {
		t.Fatalf("includeDeleted: %v", err)
	}
	if err := repo.Restore(ctx, []uint{id}); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Get(ctx, id, false); err != nil || got.Version != 2 {
		t.Fatalf("restore: %v version=%d", err, got.Version)
	}
}

func TestTicketRepo_UpdateVersioned(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, projects, users := newTicketRepoForTest(t)
	pid := seedTicketProject(ctx, t, projects, "p")
	u := seedTicketUser(ctx, t, users, "u")
	id := seedTicket(ctx, t, repo, model.Ticket{ProjectID: pid, ReporterID: u})

	if err := repo.UpdateVersioned(ctx, id, 1, map[string]any{"title": "new"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateVersioned(ctx, id, 1, map[string]any{"title": "stale"}); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("stale: %v", err)
	}
	if err := repo.UpdateVersioned(ctx, 999, 1, map[string]any{"title": "x"}); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	got, _ := repo.Get(ctx, id, false)
	if got.Title != "new" || got.Version != 2 {
		t.Fatalf("got %q v%d", got.Title, got.Version)
	}
}

func TestTicketRepo_ListFiltersAndSorting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, projects, users := newTicketRepoForTest(t)
	p1 := seedTicketProject(ctx, t, projects, "p1")
	p2 := seedTicketProject(ctx, t, projects, "p2")
	a := seedTicketUser(ctx, t, users, "a")
	b := seedTicketUser(ctx, t, users, "b")
	due := time.Now().Add(48 * time.Hour)

	ids := map[string]uint{}
	for _, tk := range []model.Ticket{
		{ProjectID: p1, ReporterID: a, AssigneeID: &b, Title: "Login bug", Status: model.TicketStatusToDo, Priority: model.TicketPriorityHigh, SortOrder: 2},
		{ProjectID: p1, ReporterID: a, Title: "100% coverage", Status: model.TicketStatusInProgress, Priority: model.TicketPriorityLow, SortOrder: 1, DueDate: &due},
		{ProjectID: p1, ReporterID: b, AssigneeID: &a, Title: "docs", Status: model.TicketStatusDone, SortOrder: 3},
		{ProjectID: p2, ReporterID: a, Title: "other", Status: model.TicketStatusToDo, SortOrder: 1},
	} {
		ids[tk.Title] = seedTicket(ctx, t, repo, tk)
	}
	titles := func(f TicketFilter) string {
		t.Helper()
		if f.Limit == 0 {
			f.Limit = 50
		}
		got, _, err := repo.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(got))
		for _, tk := range got {
			out = append(out, tk.Title)
		}
		return fmt.Sprint(out)
	}

	cases := []struct {
		name string
		f    TicketFilter
		want string
	}{
		{"project sortOrder", TicketFilter{ProjectID: &p1}, "[100% coverage Login bug docs]"},
		{"multi status", TicketFilter{ProjectID: &p1, Statuses: []model.TicketStatus{model.TicketStatusToDo, model.TicketStatusDone}}, "[Login bug docs]"},
		{"unassigned", TicketFilter{Unassigned: true}, "[100% coverage other]"},
		{"assignee", TicketFilter{AssigneeID: &b}, "[Login bug]"},
		{"title q case-insensitive", TicketFilter{TitleQuery: "LOGIN"}, "[Login bug]"},
		{"title q literal percent", TicketFilter{TitleQuery: "0%"}, "[100% coverage]"},
		{"priority desc", TicketFilter{ProjectID: &p1, Sort: TicketSortPriority, Desc: true}, "[Login bug docs 100% coverage]"},
		{"dueDate nulls last", TicketFilter{ProjectID: &p1, Sort: TicketSortDueDate}, "[100% coverage Login bug docs]"},
	}
	for _, tc := range cases {
		if got := titles(tc.f); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}

	if _, total, _ := repo.List(ctx, TicketFilter{Limit: 1}); total != 4 {
		t.Fatalf("total=%d", total)
	}
}

func TestTicketRepo_ListKeysetSortOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, projects, users := newTicketRepoForTest(t)
	pid := seedTicketProject(ctx, t, projects, "p")
	u := seedTicketUser(ctx, t, users, "u")

	// 相同 sortOrder 多条 + 递增值，验证 id 兜底与 keyset/offset 一致。
	var ids []uint
	for i, so := range []float64{10, 10, 10, 20, 30, 40} {
		id := seedTicket(ctx, t, repo, model.Ticket{
			ProjectID: pid, ReporterID: u, Title: fmt.Sprintf("t%d", i), SortOrder: so,
		})
		ids = append(ids, id)
	}

	pageIDs := func(f TicketFilter) (ids []uint, total int64) {
		t.Helper()
		got, total, err := repo.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]uint, len(got))
		for i, tk := range got {
			out[i] = tk.ID
		}
		return out, total
	}

	base := TicketFilter{ProjectID: &pid, Sort: TicketSortOrder, Limit: 2}
	p0, total0 := pageIDs(base)
	if len(p0) != 2 || p0[0] != ids[0] || p0[1] != ids[1] || total0 != 6 {
		t.Fatalf("page0=%v total=%d want [%d %d] total=6", p0, total0, ids[0], ids[1])
	}
	afterSort := 10.0
	p1Keyset, totalKS := pageIDs(TicketFilter{
		ProjectID: &pid, Sort: TicketSortOrder, Limit: 2,
		AfterSort: &afterSort, AfterID: &p0[1],
	})
	if totalKS != -1 {
		t.Fatalf("keyset total=%d want -1 (count skipped)", totalKS)
	}
	p1Offset, _ := pageIDs(TicketFilter{ProjectID: &pid, Sort: TicketSortOrder, Limit: 2, Offset: 2})
	if fmt.Sprint(p1Keyset) != fmt.Sprint(p1Offset) || fmt.Sprint(p1Keyset) != fmt.Sprint([]uint{ids[2], ids[3]}) {
		t.Fatalf("keyset=%v offset=%v want [%d %d]", p1Keyset, p1Offset, ids[2], ids[3])
	}

	afterSort = 20
	p2, _ := pageIDs(TicketFilter{
		ProjectID: &pid, Sort: TicketSortOrder, Limit: 2,
		AfterSort: &afterSort, AfterID: &ids[3],
	})
	if fmt.Sprint(p2) != fmt.Sprint([]uint{ids[4], ids[5]}) {
		t.Fatalf("page2=%v", p2)
	}

	// desc：游标比较符反向
	desc0, _ := pageIDs(TicketFilter{ProjectID: &pid, Sort: TicketSortOrder, Desc: true, Limit: 2})
	if desc0[0] != ids[5] || desc0[1] != ids[4] {
		t.Fatalf("desc0=%v", desc0)
	}
	afterSort = 30
	desc1, _ := pageIDs(TicketFilter{
		ProjectID: &pid, Sort: TicketSortOrder, Desc: true, Limit: 2,
		AfterSort: &afterSort, AfterID: &ids[4],
	})
	desc1Offset, _ := pageIDs(TicketFilter{ProjectID: &pid, Sort: TicketSortOrder, Desc: true, Limit: 2, Offset: 2})
	if fmt.Sprint(desc1) != fmt.Sprint(desc1Offset) {
		t.Fatalf("desc keyset=%v offset=%v", desc1, desc1Offset)
	}

	// 过滤变更后旧游标：结果为空（不报错）；keyset 不跑 COUNT → total=-1
	other := seedTicketProject(ctx, t, projects, "other")
	empty, total, err := repo.List(ctx, TicketFilter{
		ProjectID: &other, Sort: TicketSortOrder, Limit: 2,
		AfterSort: &afterSort, AfterID: &ids[4],
	})
	if err != nil || len(empty) != 0 || total != -1 {
		t.Fatalf("stale cursor: items=%d total=%d err=%v", len(empty), total, err)
	}

	_, _, err = repo.List(ctx, TicketFilter{
		Sort: TicketSortUpdatedAt, Limit: 1, AfterSort: &afterSort, AfterID: &ids[0],
	})
	if err == nil || !strings.Contains(err.Error(), "keyset pagination only supports") {
		t.Fatalf("want unsupported sort err, got %v", err)
	}

	// SQLite EXPLAIN：board 过滤 + keyset 应走 idx_tickets_board；全局 keyset 走 idx_tickets_sort_id。
	sqlDB, err := repo.store.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	explainUsesIndex := func(sql string, args ...any) {
		t.Helper()
		rows, qerr := sqlDB.Query("EXPLAIN QUERY PLAN "+sql, args...)
		if qerr != nil {
			t.Fatal(qerr)
		}
		defer rows.Close()
		var plan strings.Builder
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail)
			plan.WriteByte('\n')
		}
		s := plan.String()
		if !strings.Contains(s, "INDEX") {
			t.Fatalf("expected index use, plan:\n%s", s)
		}
	}
	explainUsesIndex(
		`SELECT id FROM tickets WHERE deleted_at IS NULL AND project_id = ? AND ((sort_order > ?) OR (sort_order = ? AND id > ?)) ORDER BY sort_order ASC, id ASC LIMIT 2`,
		pid, 10.0, 10.0, p0[1],
	)
	explainUsesIndex(
		`SELECT id FROM tickets WHERE deleted_at IS NULL AND ((sort_order > ?) OR (sort_order = ? AND id > ?)) ORDER BY sort_order ASC, id ASC LIMIT 2`,
		10.0, 10.0, p0[1],
	)
}

func TestTicketRepo_ColumnAndCounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, projects, users := newTicketRepoForTest(t)
	pid := seedTicketProject(ctx, t, projects, "p")
	u := seedTicketUser(ctx, t, users, "u")
	a := seedTicket(ctx, t, repo, model.Ticket{ProjectID: pid, ReporterID: u, SortOrder: 20})
	b := seedTicket(ctx, t, repo, model.Ticket{ProjectID: pid, ReporterID: u, SortOrder: 10})
	seedTicket(ctx, t, repo, model.Ticket{ProjectID: pid, ReporterID: u, Status: model.TicketStatusDone, SortOrder: 5})

	col, err := repo.Column(ctx, pid, model.TicketStatusToDo)
	if err != nil || len(col) != 2 || col[0].ID != b || col[1].ID != a {
		t.Fatalf("column=%v err=%v", col, err)
	}
	if max, _ := repo.MaxSortOrder(ctx, pid, model.TicketStatusToDo); max != 20 {
		t.Fatalf("max=%v", max)
	}
	if err := repo.SetSortOrders(ctx, []TicketPosition{{ID: a, SortOrder: 1}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, a, false); got.SortOrder != 1 || got.Version != 1 {
		t.Fatalf("set sort: %+v", got)
	}
	counts, _ := repo.CountByStatus(ctx, pid)
	if counts[model.TicketStatusToDo] != 2 || counts[model.TicketStatusDone] != 1 {
		t.Fatalf("counts=%v", counts)
	}
	if n, _ := repo.CountActiveByProject(ctx, pid); n != 3 {
		t.Fatalf("active=%d", n)
	}
	times, err := repo.TimesInRange(ctx, "created_at", pid, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil || len(times) != 3 {
		t.Fatalf("times=%v err=%v", times, err)
	}
}

func TestTicketRepo_CommentsEventsAndHardDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, projects, users := newTicketRepoForTest(t)
	pid := seedTicketProject(ctx, t, projects, "p")
	u := seedTicketUser(ctx, t, users, "u")
	id := seedTicket(ctx, t, repo, model.Ticket{ProjectID: pid, ReporterID: u})

	c := &model.TicketComment{TicketID: id, AuthorID: u, Body: "hi"}
	if err := repo.CreateComment(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateEvents(ctx, []model.TicketEvent{{TicketID: id, ActorID: u, Action: model.TicketEventCreated}}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetComment(ctx, id, c.ID)
	if err != nil || got.Author.Username != "u" {
		t.Fatalf("comment=%+v err=%v", got, err)
	}
	if _, err := repo.GetComment(ctx, id+1, c.ID); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("comment of other ticket: %v", err)
	}
	if list, total, _ := repo.ListEvents(ctx, id, 10, 0); total != 1 || list[0].Actor.Username != "u" {
		t.Fatalf("events=%+v", list)
	}

	if err := repo.HardDelete(ctx, []uint{id}); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := repo.ListComments(ctx, id, 10, 0); total != 0 {
		t.Fatal("comments not removed")
	}
	if _, total, _ := repo.ListEvents(ctx, id, 10, 0); total != 0 {
		t.Fatal("events not removed")
	}
}

func TestTicketProjectRepo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, projects, _ := newTicketRepoForTest(t)
	a := seedTicketProject(ctx, t, projects, "a")
	b := seedTicketProject(ctx, t, projects, "b")
	if err := projects.UpdateFields(ctx, b, map[string]any{"is_archived": true}); err != nil {
		t.Fatal(err)
	}
	if list, total, _ := projects.List(ctx, 10, 0, false, false); total != 1 || list[0].ID != a {
		t.Fatalf("active list=%v", list)
	}
	if _, total, _ := projects.List(ctx, 10, 0, true, false); total != 2 {
		t.Fatal("includeArchived")
	}
	if err := projects.SoftDelete(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.Get(ctx, a, false); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("deleted project visible: %v", err)
	}
	if err := projects.UpdateFields(ctx, a, map[string]any{"name": "x"}); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("update deleted: %v", err)
	}
	if _, total, _ := projects.List(ctx, 10, 0, true, true); total != 2 {
		t.Fatal("includeDeleted")
	}

	// 名称仅在未删除项目中唯一：活跃重名应失败，软删后可复用同名。
	if err := projects.Create(ctx, &model.TicketProject{Name: "b"}); !IsUniqueConstraintError(err) {
		t.Fatalf("duplicate active name: err=%v, want unique violation", err)
	}
	if err := projects.Create(ctx, &model.TicketProject{Name: "a"}); err != nil {
		t.Fatalf("recreate soft-deleted name: %v", err)
	}
}
