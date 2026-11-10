package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
)

type fakeLaunchScheduler struct {
	result map[string]string
	err    error
	called bool
	appID  uint
	user   string
}

func (f *fakeLaunchScheduler) SubmitAppJob(_ context.Context, appInfo *model.App, requestUser string) (map[string]string, error) {
	f.called = true
	if appInfo != nil {
		f.appID = appInfo.ID
	}
	f.user = requestUser
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return map[string]string{"id": "job-default"}, nil
}

func newAppServiceForTest(t *testing.T) (*Service, *db.Repos) {
	return newAppServiceWithDepsForTest(t, nil)
}

func newAppServiceWithDepsForTest(
	t *testing.T,
	scheduler launchScheduler,
) (*Service, *db.Repos) {
	t.Helper()

	store, err := db.NewStore(config.Database{
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
	repos := db.NewRepos(store)
	return NewService(repos.App, repos.User, scheduler, slog.Default()), repos
}

func createTestApp(t *testing.T, repos *db.Repos, name string) model.App {
	t.Helper()
	app := model.App{Name: name, Type: model.BatchApp}
	if err := repos.App.Create(context.Background(), &app); err != nil {
		t.Fatalf("create app: %v", err)
	}
	return app
}

func createTestUser(t *testing.T, repos *db.Repos, username string) {
	t.Helper()
	if err := repos.User.Create(context.Background(), &model.User{Username: username, Status: model.UserActive}); err != nil {
		t.Fatalf("create user: %v", err)
	}
}

func TestAppServiceSetUsersValidatesUserInService(t *testing.T) {
	t.Parallel()

	service, repos := newAppServiceForTest(t)
	app := createTestApp(t, repos, "app-a")
	createTestUser(t, repos, "alice")

	err := service.SetUsers(context.Background(), app.ID, []string{"alice", "missing"})
	if !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("set users error = %v, want ErrNotFound", err)
	}

	users, err := service.ListUsers(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("users after failed set = %v, want empty", users)
	}
}

func TestAppServiceSetUsersDeduplicatesAndReplaces(t *testing.T) {
	t.Parallel()

	service, repos := newAppServiceForTest(t)
	app := createTestApp(t, repos, "app-a")
	createTestUser(t, repos, "alice")
	createTestUser(t, repos, "bob")

	if err := service.SetUsers(context.Background(), app.ID, []string{"alice", "alice", " bob ", "", "   "}); err != nil {
		t.Fatalf("set users: %v", err)
	}

	users, err := service.ListUsers(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("users = %v, want 2 users", users)
	}
}

func TestAppServiceAddAndRemoveUsersAreIdempotent(t *testing.T) {
	t.Parallel()

	service, repos := newAppServiceForTest(t)
	app := createTestApp(t, repos, "app-a")
	createTestUser(t, repos, "alice")
	createTestUser(t, repos, "bob")

	if err := service.AddUsers(context.Background(), app.ID, []string{"alice", "alice", "bob"}); err != nil {
		t.Fatalf("add users: %v", err)
	}
	if err := service.AddUsers(context.Background(), app.ID, []string{"alice"}); err != nil {
		t.Fatalf("add duplicate user: %v", err)
	}
	users, err := service.ListUsers(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("list users after add: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("users after add = %v, want 2 users", users)
	}

	if err := service.RemoveUsers(context.Background(), app.ID, []string{"alice", "alice"}); err != nil {
		t.Fatalf("remove users: %v", err)
	}
	if err := service.RemoveUsers(context.Background(), app.ID, []string{"alice"}); err != nil {
		t.Fatalf("remove missing association: %v", err)
	}
	users, err = service.ListUsers(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("list users after remove: %v", err)
	}
	if len(users) != 1 || users[0] != "bob" {
		t.Fatalf("users after remove = %v, want [bob]", users)
	}
}

func TestAppServiceUserOperationsReturnAppNotFound(t *testing.T) {
	t.Parallel()

	service, repos := newAppServiceForTest(t)
	createTestUser(t, repos, "alice")

	err := service.AddUsers(context.Background(), 999, []string{"alice"})
	if !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("add users missing app error = %v, want ErrNotFound", err)
	}
}

func TestAppServiceCreateAndUpdate(t *testing.T) {
	t.Parallel()

	service, _ := newAppServiceForTest(t)

	// Test Create
	form := model.AppBase{
		Name:  "test_app",
		Alias: "Test App",
		Type:  model.GUIApp,
		Queue: "gui-queue",
	}
	app, err := service.Create(context.Background(), form)
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if app.Queue != "gui-queue" {
		t.Fatalf("app.Queue = %s, want gui-queue", app.Queue)
	}

	// Verify in DB
	dbApp, err := service.GetByID(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("get app by id: %v", err)
	}
	if dbApp.Queue != "gui-queue" {
		t.Fatalf("dbApp.Queue = %s, want gui-queue", dbApp.Queue)
	}

	// Test Update
	updateForm := model.AppBase{
		Name:  "test_app",
		Alias: "Test App Updated",
		Type:  model.GUIApp,
		Queue: "new-queue",
	}
	updatedApp, err := service.Update(context.Background(), app.ID, updateForm)
	if err != nil {
		t.Fatalf("update app: %v", err)
	}
	if updatedApp.Queue != "new-queue" {
		t.Fatalf("updatedApp.Queue = %s, want new-queue", updatedApp.Queue)
	}

	// Verify in DB
	dbAppUpdated, err := service.GetByID(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("get app by id after update: %v", err)
	}
	if dbAppUpdated.Queue != "new-queue" {
		t.Fatalf("dbAppUpdated.Queue = %s, want new-queue", dbAppUpdated.Queue)
	}
}

func TestAppServiceFormValidationRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	service, _ := newAppServiceForTest(t)

	_, err := service.Create(context.Background(), model.AppBase{
		Name:  "bad_form",
		Alias: "Bad Form App",
		Type:  model.FormApp,
		Form:  "invalid-json",
	})
	var contractErr contract.DomainError
	if !errors.As(err, &contractErr) || contractErr.HTTPStatus() != http.StatusUnprocessableEntity {
		t.Fatalf("create with invalid form json err = %v, want 422 validation error", err)
	}

	_, err = service.Create(context.Background(), model.AppBase{
		Name:  "empty_form",
		Alias: "Empty Form App",
		Type:  model.FormApp,
		Form:  "",
	})
	if !errors.As(err, &contractErr) || contractErr.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("create with empty form err = %v, want 400 invalid param error", err)
	}
}

