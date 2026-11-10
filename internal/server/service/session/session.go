package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/crypto"
	"design-cloud-engine/internal/server/push"
	"design-cloud-engine/internal/server/service/file"
	securitysvc "design-cloud-engine/internal/server/service/security"
	"design-cloud-engine/internal/server/service/user"

	"design-cloud-engine/internal/contract"
	"gorm.io/gorm"
)

// Service manages HTTP authentication sessions.
type Service struct {
	sessionRepo *db.SessionRepo
	logger      *slog.Logger
	authSvc     *AuthManager
	userSvc     *user.Service
	settingsSvc SettingsReader
	securitySvc *securitysvc.Service
	cache       *sessionInfoCache
	evictor     *Evictor
	publisher   push.Publisher
}

// NewService creates a new session service.
//
// evictor is the shared cross-domain session-effect component: session and user
// both use the same instance, and this service only borrows its cache. That
// keeps the construction order a DAG (Evictor → User → Session) instead of the
// former user↔session post-construction wiring.
func NewService(logger *slog.Logger, authSvc *AuthManager, userSvc *user.Service, securitySvc *securitysvc.Service, settingsSvc SettingsReader, sessionRepo *db.SessionRepo, evictor *Evictor, publisher push.Publisher) (*Service, error) {
	if logger == nil {
		return nil, fmt.Errorf("session: logger is required")
	}
	if authSvc == nil {
		return nil, fmt.Errorf("session: auth service is required")
	}
	if userSvc == nil {
		return nil, fmt.Errorf("session: user service is required")
	}
	if securitySvc == nil {
		return nil, fmt.Errorf("session: security service is required")
	}
	if settingsSvc == nil {
		return nil, fmt.Errorf("session: settings service is required")
	}
	if sessionRepo == nil {
		return nil, fmt.Errorf("session: session repo is required")
	}
	if evictor == nil {
		return nil, fmt.Errorf("session: session evictor is required")
	}
	if publisher == nil {
		publisher = push.NoopPublisher{}
	}

	return &Service{
		sessionRepo: sessionRepo,
		logger:      logger,
		authSvc:     authSvc,
		userSvc:     userSvc,
		settingsSvc: settingsSvc,
		securitySvc: securitySvc,
		cache:       evictor.cache,
		evictor:     evictor,
		publisher:   publisher,
	}, nil
}

// InvalidateByUsername drops the local cache entries of a user without deleting
// the persisted sessions.
func (s *Service) InvalidateByUsername(username string) {
	s.evictor.InvalidateByUsername(username)
}

func (s *Service) invalidateToken(token string) {
	s.cache.invalidateToken(token)
}

// Close stops the background tasks of the session service.
func (s *Service) Close() {
	// No-op: background sync tasks have been removed.
}

const passwordChangeRequiredSessionTTLSeconds = 5 * 60

