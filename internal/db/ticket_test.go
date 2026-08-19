package db

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/model"
)

func newTicketRepoForTest(t *testing.T) (*Store, *TicketRepo, *TicketProjectRepo, *UserRepo) {
	t.Helper()
	store, err := NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewRepos(store)
	return store, repos.Ticket, repos.TicketProject, repos.User
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

func seedTicket(ctx context.Context, t *testing.T, repo *TicketRepo, projectID, reporterID, assigneeID uint, title string, status model.TicketStatus) uint {
	t.Helper()
	ticket := &model.Ticket{
		ProjectID:  projectID,
		Type:       model.TicketTypeTask,
		Title:      title,
		ReporterID: reporterID,
		AssigneeID: assigneeID,
		Status:     status,
		SortOrder:  1000.0,
	}
	if err := repo.Create(ctx, ticket); err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	return ticket.ID
}

// --- TicketStore Tests ---

func TestTicketStore_CreateAndGetByID(t *testing.T) {
	t.Parallel()
	_, ticketRepo, projectRepo, userRepo := newTicketRepoForTest(t)
	ctx := context.Background()

	projectID := seedTicketProject(ctx, t, projectRepo, "Test")
	reporterID := seedTicketUser(ctx, t, userRepo, "reporter1")
	assigneeID := seedTicketUser(ctx, t, userRepo, "assignee1")

	ticketID := seedTicket(ctx, t, ticketRepo, projectID, reporterID, assigneeID, "Test Ticket", model.TicketStatusToDo)

	got, err := ticketRepo.GetByID(ctx, ticketID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != ticketID {
		t.Fatalf("expected id %d, got %d", ticketID, got.ID)
	}
	if got.Title != "Test Ticket" {
		t.Fatalf("expected title 'Test Ticket', got '%s'", got.Title)
	}
	if got.ProjectID != projectID {
		t.Fatalf("expected projectID %d, got %d", projectID, got.ProjectID)
	}
	if got.ReporterID != reporterID {
		t.Fatalf("expected reporterID %d, got %d", reporterID, got.ReporterID)
	}
	if got.AssigneeID != assigneeID {
		t.Fatalf("expected assigneeID %d, got %d", assigneeID, got.AssigneeID)
	}
}

func TestTicketStore_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	_, ticketRepo, _, _ := newTicketRepoForTest(t)
	ctx := context.Background()

	_, err := ticketRepo.GetByID(ctx, 99999)
	if err == nil {
		t.Fatal("expected error for non-existent ticket")
	}
}

func TestTicketStore_UpdateFields(t *testing.T) {
	t.Parallel()
	_, ticketRepo, projectRepo, userRepo := newTicketRepoForTest(t)
	ctx := context.Background()

	projectID := seedTicketProject(ctx, t, projectRepo, "Test")
	reporterID := seedTicketUser(ctx, t, userRepo, "reporter2")
	assigneeID := seedTicketUser(ctx, t, userRepo, "assignee2")
	ticketID := seedTicket(ctx, t, ticketRepo, projectID, reporterID, assigneeID, "Original", model.TicketStatusToDo)

	err := ticketRepo.UpdateFields(ctx, ticketID, map[string]any{
		"title":  "Updated",
		"status": model.TicketStatusInProgress,
	})
	if err != nil {
		t.Fatalf("UpdateFields: %v", err)
	}

	got, err := ticketRepo.GetByID(ctx, ticketID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.Title != "Updated" {
		t.Fatalf("expected title 'Updated', got '%s'", got.Title)
	}
	if got.Status != model.TicketStatusInProgress {
		t.Fatalf("expected status InProgress, got %d", got.Status)
	}
}

func TestTicketStore_SoftDeleteAndHardDelete(t *testing.T) {
	t.Parallel()
	_, ticketRepo, projectRepo, userRepo := newTicketRepoForTest(t)
	ctx := context.Background()

	projectID := seedTicketProject(ctx, t, projectRepo, "Test")
	reporterID := seedTicketUser(ctx, t, userRepo, "reporter3")
	assigneeID := seedTicketUser(ctx, t, userRepo, "assignee3")
	ticketID := seedTicket(ctx, t, ticketRepo, projectID, reporterID, assigneeID, "ToDelete", model.TicketStatusToDo)

	// 软删除
	if err := ticketRepo.SoftDeleteByIDs(ctx, []uint{ticketID}); err != nil {
		t.Fatalf("SoftDeleteByIDs: %v", err)
	}

	// 正常查询不返回（List 默认过滤）
	results, total, err := ticketRepo.List(ctx, TicketFilter{Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, r := range results {
		if r.ID == ticketID {
			t.Fatal("expected soft-deleted ticket not to appear in List")
		}
	}
	if total != 0 {
		t.Fatalf("expected 0 after deleting all tickets, got %d", total)
	}

	// GetByID 仍可查到（管理员用）
	got, err := ticketRepo.GetByID(ctx, ticketID)
	if err != nil {
		t.Fatalf("GetByID after soft delete should still work: %v", err)
	}
	if got.ID != ticketID {
		t.Fatalf("expected id %d, got %d", ticketID, got.ID)
	}

	// 硬删除
	if err := ticketRepo.HardDeleteByIDs(ctx, []uint{ticketID}); err != nil {
		t.Fatalf("HardDeleteByIDs: %v", err)
	}

	// 硬删除后不可查
	_, err = ticketRepo.GetByID(ctx, ticketID)
	if err == nil {
		t.Fatal("expected error after hard delete")
	}
}

func TestTicketStore_ListWithFilters(t *testing.T) {
	t.Parallel()
	_, ticketRepo, projectRepo, userRepo := newTicketRepoForTest(t)
	ctx := context.Background()

	projectID := seedTicketProject(ctx, t, projectRepo, "Test")
	reporterID := seedTicketUser(ctx, t, userRepo, "reporter4")
	assigneeID := seedTicketUser(ctx, t, userRepo, "assignee4")

	seedTicket(ctx, t, ticketRepo, projectID, reporterID, assigneeID, "Ticket A", model.TicketStatusToDo)
	seedTicket(ctx, t, ticketRepo, projectID, reporterID, assigneeID, "Ticket B", model.TicketStatusInProgress)
	seedTicket(ctx, t, ticketRepo, projectID, reporterID, assigneeID, "Ticket C", model.TicketStatusDone)

	// 按状态过滤
	results, total, err := ticketRepo.List(ctx, TicketFilter{Status: new(model.TicketStatusInProgress)})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 ticket with status InProgress, got %d", total)
	}
	if len(results) != 1 || results[0].Title != "Ticket B" {
		t.Fatalf("expected 'Ticket B', got '%s'", results[0].Title)
	}

	// 分页
	limit := 2
	paged, pTotal, err := ticketRepo.List(ctx, TicketFilter{Limit: limit})
	if err != nil {
		t.Fatalf("List with limit: %v", err)
	}
	if pTotal != 3 {
		t.Fatalf("expected total 3, got %d", pTotal)
	}
	if len(paged) != 2 {
		t.Fatalf("expected 2 items, got %d", len(paged))
	}
}

func TestTicketStore_CountByProject(t *testing.T) {
	t.Parallel()
	_, ticketRepo, projectRepo, userRepo := newTicketRepoForTest(t)
	ctx := context.Background()

	projectID := seedTicketProject(ctx, t, projectRepo, "Test")
	reporterID := seedTicketUser(ctx, t, userRepo, "reporter5")
	assigneeID := seedTicketUser(ctx, t, userRepo, "assignee5")

	seedTicket(ctx, t, ticketRepo, projectID, reporterID, assigneeID, "A", model.TicketStatusToDo)
	seedTicket(ctx, t, ticketRepo, projectID, reporterID, assigneeID, "B", model.TicketStatusInProgress)

	count, err := ticketRepo.CountByProject(ctx, projectID, false)
	if err != nil {
		t.Fatalf("CountByProject: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2, got %d", count)
	}
}

func TestTicketStore_CountByStatus(t *testing.T) {
	t.Parallel()
	_, ticketRepo, projectRepo, userRepo := newTicketRepoForTest(t)
	ctx := context.Background()

	projectID := seedTicketProject(ctx, t, projectRepo, "Test")
	reporterID := seedTicketUser(ctx, t, userRepo, "reporter6")
	assigneeID := seedTicketUser(ctx, t, userRepo, "assignee6")

	seedTicket(ctx, t, ticketRepo, projectID, reporterID, assigneeID, "A", model.TicketStatusToDo)
	seedTicket(ctx, t, ticketRepo, projectID, reporterID, assigneeID, "B", model.TicketStatusToDo)
	seedTicket(ctx, t, ticketRepo, projectID, reporterID, assigneeID, "C", model.TicketStatusDone)

	counts, err := ticketRepo.CountByStatus(ctx, 0)
	if err != nil {
		t.Fatalf("CountByStatus: %v", err)
	}
	if counts[model.TicketStatusToDo] != 2 {
		t.Fatalf("expected 2 ToDo, got %d", counts[model.TicketStatusToDo])
	}
	if counts[model.TicketStatusDone] != 1 {
		t.Fatalf("expected 1 Done, got %d", counts[model.TicketStatusDone])
	}
}

func TestTicketStore_GetMaxSortOrderInStatus(t *testing.T) {
	t.Parallel()
	_, ticketRepo, projectRepo, userRepo := newTicketRepoForTest(t)
	ctx := context.Background()

	projectID := seedTicketProject(ctx, t, projectRepo, "Test")
	reporterID := seedTicketUser(ctx, t, userRepo, "reporter7")
	assigneeID := seedTicketUser(ctx, t, userRepo, "assignee7")

	ticket := &model.Ticket{
		ProjectID: projectID, Type: model.TicketTypeTask, Title: "Sort Test",
		ReporterID: reporterID, AssigneeID: assigneeID,
		Status: model.TicketStatusToDo, SortOrder: 5000.0,
	}
	if err := ticketRepo.Create(ctx, ticket); err != nil {
		t.Fatalf("create: %v", err)
	}

	maxSort, err := ticketRepo.GetMaxSortOrderInStatus(ctx, projectID, model.TicketStatusToDo)
	if err != nil {
		t.Fatalf("GetMaxSortOrderInStatus: %v", err)
	}
	if maxSort != 5000.0 {
		t.Fatalf("expected 5000, got %f", maxSort)
	}
}

// --- TicketProjectStore Tests ---

func TestTicketProjectStore_CreateAndGetByID(t *testing.T) {
	t.Parallel()
	_, _, projectRepo, _ := newTicketRepoForTest(t)
	ctx := context.Background()

	project := &model.TicketProject{Name: "Design Cloud"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if project.ID == 0 {
		t.Fatal("expected non-zero ID after create")
	}

	got, err := projectRepo.GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Design Cloud" {
		t.Fatalf("expected 'Design Cloud', got '%s'", got.Name)
	}
}

func TestTicketProjectStore_List(t *testing.T) {
	t.Parallel()
	_, _, projectRepo, _ := newTicketRepoForTest(t)
	ctx := context.Background()

	for i := range 3 {
		name := fmt.Sprintf("Project %d", i)
		if err := projectRepo.Create(ctx, &model.TicketProject{Name: name}); err != nil {
			t.Fatalf("create project %s: %v", name, err)
		}
	}

	projects, total, err := projectRepo.List(ctx, 10, 0, true, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected 3 projects, got %d", total)
	}
	if len(projects) != 3 {
		t.Fatalf("expected 3 items, got %d", len(projects))
	}
}

func TestTicketProjectStore_Archive(t *testing.T) {
	t.Parallel()
	_, _, projectRepo, _ := newTicketRepoForTest(t)
	ctx := context.Background()

	project := &model.TicketProject{Name: "Archivable"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := projectRepo.Archive(ctx, project.ID, true); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	got, err := projectRepo.GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !got.IsArchived {
		t.Fatal("expected project to be archived")
	}

	// 取消归档
	if err := projectRepo.Archive(ctx, project.ID, false); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	got2, _ := projectRepo.GetByID(ctx, project.ID)
	if got2.IsArchived {
		t.Fatal("expected project to be unarchived")
	}
}

func TestTicketProjectStore_SoftDelete(t *testing.T) {
	t.Parallel()
	_, _, projectRepo, _ := newTicketRepoForTest(t)
	ctx := context.Background()

	project := &model.TicketProject{Name: "Deletable"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := projectRepo.SoftDelete(ctx, project.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	// 软删除后 List 不返回
	projects, total, err := projectRepo.List(ctx, 10, 0, true, false)
	if err != nil {
		t.Fatalf("List after soft delete: %v", err)
	}
	if total != 0 {
		t.Fatalf("expected 0 projects after soft delete, got %d", total)
	}
	if len(projects) != 0 {
		t.Fatalf("expected 0 items, got %d", len(projects))
	}

	// GetByID 仍可查到（管理员用）
	got, err := projectRepo.GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetByID after soft delete should still work: %v", err)
	}
	if got.ID != project.ID {
		t.Fatalf("expected id %d, got %d", project.ID, got.ID)
	}
}