func TestAppServiceCreateValidatesNameAndAlias(t *testing.T) {
	t.Parallel()

	service, _ := newAppServiceForTest(t)

	_, err := service.Create(context.Background(), model.AppBase{
		Name:  "bad-name!",
		Alias: "Alias",
		Type:  model.BatchApp,
	})
	var contractErr contract.DomainError
	if !errors.As(err, &contractErr) || contractErr.HTTPStatus() != http.StatusUnprocessableEntity {
		t.Fatalf("create with bad name err = %v, want 422 validation error", err)
	}

	_, err = service.Create(context.Background(), model.AppBase{
		Name:  "good_name_1",
		Alias: strings.Repeat("测", 101),
		Type:  model.BatchApp,
	})
	if !errors.As(err, &contractErr) || contractErr.HTTPStatus() != http.StatusUnprocessableEntity {
		t.Fatalf("create with long alias err = %v, want 422 validation error", err)
	}
}

func TestAppServiceCreateMapsNameConflict(t *testing.T) {
	t.Parallel()

	service, _ := newAppServiceForTest(t)
	ctx := context.Background()

	_, err := service.Create(ctx, model.AppBase{
		Name:  "duplicate_app",
		Alias: "First",
		Type:  model.BatchApp,
	})
	if err != nil {
		t.Fatalf("create first app: %v", err)
	}

	_, err = service.Create(ctx, model.AppBase{
		Name:  "duplicate_app",
		Alias: "Second",
		Type:  model.BatchApp,
	})
	if !errors.Is(err, ErrAppNameConflict) {
		t.Fatalf("create duplicate app err = %v, want ErrAppNameConflict", err)
	}
}