// Create authenticates a user and issues a new session token.
func (s *Service) Create(ctx context.Context, username, password string, client LoginClient, enableThreeRoles bool, fileLister FileLocationsLister) (*Payload, error) {
	ip := client.IP
	// 1. System-protected accounts — reject immediately
	if err := s.authSvc.blockRootAndAdmin(ctx, username, ip); err != nil {
		return nil, err
	}

	// 2. Admin mode login restriction — reject roles incompatible with the current mode
	if err := s.checkAdminModeRestriction(ctx, username, enableThreeRoles); err != nil {
		return nil, err
	}

	// 3. Authenticate — ban check + password verification
	authResult, err := s.authSvc.Authenticate(ctx, username, password, ip)
	if err != nil {
		s.logger.Warn("Login failed", "error", err, "username", username, "ip", ip)
		return nil, err
	}

	// 4. Sync user and handle password expiry
	userRecord, err := s.syncUserAndPasswordExpiry(ctx, username, ip, authResult.PasswordMustChange)
	if err != nil {
		return nil, err
	}

	// 5. Load auth settings, enforce session quota, and resolve the TTL.
	// GetAuthSettings already validates and falls back to schema defaults for
	// corrupt stored values; a hard error means settings are unavailable — fail closed.
	authSettings, err := s.settingsSvc.GetAuthSettings(ctx)
	if err != nil {
		s.logger.Error("Failed to load auth settings", "error", err, "username", username)
		return nil, fmt.Errorf("load auth settings: %w", err)
	}
	if err := s.enforceSessionQuota(ctx, userRecord, authSettings); err != nil {
		return nil, err
	}
	sessionTTLSeconds := authSettings.SessionTTL
	if authResult.PasswordMustChange {
		sessionTTLSeconds = passwordChangeRequiredSessionTTLSeconds
	}

	// 6. Create session record
	newSession := newSessionRecordWithTTL(userRecord.ID, username, ip, userRecord.Role, userRecord.Type, userRecord.AdminScopes, sessionTTLSeconds, authResult.PasswordMustChange)
	newSession.Hostname = model.TruncateText(client.Hostname, model.MaxSessionHostnameLength)
	newSession.Platform = model.TruncateText(client.Platform, model.MaxSessionPlatformLength)
	newSession.OSVersion = model.TruncateText(client.OSVersion, model.MaxSessionOSVersionLength)
	newSession.AppVersion = model.TruncateText(client.AppVersion, model.MaxSessionAppVersionLength)
	newSession.DeviceID = model.TruncateText(client.DeviceID, model.MaxSessionDeviceIDLength)
	if err := s.sessionRepo.Create(ctx, &newSession); err != nil {
		s.logger.Error("Save session failed", "error", err, "username", username)
		if db.IsUniqueConstraintError(err) {
			return nil, fmt.Errorf("session token collision, please retry: %w", err)
		}
		return nil, fmt.Errorf("create session: %w", err)
	}

	s.logger.Debug("Login success",
		"username", username,
		"ip", ip)

	payload := s.BuildSessionContextPayload(ctx, &newSession, fileLister, ip, authResult.PasswordMustChange)
	payload.MultiSessionEnabled = multiSessionEnabled(authSettings)
	// System users carry their LDAP password expiration; app users have none.
	if u, err := s.userSvc.Get(ctx, username); err == nil {
		payload.PasswordExpiresAt = u.PasswordExpiresAt
	}
	return payload, nil
}

// checkAdminModeRestriction rejects logins whose role is incompatible with the
// current admin mode. Lightweight DB lookup before expensive auth; LDAP
// auto-provisioned users default to Normal(0) which never triggers this check.
// NOTE: fail-closed — DB errors (e.g. transient outage) deny login rather than
// silently bypassing the restriction check.
func (s *Service) checkAdminModeRestriction(ctx context.Context, username string, enableThreeRoles bool) error {
	dbUser, err := s.userSvc.GetDBUser(ctx, username)
	if err == nil {
		if enableThreeRoles {
			if dbUser.Role == model.RoleSuperAdmin {
				return ErrAdminModeRestriction
			}
		} else {
			if dbUser.Role == model.RoleAuditAdmin || dbUser.Role == model.RoleSystemAdmin || dbUser.Role == model.RoleSecurityAdmin {
				return ErrAdminModeRestriction
			}
		}
	} else if !errors.Is(err, contract.ErrNotFound) {
		// Unexpected DB error (transient outage, connection failure, etc.)
		s.logger.Warn("admin-mode check failed, denying login", "error", err, "username", username)
		return fmt.Errorf("admin-mode precheck failed: %w", err)
	}
	// ErrNotFound — user not yet provisioned in DB (LDAP auto-provision path);
	// fall through to authentication which will create the record.
	return nil
}

// syncUserAndPasswordExpiry syncs the login user record and applies the
// password-expired status when the authenticator requires a change.
func (s *Service) syncUserAndPasswordExpiry(ctx context.Context, username, ip string, passwordMustChange bool) (*model.User, error) {
	userRecord, err := s.userSvc.SyncUserLogin(ctx, username, ip)
	if err != nil {
		s.logger.Error("Failed to sync login user", "error", err, "username", username)
		return nil, fmt.Errorf("failed to sync login user: %w", err)
	}
	if passwordMustChange && userRecord.Status != model.UserPasswordExpired {
		if err := s.userSvc.ChangeStatus(ctx, username, model.UserPasswordExpired); err != nil {
			s.logger.Error("Failed to mark user password expired", "error", err, "username", username)
			return nil, fmt.Errorf("failed to update user status: %w", err)
		}
		userRecord.Status = model.UserPasswordExpired
	} else if !passwordMustChange && userRecord.Status == model.UserPasswordExpired {
		// LDAP/OS auth succeeded (e.g. admin reset password in IPA); clear stale portal DB state.
		if err := s.userSvc.ChangeStatus(ctx, username, model.UserActive); err != nil {
			s.logger.Error("Failed to clear stale password-expired status", "error", err, "username", username)
			return nil, fmt.Errorf("failed to update user status: %w", err)
		}
		userRecord.Status = model.UserActive
	}
	return userRecord, nil
}

