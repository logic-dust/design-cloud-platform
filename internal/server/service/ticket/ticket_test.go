package ticket

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService_CreateTicket(t *testing.T) {
	t.Parallel()
	svc, repos := setupTestCore(t)
	ctx := context.Background()

	projectID := seedProject(t, repos, "Test")
	reporterID := seedUser(t, repos, "reporter")
	assigneeID := seedUser(t, repos, "assignee")

	req := CreateTicketRequest{
		ProjectID:   projectID,
		Type:        int(model.TicketTypeBug),
		Title:       "测试工单",
		Description: "这是一个测试工单",
		AssigneeID:  assigneeID,
		Priority:    int(model.TicketPriorityHigh),
		Status:      int(model.TicketStatusToDo),
	}

	item, err := svc.Create(ctx, req, reporterID, "reporter")
	require.NoError(t, err)
	require.NotNil(t, item)
	assert.Equal(t, "测试工单", item.Title)
	assert.Equal(t, model.TicketTypeBug, item.Type)
	assert.Equal(t, model.TicketPriorityHigh, item.Priority)
	assert.Equal(t, model.TicketStatusToDo, item.Status)
	assert.Equal(t, float64(1000), item.SortOrder)
	assert.Equal(t, reporterID, item.ReporterID)
	assert.Equal(t, assigneeID, item.AssigneeID)
}

func TestService_CreateTicket_Validation(t *testing.T) {
	t.Parallel()
	svc, repos := setupTestCore(t)
	ctx := context.Background()

	projectID := seedProject(t, repos, "Test")
	reporterID := seedUser(t, repos, "reporter")
	assigneeID := seedUser(t, repos, "assignee")

	t.Run("missing title", func(t *testing.T) {
		req := CreateTicketRequest{
			ProjectID:  projectID,
			AssigneeID: assigneeID,
			Title:      "",
		}
		_, err := svc.Create(ctx, req, reporterID, "reporter")
		assert.ErrorIs(t, err, ErrTitleRequired)
	})

	t.Run("project not found", func(t *testing.T) {
		req := CreateTicketRequest{
			ProjectID:  99999,
			AssigneeID: assigneeID,
			Title:      "No Project",
		}
		_, err := svc.Create(ctx, req, reporterID, "reporter")
		assert.ErrorIs(t, err, common.ErrNotFound)
	})

	t.Run("assignee not found", func(t *testing.T) {
		req := CreateTicketRequest{
			ProjectID:  projectID,
			AssigneeID: 99999,
			Title:      "No Assignee",
		}
		_, err := svc.Create(ctx, req, reporterID, "reporter")
		assert.ErrorIs(t, err, common.ErrNotFound)
	})
}

func TestService_GetTicketByID(t *testing.T) {
	t.Parallel()
	svc, repos := setupTestCore(t)
	ctx := context.Background()

	projectID := seedProject(t, repos, "Test")
	reporterID := seedUser(t, repos, "r1")
	assigneeID := seedUser(t, repos, "a1")

	created, err := svc.Create(ctx, CreateTicketRequest{
		ProjectID: projectID, Title: "Get Me", AssigneeID: assigneeID,
	}, reporterID, "reporter")
	require.NoError(t, err)

	got, err := svc.GetByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Get Me", got.Title)
}

func TestService_GetTicketByID_NotFound(t *testing.T) {
	t.Parallel()
	svc, _ := setupTestCore(t)
	ctx := context.Background()

	_, err := svc.GetByID(ctx, 99999)
	assert.ErrorIs(t, err, common.ErrNotFound)
}

func TestService_UpdateTicket(t *testing.T) {
	t.Parallel()
	svc, repos := setupTestCore(t)
	ctx := context.Background()

	projectID := seedProject(t, repos, "Test")
	reporterID := seedUser(t, repos, "r2")
	assigneeID := seedUser(t, repos, "a2")

	created, err := svc.Create(ctx, CreateTicketRequest{
		ProjectID: projectID, Title: "Original", AssigneeID: assigneeID,
	}, reporterID, "reporter")
	require.NoError(t, err)

	ticketID := created.ID
	updated, err := svc.Update(ctx, ticketID, UpdateTicketRequest{
		Title: new("Updated Title"),
	}, "reporter")
	require.NoError(t, err)
	assert.Equal(t, "Updated Title", updated.Title)
}

