package user

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/platform/crypto"
	"design-cloud-engine/internal/server/cache"
	userprovider "design-cloud-engine/internal/server/service/user/provider"

	"design-cloud-engine/internal/contract"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sync/singleflight"
)

// Service is the user management service.
// All auth-mode-specific logic is handled by the embedded userProvider.
type Service struct {
	provider                     userprovider.Provider // primary: login / listing / password flows
	localProv                    userprovider.Provider // ID-mapping resolution base (nil = platform has no local passwd/group actions)
	ldapProv                     userprovider.Provider // ID-mapping resolution overlay (nil = AuthMode != ldap)
	logger                       *slog.Logger
	cfg                          Config
	userRepo                     *db.UserRepo
	leaseRepo                    *db.TaskLeaseRepo
	cacheVersion                 *db.CacheVersionRepo
	auditSvc                     *audit.Service
	authSettings                 AuthSettingsReader
	idMappingCache               *idMappingCache
	invalidateSessionsByUsername func(string)
	deleteSessionsByUsername     func(context.Context, string) error
	hotAssignees                 hotAssigneeCache
	hotAssigneesSF               singleflight.Group
	syncTaskMu                   sync.Mutex
	syncTaskCtx                  context.Context
	syncTasksStopped             bool
	syncTasks                    sync.WaitGroup
}

type hotAssigneeCache struct {
	mu     sync.RWMutex
	loaded time.Time
	users  []model.User
}

const hotAssigneeCacheTTL = 2 * time.Minute

const HotAssigneeDefaultLimit = 10

type Config struct {
	AuthMode         config.UserAuthMode
	SyncLeaseOwnerID string
	LDAP             config.LdapConfig
	EnableThreeRoles bool
}

// NewService creates a user service with the appropriate auth backend.
// sessionRevoker is the shared session-effect component (session.Evictor); it is
// optional — nil means the service cannot trigger session evictions for its
// intra-aggregate flows (password change, scopes, LDAP sync), which is acceptable
// for unit tests. Production always injects it up front; nothing is wired after
// construction.
func NewService(execSvc execution.Executor, cfg Config, authSettings AuthSettingsReader, userRepo *db.UserRepo, leaseRepo *db.TaskLeaseRepo, cacheVersion *db.CacheVersionRepo, invalidator *cache.Invalidator, logger *slog.Logger, auditSvc *audit.Service, sessionRevoker SessionRevoker) (*Service, error) {
	if authSettings == nil {
		return nil, errors.New("user: auth settings reader is required")
	}
	if userRepo == nil {
		return nil, errors.New("user: user repo is required")
	}
	if leaseRepo == nil {
		return nil, errors.New("user: lease repo is required")
	}
	if logger == nil {
		return nil, errors.New("user: logger is required")
	}
	if auditSvc == nil {
		return nil, errors.New("user: audit service is required")
	}
	bundle, err := userprovider.NewBundle(execSvc, config.User{
		AuthMode: cfg.AuthMode,
		Ldap:     cfg.LDAP,
	}, userRepo,
		logger)
	if err != nil {
		return nil, err
	}

	ic := newIDMappingCache()
	s := &Service{
		provider:       bundle.Primary,
		localProv:      bundle.Local,
		ldapProv:       bundle.LDAP,
		logger:         logger,
		cfg:            cfg,
		userRepo:       userRepo,
		leaseRepo:      leaseRepo,
		cacheVersion:   cacheVersion,
		auditSvc:       auditSvc,
		authSettings:   authSettings,
		idMappingCache: ic,
	}
	if sessionRevoker != nil {
		s.invalidateSessionsByUsername = sessionRevoker.InvalidateByUsername
		s.deleteSessionsByUsername = sessionRevoker.Delete
	}
	if invalidator != nil {
		invalidator.Register(idMappingCacheTopic, func() {
			ic.Clear()
		})
	}
	return s, nil
}