// multiSessionEnabled reports whether one user may hold several sessions at once.
func multiSessionEnabled(authSettings schema.AuthSettings) bool {
	return authSettings.MaxSessionsPerUser > 1
}

// MultiSessionEnabled loads auth settings and reports whether multiple sessions per user are allowed.
func (s *Service) MultiSessionEnabled(ctx context.Context) (bool, error) {
	authSettings, err := s.settingsSvc.GetAuthSettings(ctx)
	if err != nil {
		return false, fmt.Errorf("load auth settings: %w", err)
	}
	return multiSessionEnabled(authSettings), nil
}

// enforceSessionQuota trims the user's oldest sessions when the active count
// exceeds MaxSessionsPerUser. No-op when the quota is disabled (<= 0).
func (s *Service) enforceSessionQuota(ctx context.Context, userRecord *model.User, authSettings schema.AuthSettings) error {
	maxSessions := authSettings.MaxSessionsPerUser
	if maxSessions <= 0 {
		return nil
	}
	count, err := s.sessionRepo.CountByUser(ctx, userRecord.ID)
	if err != nil {
		return fmt.Errorf("count active sessions: %w", err)
	}
	if count < int64(maxSessions) {
		return nil
	}
	oldSessions, err := s.sessionRepo.GetByUserID(ctx, userRecord.ID)
	if err != nil {
		return fmt.Errorf("load active sessions: %w", err)
	}
	for i := range len(oldSessions) - maxSessions + 1 {
		if err := s.sessionRepo.DeleteByIDs(ctx, []uint{oldSessions[i].ID}); err != nil {
			return fmt.Errorf("clear old session: %w", err)
		}
		s.invalidateToken(oldSessions[i].Token)
	}
	return nil
}

func (s *Service) Renew(ctx context.Context, sessionToken string) (*model.Session, error) {
	current, err := s.GetByToken(ctx, sessionToken)
	if err != nil {
		return nil, err
	}
	if time.Now().After(current.ExpiresAt) {
		_ = s.DeleteByAccessToken(ctx, sessionToken)
		return nil, ErrExpiredSession
	}
	userRecord, err := s.userSvc.GetDBUser(ctx, current.Username)
	if err != nil {
		return nil, err
	}
	if userRecord.Status == model.UserPasswordExpired {
		return nil, ErrPasswordChangeRequired
	}
	now := time.Now()
	ttlSec, err := s.sessionTTLSeconds(ctx)
	if err != nil {
		return nil, err
	}
	ttl := time.Duration(ttlSec) * time.Second

	// 社区标准滑动窗口 (Sliding Expiration Window)：
	// 仅当会话剩余有效时间小于总 TTL 的一半时（进入滑动刷新窗口），才触发真实 DB 写入；
	// 前半程周期内的 Renew 请求直接返回当前会话内存快照，完全免除无谓的 SQL UPDATE。
	if current.ExpiresAt.Sub(now) > ttl/2 {
		return current, nil
	}

	refreshed := *current
	refreshed.ExpiresAt = now.Add(ttl)
	refreshed.UpdatedAt = now

	if err := s.sessionRepo.Renew(ctx, refreshed.ID, refreshed.ExpiresAt); err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			s.cache.invalidateToken(sessionToken)
			return nil, ErrInvalidSessionToken
		}
		return nil, fmt.Errorf("renew session %d: %w", refreshed.ID, err)
	}
	s.cache.put(&refreshed)

	return &refreshed, nil
}