func TestService_DeleteTicket_Success(t *testing.T) {
	t.Parallel()
	svc, repos := setupTestCore(t)
	ctx := context.Background()

	projectID := seedProject(t, repos, "Test")
	reporterID := seedUser(t, repos, "r3")
	assigneeID := seedUser(t, repos, "a3")

	created, err := svc.Create(ctx, CreateTicketRequest{
		ProjectID: projectID, Title: "To Delete", AssigneeID: assigneeID,
	}, reporterID, "reporter")
	require.NoError(t, err)

	err = svc.Delete(ctx, reporterID, "reporter", created.ID)
	require.NoError(t, err)

	got, err := svc.GetByID(ctx, created.ID)
	require.NoError(t, err)
	assert.True(t, got.DeletedAt.Valid)
}

func TestService_DeleteTicket_Forbidden(t *testing.T) {
	t.Parallel()
	svc, repos := setupTestCore(t)
	ctx := context.Background()

	projectID := seedProject(t, repos, "Test")
	reporterID := seedUser(t, repos, "r4")
	assigneeID := seedUser(t, repos, "a4")
	otherUserID := seedUser(t, repos, "other")

	created, err := svc.Create(ctx, CreateTicketRequest{
		ProjectID: projectID, Title: "Protected", AssigneeID: assigneeID,
	}, reporterID, "reporter")
	require.NoError(t, err)

	err = svc.Delete(ctx, otherUserID, "other", created.ID)
	assert.ErrorIs(t, err, ErrPermissionDenied)
}

func TestService_DeleteTickets_Bulk(t *testing.T) {
	t.Parallel()
	svc, repos := setupTestCore(t)
	ctx := context.Background()

	projectID := seedProject(t, repos, "Test")
	reporterID := seedUser(t, repos, "r_bulk")
	assigneeID := seedUser(t, repos, "a_bulk")

	t1, err := svc.Create(ctx, CreateTicketRequest{
		ProjectID: projectID, Title: "Ticket 1", AssigneeID: assigneeID,
	}, reporterID, "r_bulk")
	require.NoError(t, err)

	t2, err := svc.Create(ctx, CreateTicketRequest{
		ProjectID: projectID, Title: "Ticket 2", AssigneeID: assigneeID,
	}, reporterID, "r_bulk")
	require.NoError(t, err)

	// User bulk soft delete
	err = svc.Delete(ctx, reporterID, "r_bulk", t1.ID, t2.ID)
	require.NoError(t, err)

	got1, err := svc.GetByID(ctx, t1.ID)
	require.NoError(t, err)
	assert.True(t, got1.DeletedAt.Valid)

	got2, err := svc.GetByID(ctx, t2.ID)
	require.NoError(t, err)
	assert.True(t, got2.DeletedAt.Valid)

	// Admin permanent delete
	err = svc.AdminPermanentDelete(ctx, t1.ID, t2.ID)
	require.NoError(t, err)

	_, err = svc.GetByID(ctx, t1.ID)
	assert.Error(t, err)
}