// Delete removes a portal user row only. Cross-domain cleanup (connection rows,
// proxy sessions, portal sessions) is orchestrated by the identity application layer
// (identity.DeleteUser) before this atomic delete.
func (s *Service) Delete(ctx context.Context, username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return contract.NewInvalidParamError("username is required")
	}
	if _, err := s.userRepo.GetByName(ctx, username); err != nil {
		return err
	}
	return s.userRepo.Delete(ctx, username)
}

type AdminUserUpdateRequest struct {
	Status         *int  `json:"status"`
	DownloadAccess *bool `json:"downloadAccess"`
	UploadAccess   *bool `json:"uploadAccess"`
	MaxConnections *uint `json:"maxConnections"` // nil=no change, 0=inherit global, >0=custom quota (1–100)
}

// InitAdminAccounts ensures default admin accounts exist and enforces role exclusivity based on configuration.
// enableThreeRoles=true: three-admin mode (audit/sysadmin/security). Superadmin is disabled.
// enableThreeRoles=false: single-admin mode (superadmin). Three-admins are disabled.
// Passwords are randomly generated per instance and printed to the startup log.
// Initial password is only valid for the first login (status: UserPasswordExpired).
func (s *Service) InitAdminAccounts(ctx context.Context, enableThreeRoles bool) error {
	type adminAccount struct {
		name string
		role model.Role
	}

	var accounts []adminAccount
	var rolesToDisable []model.Role

	if enableThreeRoles {
		accounts = []adminAccount{
			{name: "securityadmin", role: model.RoleSecurityAdmin},
			{name: "systemadmin", role: model.RoleSystemAdmin},
			{name: "auditadmin", role: model.RoleAuditAdmin},
		}
		rolesToDisable = []model.Role{model.RoleSuperAdmin}
	} else {
		accounts = []adminAccount{
			{name: "dceadmin", role: model.RoleSuperAdmin},
		}
		rolesToDisable = []model.Role{
			model.RoleSecurityAdmin,
			model.RoleSystemAdmin,
			model.RoleAuditAdmin,
		}
	}

	// 1. Create or re-enable missing admins
	for _, a := range accounts {
		existing, err := s.userRepo.GetByName(ctx, a.name)
		if err == nil {
			if existing.Status == model.UserDisabled {
				if err := s.userRepo.ChangeStatus(ctx, a.name, model.UserPasswordExpired); err != nil {
					return fmt.Errorf("re-enable admin user %q: %w", a.name, err)
				}
				s.logger.Info("Re-enabled admin user", "username", a.name, "role", a.role)
			}
			continue
		}
		if !errors.Is(err, contract.ErrNotFound) {
			return fmt.Errorf("check admin user %q: %w", a.name, err)
		}

		rawPwd := crypto.GenSecret(16)
		hashed, err := hashPassword(rawPwd)
		if err != nil {
			return fmt.Errorf("hash password for %q: %w", a.name, err)
		}

		user := model.User{
			Username: a.name,
			Password: hashed,
			Role:     a.role,
			Status:   model.UserPasswordExpired, // 强制首次登录改密
			Type:     model.UserTypeApp,
		}
		if err := s.userRepo.Create(ctx, &user); err != nil {
			return fmt.Errorf("create admin user %q: %w", a.name, err)
		}
		s.logger.Info("Created admin user", "username", a.name, "role", a.role, "password", rawPwd)
	}

	// 2. Disable conflicting admins
	if err := s.userRepo.UpdateUserStatusByRoles(ctx, rolesToDisable, model.UserDisabled); err != nil {
		return fmt.Errorf("disable conflicting admin roles: %w", err)
	}

	return nil
}

// hashPassword hashes an app-user password with bcrypt for DB storage.
func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// GetMaxConnections returns the per-user connection limit override.
// Returns 0 when the user has no custom limit (inherits global).
func (s *Service) GetMaxConnections(ctx context.Context, username string) (uint, error) {
	user, err := s.userRepo.GetByName(ctx, username)
	if err != nil {
		return 0, fmt.Errorf("get user %q for max connections: %w", username, err)
	}
	return user.MaxConnections, nil
}