// GetByToken 通过给定的token查找是否存在对应的session
func (s *Service) GetByToken(ctx context.Context, token string) (*model.Session, error) {
	if info, ok := s.cache.get(token); ok {
		if time.Now().Before(info.ExpiresAt) {
			return info, nil
		}
		s.cache.invalidateToken(token)
	}
	session, err := s.sessionRepo.GetByToken(ctx, token)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return nil, ErrInvalidSessionToken
		}
		return nil, fmt.Errorf("get session by token: %w", err)
	}
	s.cache.put(session)
	return session, nil
}

// bumpSessionInvalidation bumps the session invalidation cache inside the given
// transaction. It is the single shared implementation of the invalidation
// closure previously duplicated across the Delete* methods.
func (s *Service) bumpSessionInvalidation(ctx context.Context, tx *gorm.DB) error {
	if s.evictor != nil && s.evictor.invalidator != nil {
		return s.evictor.invalidator.BumpTx(ctx, tx, db.CacheVersionTopicSessions)
	}
	return nil
}

// Delete removes every persisted session of the user through the shared Evictor,
// so user-initiated and session-initiated revocation share one implementation.
func (s *Service) Delete(ctx context.Context, username string) error {
	return s.evictor.Delete(ctx, username)
}

func (s *Service) DeleteByIDs(ctx context.Context, sessionIDs ...uint) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	sessions, err := s.sessionRepo.GetByIDs(ctx, sessionIDs)
	if err != nil {
		return fmt.Errorf("get sessions for delete: %w", err)
	}
	if len(sessions) == 0 {
		return nil
	}

	actualIDs := make([]uint, 0, len(sessions))
	usernames := make(map[string]uint, len(sessions))
	for _, sess := range sessions {
		actualIDs = append(actualIDs, sess.ID)
		usernames[sess.Username] = sess.UserID
		s.invalidateToken(sess.Token)
	}

	if err := s.sessionRepo.DeleteByIDs(ctx, actualIDs, func(tx *gorm.DB) error {
		return s.bumpSessionInvalidation(ctx, tx)
	}); err != nil {
		return fmt.Errorf("delete sessions %v: %w", actualIDs, err)
	}

	userList := slices.Collect(maps.Keys(usernames))

	// 推送事件：强制下线被踢用户（管理员踢会话）
	s.publisher.PublishToUser(ctx, userList, push.Message{
		Name: push.EventSession,
		Payload: push.EventPayload{
			Action: "logout",
			Notify: &push.Notification{
				Title:   "会话已终止",
				Message: "管理员已强制断开您的连接",
				Level:   "warning",
			},
		},
	})
	for username, userID := range usernames {
		s.evictor.revokeProxyIfNoSessions(ctx, userID, username)
	}
	return nil
}

func (s *Service) DeleteByAccessToken(ctx context.Context, accessToken string) error {
	info, err := s.GetByToken(ctx, accessToken)
	if err != nil {
		return err
	}
	if err := s.sessionRepo.DeleteByIDs(ctx, []uint{info.ID}, func(tx *gorm.DB) error {
		return s.bumpSessionInvalidation(ctx, tx)
	}); err != nil {
		return fmt.Errorf("delete session %d by token: %w", info.ID, err)
	}
	s.invalidateToken(accessToken)
	s.evictor.revokeProxyIfNoSessions(ctx, info.UserID, info.Username)
	return nil
}

func (s *Service) DeleteOwnedByIDs(ctx context.Context, ownerUserID uint, sessionIDs ...uint) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	sessions, err := s.sessionRepo.GetByIDs(ctx, sessionIDs)
	if err != nil {
		return fmt.Errorf("get owned sessions for delete: %w", err)
	}
	if len(sessions) == 0 {
		return nil
	}

	actualIDs := make([]uint, 0, len(sessions))
	username := ""
	for _, sess := range sessions {
		if sess.UserID != ownerUserID {
			return ErrSessionAccessDenied
		}
		actualIDs = append(actualIDs, sess.ID)
		username = sess.Username
		s.invalidateToken(sess.Token)
	}

	if err := s.sessionRepo.DeleteByIDs(ctx, actualIDs, func(tx *gorm.DB) error {
		return s.bumpSessionInvalidation(ctx, tx)
	}); err != nil {
		return fmt.Errorf("delete owned sessions %v: %w", actualIDs, err)
	}
	s.evictor.revokeProxyIfNoSessions(ctx, ownerUserID, username)
	return nil
}