func TestAppServiceLaunchDispatchesJobForAccessibleApp(t *testing.T) {
	t.Parallel()

	scheduler := &fakeLaunchScheduler{result: map[string]string{"id": "job-123"}}
	service, _ := newAppServiceWithDepsForTest(t, scheduler)
	ctx := context.Background()

	app, err := service.Create(ctx, model.AppBase{
		Name:   "gui_launch_app",
		Alias:  "GUI Launch",
		Type:   model.GUIApp,
		Public: true,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	job, err := service.Launch(ctx, app.ID, "alice")
	if err != nil {
		t.Fatalf("launch app: %v", err)
	}
	if job["id"] != "job-123" {
		t.Fatalf("job id = %q, want job-123", job["id"])
	}
	if !scheduler.called {
		t.Fatalf("scheduler should be called")
	}
	if scheduler.appID != app.ID || scheduler.user != "alice" {
		t.Fatalf("scheduler called with appID=%d user=%s, want appID=%d user=alice", scheduler.appID, scheduler.user, app.ID)
	}
}

func TestAppServiceLaunchRejectsInaccessiblePrivateApp(t *testing.T) {
	t.Parallel()

	scheduler := &fakeLaunchScheduler{}
	service, repos := newAppServiceWithDepsForTest(t, scheduler)
	ctx := context.Background()

	createTestUser(t, repos, "alice")
	createTestUser(t, repos, "bob")

	app, err := service.Create(ctx, model.AppBase{
		Name:   "private_gui_launch",
		Alias:  "Private GUI",
		Type:   model.GUIApp,
		Public: false,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if err := service.AddUsers(ctx, app.ID, []string{"alice"}); err != nil {
		t.Fatalf("add app user: %v", err)
	}

	_, err = service.Launch(ctx, app.ID, "bob")
	if err == nil {
		t.Fatalf("launch private app should fail for unauthorized user")
	}
	domErr, ok := errors.AsType[contract.DomainError](err)
	if !ok || domErr.HTTPStatus() != 403 {
		t.Fatalf("launch private app error = %v, want 403 DomainError", err)
	}
	if scheduler.called {
		t.Fatalf("scheduler should not be called for inaccessible app")
	}
}

func TestAppServiceListAccessible(t *testing.T) {
	t.Parallel()

	service, repos := newAppServiceForTest(t)
	ctx := context.Background()

	createTestUser(t, repos, "alice")
	createTestUser(t, repos, "bob")

	// 1. 公共应用
	pubApp := createTestApp(t, repos, "pub_app")
	if err := repos.App.Update(ctx, &model.App{ID: pubApp.ID, Name: "pub_app", Type: model.BatchApp, Public: true}); err != nil {
		t.Fatalf("update pub app: %v", err)
	}

	// 2. 授权给 alice 的私有应用
	aliceApp := createTestApp(t, repos, "alice_app")
	if err := service.SetUsers(ctx, aliceApp.ID, []string{"alice"}); err != nil {
		t.Fatalf("set users: %v", err)
	}

	// 3. 授权给 bob 的私有应用
	bobApp := createTestApp(t, repos, "bob_app")
	if err := service.SetUsers(ctx, bobApp.ID, []string{"bob"}); err != nil {
		t.Fatalf("set users: %v", err)
	}

	// alice 应该能查到 pub_app 和 alice_app，查不到 bob_app
	aliceApps, err := service.ListAccessible(ctx, "alice")
	if err != nil {
		t.Fatalf("list accessible alice: %v", err)
	}
	if len(aliceApps) != 2 {
		t.Fatalf("alice accessible apps count = %d, want 2", len(aliceApps))
	}

	// bob 应该能查到 pub_app 和 bob_app，查不到 alice_app
	bobApps, err := service.ListAccessible(ctx, "bob")
	if err != nil {
		t.Fatalf("list accessible bob: %v", err)
	}
	if len(bobApps) != 2 {
		t.Fatalf("bob accessible apps count = %d, want 2", len(bobApps))
	}
}