// Get returns a single user's information.
// App users (local-only accounts) are returned from the app DB directly;
// system users are resolved through the provider (getent/LDAP).
func (s *Service) Get(ctx context.Context, username string) (*userprovider.SystemUser, error) {
	if dbUser, err := s.userRepo.GetByName(ctx, username); err == nil && dbUser.Type == model.UserTypeApp {
		return &userprovider.SystemUser{
			ID:       dbUser.ID,
			Username: dbUser.Username,
			Status:   model.UserStatusString(dbUser.Status),
		}, nil
	}
	return s.provider.GetUser(ctx, username)
}

// List returns users from the database with pagination.
// q optionally filters by username or lastLoginIP (case-insensitive substring).
func (s *Service) List(ctx context.Context, limit, offset int, q string) ([]model.User, int64, error) {
	users, total, err := s.userRepo.List(ctx, limit, offset, q)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	return users, total, nil
}

// SearchAssignees returns up to limit active normal users whose username
// contains q (case-insensitive), usernames starting with q first.
func (s *Service) SearchAssignees(ctx context.Context, q string, limit int) ([]model.User, error) {
	users, err := s.userRepo.SearchAssignees(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("search assignees: %w", err)
	}
	return users, nil
}

// HotAssignees returns recently assigned normal users from a short-lived cache.
func (s *Service) HotAssignees(ctx context.Context, limit int) ([]model.User, error) {
	if limit <= 0 {
		limit = HotAssigneeDefaultLimit
	}
	now := time.Now()
	s.hotAssignees.mu.RLock()
	if !s.hotAssignees.loaded.IsZero() && now.Sub(s.hotAssignees.loaded) < hotAssigneeCacheTTL {
		users := clipUsers(s.hotAssignees.users, limit)
		s.hotAssignees.mu.RUnlock()
		return users, nil
	}
	s.hotAssignees.mu.RUnlock()

	_, err, _ := s.hotAssigneesSF.Do("load", func() (any, error) {
		s.hotAssignees.mu.RLock()
		if !s.hotAssignees.loaded.IsZero() && time.Since(s.hotAssignees.loaded) < hotAssigneeCacheTTL {
			s.hotAssignees.mu.RUnlock()
			return nil, nil
		}
		s.hotAssignees.mu.RUnlock()

		users, err := s.userRepo.ListHotAssignees(ctx, HotAssigneeDefaultLimit)
		if err != nil {
			return nil, fmt.Errorf("hot assignees: %w", err)
		}
		s.hotAssignees.mu.Lock()
		s.hotAssignees.users = users
		s.hotAssignees.loaded = time.Now()
		s.hotAssignees.mu.Unlock()
		return users, nil
	})
	if err != nil {
		return nil, err
	}

	s.hotAssignees.mu.RLock()
	out := clipUsers(s.hotAssignees.users, limit)
	s.hotAssignees.mu.RUnlock()
	return out, nil
}

func clipUsers(users []model.User, limit int) []model.User {
	if len(users) <= limit {
		return users
	}
	return users[:limit]
}

// GetGroupMembers returns members of a specific group.
func (s *Service) GetGroupMembers(ctx context.Context, groupName string) ([]string, error) {
	return s.provider.GetGroupMembers(ctx, groupName)
}

// ListUserGroups returns groups of all users.
func (s *Service) ListUserGroups(ctx context.Context) (map[string][]string, error) {
	return s.provider.ListUserGroups(ctx)
}

// VerifyPassword delegates password verification to the underlying provider.
func (s *Service) VerifyPassword(ctx context.Context, username, password string) error {
	return s.provider.VerifyPassword(ctx, username, password)
}