func (s *Service) DeleteOwnedByID(ctx context.Context, sessionID, ownerUserID uint) error {
	session, err := s.sessionRepo.Get(ctx, sessionID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("get session %d: %w", sessionID, err)
	}
	if session.UserID != ownerUserID {
		return ErrSessionAccessDenied
	}
	return s.DeleteOwnedByIDs(ctx, ownerUserID, session.ID)
}

func (s *Service) List(ctx context.Context, limit, offset int) ([]model.Session, int64, error) {
	sessions, total, err := s.sessionRepo.List(ctx, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list sessions: %w", err)
	}
	return sessions, total, nil
}

func (s *Service) ListByUserID(ctx context.Context, userID uint) ([]model.Session, error) {
	sessions, err := s.sessionRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions for user %d: %w", userID, err)
	}
	return sessions, nil
}

func (s *Service) ListByUserIDPage(ctx context.Context, userID uint, limit, offset int) ([]model.Session, int64, error) {
	sessions, total, err := s.sessionRepo.ListByUserIDPage(ctx, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list sessions for user %d: %w", userID, err)
	}
	return sessions, total, nil
}

// mergeUniqueModules merges two string slices in order, returning a new slice
// with duplicate items and empty strings ("") removed.
func mergeUniqueModules(a, b []string) []string {
	res := make([]string, 0, len(a)+len(b))
	seen := make(map[string]bool, len(a)+len(b))
	for _, s := range [][]string{a, b} {
		for _, item := range s {
			if item != "" && !seen[item] {
				seen[item] = true
				res = append(res, item)
			}
		}
	}
	return res
}

func newSessionRecordWithTTL(userID uint, username, ip string, role model.Role, userType model.UserType, adminScopes model.AdminScopes, ttlSeconds int, passwordMustChange bool) model.Session {
	now := time.Now()
	secret := crypto.GenSecret(24)
	return model.Session{
		UserID:             userID,
		Username:           username,
		Token:              secret,
		IP:                 ip,
		Role:               role,
		UserType:           userType,
		AdminScopes:        adminScopes,
		PasswordMustChange: passwordMustChange,
		ExpiresAt:          now.Add(time.Duration(ttlSeconds) * time.Second),
	}
}

func (s *Service) sessionTTLSeconds(ctx context.Context) (int, error) {
	authSetting, err := s.settingsSvc.GetAuthSettings(ctx)
	if err != nil {
		return 0, fmt.Errorf("load auth settings: %w", err)
	}
	return authSetting.SessionTTL, nil
}

type FileLocationsLister interface {
	ListLocations(ctx context.Context, runUser string) ([]file.LocationInfo, error)
}

func BuildAdminConsoleModules(sessionRecord *model.Session) []string {
	if sessionRecord.Role == model.RoleSuperAdmin {
		return []string{"overview", "node", "connection", "scheduler", "app", "settings", "license",
			"ticket", "user", "file", "sessions", "security", "audit", "storage", "status", "scope", "role"}
	}

	type moduleRule struct {
		name   string
		role   model.Role
		scopes []model.AdminScope
	}
	rules := []moduleRule{
		{"node", model.RoleSystemAdmin, []model.AdminScope{model.ScopeNodes}},
		{"connection", model.RoleSystemAdmin, []model.AdminScope{model.ScopeConnections}},
		{"scheduler", model.RoleSystemAdmin, []model.AdminScope{model.ScopeSchedulers}},
		{"app", model.RoleSystemAdmin, []model.AdminScope{model.ScopeApps}},
		{"settings", model.RoleSystemAdmin, []model.AdminScope{model.ScopeSettings}},
		{"license", model.RoleSystemAdmin, []model.AdminScope{model.ScopeLicenses}},
		{"ticket", model.RoleSystemAdmin, []model.AdminScope{model.ScopeTickets}},
		{"user", model.RoleSystemAdmin, []model.AdminScope{model.ScopeUsers}},
		{"storage", model.RoleSystemAdmin, []model.AdminScope{model.ScopeStorages}},
		{"status", model.RoleSystemAdmin, []model.AdminScope{model.ScopeStatuses}},
		{"file", model.RoleSecurityAdmin, []model.AdminScope{model.ScopeFiles}},
		{"sessions", model.RoleSecurityAdmin, []model.AdminScope{model.ScopeSessions}},
		{"security", model.RoleSecurityAdmin, []model.AdminScope{model.ScopeBans}},
	}

	modules := []string{"overview"} // 硬添加：无条件作为第一项，与任何权限无关
	for _, r := range rules {
		if sessionRecord.Role == r.role || sessionRecord.AdminScopes.HasAny(r.scopes...) {
			modules = append(modules, r.name)
		}
	}

	// Role-only modules
	if sessionRecord.Role == model.RoleSecurityAdmin {
		modules = append(modules, "role")
	}
	// Audit: ScopeAll exact match or AuditAdmin role
	if sessionRecord.Role == model.RoleAuditAdmin || sessionRecord.AdminScopes == model.AdminScopes(model.ScopeAll) {
		modules = append(modules, "audit")
	}
	return modules
}

