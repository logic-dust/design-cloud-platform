package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

var (
	// ErrAppNameConflict is returned when an app name already exists.
	ErrAppNameConflict = errors.New("app name already exists")
)

type launchScheduler interface {
	SubmitAppJob(ctx context.Context, appInfo *model.App, requestUser string) (map[string]string, error)
}

// Service provides application management logic.
type Service struct {
	appRepo   *db.AppRepo
	userRepo  *db.UserRepo
	scheduler launchScheduler
	logger    *slog.Logger
}

// NewService creates a new app service with its repository and runtime orchestration dependencies.
func NewService(
	appRepo *db.AppRepo,
	userRepo *db.UserRepo,
	scheduler launchScheduler,
	logger *slog.Logger,
) *Service {
	return &Service{
		appRepo:   appRepo,
		userRepo:  userRepo,
		scheduler: scheduler,
		logger:    logger,
	}
}

// List returns all apps with pagination.
func (s *Service) List(ctx context.Context, limit, offset int) ([]model.App, int64, error) {
	apps, total, err := s.appRepo.List(ctx, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list apps: %w", err)
	}
	return apps, total, nil
}

// ListAccessible returns all apps accessible by a user (public or explicitly granted).
func (s *Service) ListAccessible(ctx context.Context, username string) ([]model.App, error) {
	apps, err := s.appRepo.ListAccessible(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("list accessible apps for user %s: %w", username, err)
	}
	return apps, nil
}

// GetByID returns an app by ID.
func (s *Service) GetByID(ctx context.Context, id uint) (model.App, error) {
	app, err := s.appRepo.GetByID(ctx, id)
	if err != nil {
		return model.App{}, fmt.Errorf("get app %d: %w", id, err)
	}
	return app, nil
}

func (s *Service) GetAccessibleByID(ctx context.Context, id uint, username string) (model.App, error) {
	app, err := s.GetByID(ctx, id)
	if err != nil {
		return model.App{}, fmt.Errorf("get accessible app %d for user %s: %w", id, username, err)
	}
	if app.Public {
		return app, nil
	}
	hasAccess, err := s.appRepo.UserHasAccess(ctx, username, app.ID)
	if err != nil {
		return model.App{}, fmt.Errorf("check access app %d user %s: %w", app.ID, username, err)
	}
	if !hasAccess {
		return model.App{}, common.NewForbiddenError("permission denied")
	}
	return app, nil
}

func (s *Service) Launch(ctx context.Context, appID uint, requestUser string) (map[string]string, error) {
	appInfo, err := s.GetAccessibleByID(ctx, appID, requestUser)
	if err != nil {
		return nil, fmt.Errorf("launch app %d: %w", appID, err)
	}

	if s.scheduler == nil {
		return nil, errors.New("scheduler not configured")
	}
	return s.scheduler.SubmitAppJob(ctx, &appInfo, requestUser)
}

// Create creates a new app from writable input fields.
func (s *Service) Create(ctx context.Context, form model.AppBase) (model.App, error) {
	if err := validateReqAppForm(form); err != nil {
		return model.App{}, err
	}
	var app model.App
	applyAppForm(&app, form)
	if err := s.appRepo.Create(ctx, &app); err != nil {
		return model.App{}, mapAppStoreError("create", err)
	}
	return app, nil
}

// Update updates an existing app with writable input fields only.
func (s *Service) Update(ctx context.Context, id uint, form model.AppBase) (model.App, error) {
	if err := validateReqAppForm(form); err != nil {
		return model.App{}, err
	}
	app, err := s.appRepo.GetByID(ctx, id)
	if err != nil {
		return model.App{}, fmt.Errorf("update app %d: %w", id, err)
	}
	applyAppForm(&app, form)
	if err := s.appRepo.Update(ctx, &app); err != nil {
		return model.App{}, mapAppStoreError("update", err)
	}
	return app, nil
}

// Delete deletes an app by ID.
func (s *Service) Delete(ctx context.Context, id uint) error {
	if err := s.appRepo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete app %d: %w", id, err)
	}
	return nil
}

// SetUsers replaces the entire user list for an app.
func (s *Service) SetUsers(ctx context.Context, appID uint, usernames []string) error {
	users, err := s.resolveUsers(ctx, usernames)
	if err != nil {
		return err
	}
	if err := s.appRepo.SetUsers(ctx, appID, users); err != nil {
		return fmt.Errorf("set users for app %d: %w", appID, err)
	}
	return nil
}

// AddUsers adds multiple users to an app.
func (s *Service) AddUsers(ctx context.Context, appID uint, usernames []string) error {
	users, err := s.resolveUsers(ctx, usernames)
	if err != nil {
		return err
	}
	if err := s.appRepo.AddUsers(ctx, appID, users); err != nil {
		return fmt.Errorf("add users to app %d: %w", appID, err)
	}
	return nil
}

// RemoveUsers removes multiple users from an app.
func (s *Service) RemoveUsers(ctx context.Context, appID uint, usernames []string) error {
	users, err := s.resolveUsers(ctx, usernames)
	if err != nil {
		return err
	}
	if err := s.appRepo.RemoveUsers(ctx, appID, users); err != nil {
		return fmt.Errorf("remove users from app %d: %w", appID, err)
	}
	return nil
}

// ListUsers returns all users linked to an app.
func (s *Service) ListUsers(ctx context.Context, appID uint) ([]string, error) {
	users, err := s.appRepo.ListAuthorizedUsers(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("list users for app %d: %w", appID, err)
	}
	return users, nil
}

func (s *Service) resolveUsers(ctx context.Context, usernames []string) ([]model.User, error) {
	if len(usernames) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(usernames))
	var uniqueNames []string
	for _, username := range usernames {
		normalized := strings.TrimSpace(username)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		uniqueNames = append(uniqueNames, normalized)
	}

	if len(uniqueNames) == 0 {
		return nil, nil
	}

	users, err := s.userRepo.GetByNames(ctx, uniqueNames)
	if err != nil {
		return nil, fmt.Errorf("resolve users: %w", err)
	}

	// Check if all requested users were found to preserve strict behavior
	if len(users) != len(uniqueNames) {
		found := make(map[string]bool, len(users))
		for _, u := range users {
			found[u.Username] = true
		}
		for _, name := range uniqueNames {
			if !found[name] {
				return nil, fmt.Errorf("user %q: %w", name, common.ErrNotFound)
			}
		}
	}

	return users, nil
}

func mapAppStoreError(op string, err error) error {
	if errors.Is(err, db.ErrAppNameExists) {
		return common.NewConflictError("app name already exists", ErrAppNameConflict)
	}
	return fmt.Errorf("%s app: %w", op, err)
}