// SyncUserLogin ensures an authenticated user has a DB record
// and updates the last successful login metadata.
func (s *Service) SyncUserLogin(ctx context.Context, username, ip string) (*model.User, error) {
	user, err := s.userRepo.GetByName(ctx, username)

	if errors.Is(err, contract.ErrNotFound) {
		user = model.User{
			Username:    username,
			Status:      model.UserActive,
			Type:        model.UserTypeSystem,
			LastLoginIP: ip,
			LastLoginAt: time.Now(),
		}
		if err := s.userRepo.Create(ctx, &user); err != nil {
			return nil, fmt.Errorf("create user %q on login sync: %w", username, err)
		}
		return &user, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user %q on login sync: %w", username, err)
	}

	user.LastLoginIP = ip
	user.LastLoginAt = time.Now()
	if err := s.userRepo.Update(ctx, &user); err != nil {
		return nil, fmt.Errorf("update user %q on login sync: %w", username, err)
	}
	return &user, nil
}

// GetDBUser returns the persisted user model for lower-level service integrations.
func (s *Service) GetDBUser(ctx context.Context, username string) (model.User, error) {
	user, err := s.userRepo.GetByName(ctx, username)
	if err != nil {
		return model.User{}, fmt.Errorf("get db user %q: %w", username, err)
	}
	return user, nil
}

// GetDBUserByID returns the persisted user model by primary key.
func (s *Service) GetDBUserByID(ctx context.Context, id uint) (model.User, error) {
	user, err := s.userRepo.GetByID(ctx, id)
	if err != nil {
		return model.User{}, fmt.Errorf("get db user %d: %w", id, err)
	}
	return user, nil
}

// checkAdminTargetProtection validates if the operator is allowed to manage the target user.
// - Normal sub-admins (single-admin mode) must not be able to modify SuperAdmin or other scoped users.
// - In three-role mode, SystemAdmin can reset anyone (including other SystemAdmins), except themselves.
func (s *Service) checkAdminTargetProtection(operator *model.Session, target *model.User) error {
	// Operator cannot modify their own account using admin interfaces.
	if operator.Username == target.Username {
		return fmt.Errorf("cannot modify your own account: %w", ErrForbidden)
	}

	// SuperAdmin can modify anyone else
	if operator.Role == model.RoleSuperAdmin {
		return nil
	}

	if s.cfg.EnableThreeRoles {
		// Three-role mode: no one can modify SuperAdmin (if one existed, though in 3-role mode they don't log in, but still protect it).
		if target.Role == model.RoleSuperAdmin {
			return fmt.Errorf("cannot modify superadmin: %w", ErrForbidden)
		}
		// SystemAdmin is allowed to modify other roles
		return nil
	}

	// Single-admin mode: normal scoped admins
	if target.Role == model.RoleSuperAdmin || target.AdminScopes != 0 {
		return fmt.Errorf("cannot modify an admin user: %w", ErrForbidden)
	}

	return nil
}

func (s *Service) Update(ctx context.Context, user *model.User) error {
	err := s.userRepo.Update(ctx, user)
	if err != nil {
		return fmt.Errorf("update user %q: %w", user.Username, err)
	}
	if s.invalidateSessionsByUsername != nil && user != nil && user.Username != "" {
		s.invalidateSessionsByUsername(user.Username)
	}
	s.auditSvc.LogUser(ctx, "USER_UPDATE", "success", user.Username, slog.LevelInfo, nil)
	return nil
}

func (s *Service) ChangeStatus(ctx context.Context, username string, newStatus model.UserStatusCode) error {
	err := s.userRepo.ChangeStatus(ctx, username, newStatus)
	if err != nil {
		return fmt.Errorf("change status of user %q to %d: %w", username, newStatus, err)
	}
	if s.deleteSessionsByUsername != nil && newStatus != model.UserActive {
		if err := s.deleteSessionsByUsername(ctx, username); err != nil {
			s.logger.Warn("failed to delete sessions after user status change",
				"username", username, "new_status", newStatus, "error", err)
		}
	}
	return nil
}