func (s *Service) BuildSessionContextPayload(ctx context.Context, sessionRecord *model.Session, fileLister FileLocationsLister, clientIP string, passwordMustChange bool) *Payload {
	var (
		locs []file.LocationInfo
		err  error
	)
	if fileLister != nil {
		locs, err = fileLister.ListLocations(ctx, sessionRecord.Username)
		if err != nil {
			locs = nil
		}
	}

	hasAdminAccess := sessionRecord.Role >= model.RoleAuditAdmin || sessionRecord.AdminScopes != 0
	adminModules := BuildAdminConsoleModules(sessionRecord)
	userPortalEnabled := sessionRecord.Role < model.RoleAuditAdmin || sessionRecord.Role == model.RoleSuperAdmin

	var globalDisabled []string
	fileEnabled := true
	fileDownloadEnabled := true
	fileUploadEnabled := true
	var fileUploadMaxSizeBytes int64

	if s.settingsSvc != nil {
		if pSettings, err := s.settingsSvc.GetPlatformSettings(ctx); err == nil {
			for module, disabled := range pSettings.DisabledModules {
				if disabled {
					globalDisabled = append(globalDisabled, module)
				}
			}
			slices.Sort(globalDisabled)
		}
		if fSettings, err := s.settingsSvc.GetFileSettings(ctx); err == nil {
			fileEnabled = fSettings.Enabled
			fileDownloadEnabled = fSettings.DownloadEnabled
			fileUploadEnabled = fSettings.UploadEnabled
			fileUploadMaxSizeBytes = fSettings.UploadMaxSizeBytes
		}
	}
	var userDisabled []string
	if s.userSvc != nil {
		if u, err := s.userSvc.GetDBUser(ctx, sessionRecord.Username); err == nil {
			userDisabled = u.Preference.DisabledModules
			fileDownloadEnabled = fileDownloadEnabled && u.DownloadAccess
			fileUploadEnabled = fileUploadEnabled && u.UploadAccess
		}
	}
	if !fileEnabled {
		locs = []file.LocationInfo{}
	}
	disabledModules := mergeUniqueModules(globalDisabled, userDisabled)

	uiPayload := UIPayload{
		AdminConsole: AdminConsolePayload{
			Enabled: hasAdminAccess,
			Mode:    UIModuleListAllow,
			Modules: adminModules,
		},
		UserPortal: UserPortalPayload{
			Enabled: userPortalEnabled,
			Mode:    UIModuleListDeny,
			Modules: disabledModules,
		},
	}

	return &Payload{
		Token:              sessionRecord.Token,
		Username:           sessionRecord.Username,
		Role:               sessionRecord.Role.String(),
		IP:                 clientIP,
		ExpiresAt:          sessionRecord.ExpiresAt,
		PasswordMustChange: passwordMustChange,
		File: FilePayload{
			Enabled:            fileEnabled,
			Locations:          locs,
			DownloadEnabled:    fileEnabled && fileDownloadEnabled,
			UploadEnabled:      fileEnabled && fileUploadEnabled,
			UploadMaxSizeBytes: fileUploadMaxSizeBytes,
		},
		UI: uiPayload,
	}
}