func TestService_ListTickets(t *testing.T) {
	t.Parallel()
	svc, repos := setupTestCore(t)
	ctx := context.Background()

	projectID := seedProject(t, repos, "Test")
	reporterID := seedUser(t, repos, "r5")
	assigneeID := seedUser(t, repos, "a5")

	// 创建 3 个工单
	for i := range 3 {
		title := fmt.Sprintf("Ticket %d", i)
		_, err := svc.Create(ctx, CreateTicketRequest{
			ProjectID: projectID, Title: title, AssigneeID: assigneeID,
		}, reporterID, "reporter")
		require.NoError(t, err)
	}

	// 无过滤条件
	result, err := svc.List(ctx, Filter{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(3), result.TotalCount)
	assert.Len(t, result.Items, 3)
	assert.False(t, result.HasMore)

	// 按 projectId 过滤
	result, err = svc.List(ctx, Filter{ProjectID: &projectID, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(3), result.TotalCount)
}

func TestService_ListTickets_Pagination(t *testing.T) {
	t.Parallel()
	svc, repos := setupTestCore(t)
	ctx := context.Background()

	projectID := seedProject(t, repos, "Test")
	reporterID := seedUser(t, repos, "r6")
	assigneeID := seedUser(t, repos, "a6")

	for i := range 5 {
		_, err := svc.Create(ctx, CreateTicketRequest{
			ProjectID: projectID, Title: fmt.Sprintf("T%d", i), AssigneeID: assigneeID,
		}, reporterID, "reporter")
		require.NoError(t, err)
	}

	// 第一页：limit=2
	page1, err := svc.List(ctx, Filter{Limit: 2, Offset: 0})
	require.NoError(t, err)
	assert.Equal(t, int64(5), page1.TotalCount)
	assert.Len(t, page1.Items, 2)
	assert.True(t, page1.HasMore)

	// 第二页：limit=2, offset=2
	page2, err := svc.List(ctx, Filter{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Len(t, page2.Items, 2)
	assert.True(t, page2.HasMore)

	// 第三页：limit=2, offset=4
	page3, err := svc.List(ctx, Filter{Limit: 2, Offset: 4})
	require.NoError(t, err)
	assert.Len(t, page3.Items, 1)
	assert.False(t, page3.HasMore)
}

func TestService_CreateProject(t *testing.T) {
	t.Parallel()
	svc, _ := setupTestCore(t)
	ctx := context.Background()

	// 成功创建
	proj, err := svc.CreateProject(ctx, CreateProjectRequest{
		Name: "Design Cloud", Description: "主项目",
	}, 1, "admin")
	require.NoError(t, err)
	assert.Equal(t, "Design Cloud", proj.Name)
	assert.False(t, proj.IsArchived)

	// 重名冲突
	_, err = svc.CreateProject(ctx, CreateProjectRequest{
		Name: "Design Cloud", Description: "重复项目",
	}, 1, "admin")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProjectNameConflict)

	// 空名称
	_, err = svc.CreateProject(ctx, CreateProjectRequest{
		Name: "   ", Description: "空名称",
	}, 1, "admin")
	require.Error(t, err)

	// 超长名称
	_, err = svc.CreateProject(ctx, CreateProjectRequest{
		Name: strings.Repeat("a", 129),
	}, 1, "admin")
	require.Error(t, err)
}

func TestService_UpdateProject(t *testing.T) {
	t.Parallel()
	svc, repos := setupTestCore(t)
	ctx := context.Background()

	projectID := seedProject(t, repos, "Project A")
	seedProject(t, repos, "Project B")

	// 成功更新
	newName := "Project A Updated"
	updated, err := svc.UpdateProject(ctx, projectID, UpdateProjectRequest{
		Name: &newName,
	}, 1, "admin")
	require.NoError(t, err)
	assert.Equal(t, newName, updated.Name)

	// 更新为已存在名称应冲突
	conflictName := "Project B"
	_, err = svc.UpdateProject(ctx, projectID, UpdateProjectRequest{
		Name: &conflictName,
	}, 1, "admin")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProjectNameConflict)

	// 更新为空名称应报错
	emptyName := "  "
	_, err = svc.UpdateProject(ctx, projectID, UpdateProjectRequest{
		Name: &emptyName,
	}, 1, "admin")
	require.Error(t, err)
}

func TestService_DeleteProject_WithTickets(t *testing.T) {
	t.Parallel()
	svc, repos := setupTestCore(t)
	ctx := context.Background()

	projectID := seedProject(t, repos, "Test")
	reporterID := seedUser(t, repos, "r7")
	assigneeID := seedUser(t, repos, "a7")

	// 创建工单
	_, err := svc.Create(ctx, CreateTicketRequest{
		ProjectID: projectID, Title: "Blocking", AssigneeID: assigneeID,
	}, reporterID, "reporter")
	require.NoError(t, err)

	// 删除项目应失败
	err = svc.DeleteProject(ctx, projectID, reporterID, "reporter")
	assert.ErrorIs(t, err, ErrProjectHasTickets)
}

func TestService_ArchiveProject(t *testing.T) {
	t.Parallel()
	svc, repos := setupTestCore(t)
	ctx := context.Background()

	projectID := seedProject(t, repos, "Test")

	proj, err := svc.ArchiveProject(ctx, projectID, true, 1, "admin")
	require.NoError(t, err)
	assert.True(t, proj.IsArchived)

	proj, err = svc.ArchiveProject(ctx, projectID, false, 1, "admin")
	require.NoError(t, err)
	assert.False(t, proj.IsArchived)
}

func TestService_GetSummary(t *testing.T) {
	t.Parallel()
	svc, repos := setupTestCore(t)
	ctx := context.Background()

	projectID := seedProject(t, repos, "Test")
	reporterID := seedUser(t, repos, "r8")
	assigneeID := seedUser(t, repos, "a8")

	// 创建 2 个工单
	_, err := svc.Create(ctx, CreateTicketRequest{
		ProjectID: projectID, Title: "A", AssigneeID: assigneeID,
		Status: int(model.TicketStatusDone),
	}, reporterID, "reporter")
	require.NoError(t, err)

	_, err = svc.Create(ctx, CreateTicketRequest{
		ProjectID: projectID, Title: "B", AssigneeID: assigneeID,
		Status: int(model.TicketStatusToDo),
	}, reporterID, "reporter")
	require.NoError(t, err)

	summary, err := svc.GetSummary(ctx, projectID)
	require.NoError(t, err)
	assert.Equal(t, 2, summary.TotalTasks)
	assert.Equal(t, 1, summary.CompletedTasks)
	assert.InDelta(t, 50.0, summary.OverallCompletionRate, 0.01)
	assert.Len(t, summary.Trends, 7)
}

// --- helpers ---