// AdminResetPassword forces a password reset for a user (Admin action).
func (s *Service) AdminResetPassword(ctx context.Context, operator *model.Session, username string) (string, error) {
	dbUser, err := s.userRepo.GetByName(ctx, username)
	if err != nil && !errors.Is(err, contract.ErrNotFound) {
		return "", fmt.Errorf("get user %q for password reset: %w", username, err)
	}

	// If the user exists in DB, perform target protection check.
	// If the user does not exist in DB yet (e.g. OS/LDAP user being stubbed for the first time), bypass it since they are not an admin.
	if dbUser.ID != 0 {
		if err := s.checkAdminTargetProtection(operator, &dbUser); err != nil {
			return "", err
		}
	} else {
		// Just ensure operator is not trying to reset themselves if they somehow don't exist in DB (unlikely)
		if operator.Username == username {
			return "", fmt.Errorf("cannot modify your own account: %w", ErrForbidden)
		}
	}

	newPassword := crypto.GenSecret(16)

	if dbUser.Type == model.UserTypeApp {
		hashedPwd, err := hashPassword(newPassword)
		if err != nil {
			return "", err
		}
		dbUser.Password = hashedPwd
		if err := s.persistPasswordResetState(ctx, &dbUser, dbUser.Username, model.UserTypeApp); err != nil {
			return "", err
		}

		s.auditSvc.LogUser(ctx, "USER_PASSWORD_RESET", "success", dbUser.Username, slog.LevelInfo, map[string]any{
			"user_type": "app",
		})
		return newPassword, nil
	}

	if err := s.provider.ResetPassword(ctx, username, newPassword); err != nil {
		return "", err
	}

	if err := s.persistPasswordResetState(ctx, &dbUser, username, model.UserTypeSystem); err != nil {
		return "", err
	}

	s.auditSvc.LogUser(ctx, "USER_PASSWORD_RESET", "success", username, slog.LevelInfo, map[string]any{
		"user_type": "system",
	})
	return newPassword, nil
}

func (s *Service) persistPasswordResetState(ctx context.Context, dbUser *model.User, username string, userType model.UserType) error {
	if dbUser.ID == 0 {
		*dbUser = model.User{
			Username: username,
			Status:   model.UserPasswordExpired,
			Type:     userType,
		}
		if err := s.userRepo.Create(ctx, dbUser); err != nil {
			return fmt.Errorf("create user %q for password reset state: %w", username, err)
		}
	} else {
		dbUser.Status = model.UserPasswordExpired
		if err := s.userRepo.Update(ctx, dbUser); err != nil {
			return fmt.Errorf("update user %q for password reset state: %w", username, err)
		}
	}

	if s.deleteSessionsByUsername != nil {
		_ = s.deleteSessionsByUsername(ctx, username)
	}

	return nil
}

// ChangePassword allows a user to change their own password.
func (s *Service) ChangePassword(ctx context.Context, username, oldPassword, newPassword string) error {
	dbUser, err := s.userRepo.GetByName(ctx, username)
	if err != nil {
		return fmt.Errorf("get user %q for password change: %w", username, err)
	}

	if dbUser.Type == model.UserTypeApp {
		return s.changeAppUserPassword(ctx, &dbUser, oldPassword, newPassword)
	}
	if err := s.provider.ChangePassword(ctx, username, oldPassword, newPassword); err != nil {
		return err
	}
	if dbUser.Status == model.UserPasswordExpired {
		if err := s.ChangeStatus(ctx, username, model.UserActive); err != nil {
			s.logger.Error("failed to update user status after password change", "username", username, "error", err)
			s.auditSvc.LogUser(ctx, "USER_STATUS_UPDATE", "failed", username, slog.LevelWarn, map[string]any{
				"status": model.UserActive,
				"error":  err.Error(),
			})
		}
	}

	s.auditSvc.LogUser(ctx, "USER_PASSWORD_CHANGE", "success", username, slog.LevelInfo, nil)
	if err := s.evictUserSessions(ctx, username); err != nil {
		s.logger.Warn("failed to evict sessions after password change", "username", username, "error", err)
	}
	return nil
}

func (s *Service) changeAppUserPassword(ctx context.Context, dbUser *model.User, oldPassword, newPassword string) error {
	if dbUser == nil {
		return userprovider.ErrUserNotFound
	}
	if dbUser.Status != model.UserActive && dbUser.Status != model.UserPasswordExpired {
		return userprovider.ErrUserNotActive
	}
	if err := bcrypt.CompareHashAndPassword([]byte(dbUser.Password), []byte(oldPassword)); err != nil {
		return userprovider.ErrUserPasswordIncorrect
	}
	if err := bcrypt.CompareHashAndPassword([]byte(dbUser.Password), []byte(newPassword)); err == nil {
		return userprovider.ErrUserPasswordSameAsOld
	}
	if err := validatePasswordPolicy(newPassword); err != nil {
		return err
	}

	hashedPassword, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	dbUser.Password = hashedPassword
	if err := s.userRepo.Update(ctx, dbUser); err != nil {
		return fmt.Errorf("update user %q after password change: %w", dbUser.Username, err)
	}
	if dbUser.Status == model.UserPasswordExpired || dbUser.Status == model.UserPasswordNotSet {
		if err := s.ChangeStatus(ctx, dbUser.Username, model.UserActive); err != nil {
			s.logger.Error("failed to update app user status after password change", "username", dbUser.Username, "error", err)
			s.auditSvc.LogUser(ctx, "USER_STATUS_UPDATE", "failed", dbUser.Username, slog.LevelWarn, map[string]any{
				"status": model.UserActive,
				"error":  err.Error(),
			})
		}
	}

	s.auditSvc.LogUser(ctx, "APP_USER_PASSWORD_CHANGE", "success", dbUser.Username, slog.LevelInfo, nil)
	if err := s.evictUserSessions(ctx, dbUser.Username); err != nil {
		s.logger.Warn("failed to evict sessions after password change", "username", dbUser.Username, "error", err)
	}
	return nil
}

// AdminUpdate updates admin-managed mutable fields for a user (atomic write only).
// Session eviction on status change is orchestrated by the identity application layer
// (identity.UpdateUserStatus), not by this method.
func (s *Service) AdminUpdate(ctx context.Context, operator *model.Session, username string, req AdminUserUpdateRequest) (*model.User, error) {
	dbUser, err := s.userRepo.GetByName(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("get user %q for admin update: %w", username, err)
	}

	if err := s.checkAdminTargetProtection(operator, &dbUser); err != nil {
		return nil, err
	}

	updates := make(map[string]any)

	if req.DownloadAccess != nil {
		updates["download_access"] = *req.DownloadAccess
		dbUser.DownloadAccess = *req.DownloadAccess
	}
	if req.UploadAccess != nil {
		updates["upload_access"] = *req.UploadAccess
		dbUser.UploadAccess = *req.UploadAccess
	}
	if req.Status != nil {
		status := model.UserStatusCode(*req.Status)
		if status < model.UserActive || status > model.UserLocked {
			return nil, ErrInvalidUserStatus
		}
		if dbUser.Status != status {
			updates["status"] = status
			dbUser.Status = status
			s.auditSvc.LogUser(ctx, "USER_STATUS_UPDATE", "success", dbUser.Username, slog.LevelInfo, map[string]any{
				"status": status,
			})
		}
	}
	if req.MaxConnections != nil {
		v := *req.MaxConnections
		if v > 100 {
			return nil, contract.NewValidationError("maxConnections must be 0 (inherit global) or 1–100")
		}

		// 0 = inherit global, >0 = custom quota
		updates["max_connections"] = v
		dbUser.MaxConnections = v
	}

	if err := s.userRepo.UpdateFields(ctx, username, updates); err != nil {
		return nil, fmt.Errorf("update fields for user %q: %w", username, err)
	}

	return &dbUser, nil
}

// ScopedUser represents a user with admin scopes.
type ScopedUser struct {
	Username    string            `json:"username"`
	AdminScopes model.AdminScopes `json:"adminScopes"`
}

// SetScopes sets the admin scopes for a normal user and deletes stale sessions.
func (s *Service) SetScopes(ctx context.Context, username string, scopes model.AdminScopes) error {
	if s.cfg.EnableThreeRoles && scopes != 0 {
		return ErrAdminScopeDisabledInThreeRoles
	}

	dbUser, err := s.userRepo.GetByName(ctx, username)
	if err != nil {
		return fmt.Errorf("get user %q for set scopes: %w", username, err)
	}
	if err := validateScopes(scopes); err != nil {
		return err
	}
	if dbUser.Role != model.RoleNormal && scopes != 0 {
		return fmt.Errorf("%w: %s", ErrScopeRequiresNormalRole, dbUser.Role.String())
	}
	if dbUser.AdminScopes == scopes {
		return nil
	}
	if scopes == 0 {
		if err := s.userRepo.UpdateFields(ctx, username, map[string]any{"admin_scopes": scopes}); err != nil {
			return fmt.Errorf("clear scopes for user %q: %w", username, err)
		}
	} else {
		updated, err := s.userRepo.UpdateScopesIfRole(ctx, username, model.RoleNormal, scopes)
		if err != nil {
			return fmt.Errorf("update scopes for user %q: %w", username, err)
		}
		if !updated {
			current, err := s.userRepo.GetByName(ctx, username)
			if err != nil {
				return fmt.Errorf("get user %q after scope update race: %w", username, err)
			}
			return fmt.Errorf("%w: %s", ErrScopeRequiresNormalRole, current.Role.String())
		}
	}
	if err := s.evictUserSessions(ctx, username); err != nil {
		return fmt.Errorf("delete sessions after admin scope update: %w", err)
	}
	s.auditSvc.LogUser(ctx, "USER_ADMIN_SCOPE_SET", "success", username, slog.LevelInfo, map[string]any{
		"admin_scopes": int64(scopes),
	})
	return nil
}

func (s *Service) SetScopesByNames(ctx context.Context, username string, scopeNames []string) error {
	scopes, err := ParseScopes(scopeNames)
	if err != nil {
		return err
	}
	return s.SetScopes(ctx, username, scopes)
}

func (s *Service) evictUserSessions(ctx context.Context, username string) error {
	if s.deleteSessionsByUsername != nil {
		return s.deleteSessionsByUsername(ctx, username)
	}
	if s.invalidateSessionsByUsername != nil {
		s.invalidateSessionsByUsername(username)
	}
	return nil
}

// ListScopes returns all users with non-zero AdminScopes.
func (s *Service) ListScopes(ctx context.Context) ([]ScopedUser, error) {
	if s.cfg.EnableThreeRoles {
		return []ScopedUser{}, nil
	}

	users, err := s.userRepo.ListScoped(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users for scopes: %w", err)
	}
	result := make([]ScopedUser, 0, len(users))
	for _, u := range users {
		result = append(result, ScopedUser{
			Username:    u.Username,
			AdminScopes: u.AdminScopes,
		})
	}
	return result, nil
}

// Helper methods (used only internally)

// validatePasswordPolicy checks basic password strength.
// More sophisticated rules (complexity, history, rotation) should be handled
// by the upstream auth provider.
func validatePasswordPolicy(password string) error {
	const minLen = 6
	const maxLen = 128

	pwdLen := len(password)
	if pwdLen < minLen || pwdLen > maxLen {
		return fmt.Errorf("%w: password must be %d–%d characters", ErrPasswordPolicy, minLen, maxLen)
	}

	lower := strings.ToLower(password)

	if isWeakPassword(lower) {
		return fmt.Errorf("%w: password is too common", ErrPasswordPolicy)
	}

	// Not all same character (e.g. "aaaaaa", "111111")
	if allSame(password) {
		return fmt.Errorf("%w: password must not consist of a single repeated character", ErrPasswordPolicy)
	}

	// Not all digits
	if isAllDigits(password) {
		return fmt.Errorf("%w: password must not be entirely numeric", ErrPasswordPolicy)
	}

	return nil
}

// allSame reports whether every rune in s is identical.
func allSame(s string) bool {
	if len(s) == 0 {
		return false
	}
	first := s[0]
	for i := 1; i < len(s); i++ {
		if s[i] != first {
			return false
		}
	}
	return true
}

// isAllDigits reports whether s contains only decimal digits.
func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
