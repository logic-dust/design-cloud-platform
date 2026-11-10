package user

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/infra/concurrency"
	"design-cloud-engine/internal/model"
	userprovider "design-cloud-engine/internal/server/service/user/provider"
)

// LDAPHealthResult carries the result of an LDAP connectivity check.
type LDAPHealthResult struct {
	Configured bool
	LatencyMs  int64
	Err        error
}

// LDAPHealthCheck is the user-domain facade for provider-level LDAP connectivity checks.
// The actual LDAP probe implementation lives in user/provider; this method keeps the
// external access point on user.Service so API and registry wiring do not depend on
// provider internals.
// When AuthMode is not LDAP, Configured is false and no network call is made.
func (s *Service) LDAPHealthCheck(ctx context.Context) LDAPHealthResult {
	if s.cfg.AuthMode != config.UserAuthModeLDAP {
		return LDAPHealthResult{Configured: false}
	}
	latency, err, ok := userprovider.CheckLDAPHealth(ctx, s.provider)
	if !ok {
		return LDAPHealthResult{Configured: true, Err: fmt.Errorf("unexpected LDAP provider type")}
	}
	return LDAPHealthResult{Configured: true, LatencyMs: latency, Err: err}
}

// SyncUsers compares system/LDAP users against DB Normal-type users.
//
// Actions:
//  1. DB users NOT in system/LDAP → set Status = UserLocked (frozen)
//  2. DB users that were previously frozen but now exist in system/LDAP → restore Status = UserActive
//
// System-type users (from LDAP/OS) are the targets; App-type users are never touched.
func (s *Service) SyncUsers(ctx context.Context, logger *slog.Logger) {
	systemUsers, err := s.provider.ListUsers(ctx)
	if err != nil {
		logger.Error("user sync: failed to list system/LDAP users", "error", err)
		return
	}
	if len(systemUsers) == 0 {
		logger.Error("user sync: system/LDAP returned 0 users, aborting to prevent mass freeze")
		return
	}

	// Build lookup map of system users
	systemByName := make(map[string]userprovider.SystemUser, len(systemUsers))
	for _, su := range systemUsers {
		systemByName[su.Username] = su
	}

	// Load all System-type (LDAP/OS) users from DB for sync check.
	// Abort on error to prevent misidentifying all DB users as "not in system"
	// which would cause mass-freezing of every user account.
	dbUsers, err := s.userRepo.ListSystem(ctx)
	if err != nil {
		logger.Error("user sync: failed to list DB system users, aborting to prevent mass freeze", "error", err)
		return
	}

	now := time.Now()
	var toFreezeFromActive, toFreezeFromExpired []string
	var toRestoreFromLocked, toRestoreFromExpired []string
	var toExpireFromActive, toExpireFromLocked []string

	for i := range dbUsers {
		u := &dbUsers[i]
		if u.Status == model.UserDisabled {
			continue
		}
		su, existsInSystem := systemByName[u.Username]
		if !existsInSystem {
			switch u.Status {
			case model.UserActive:
				toFreezeFromActive = append(toFreezeFromActive, u.Username)
			case model.UserPasswordExpired:
				toFreezeFromExpired = append(toFreezeFromExpired, u.Username)
			}
			continue
		}
		if su.Status == model.UserStatusString(model.UserPasswordExpired) {
			switch u.Status {
			case model.UserActive:
				toExpireFromActive = append(toExpireFromActive, u.Username)
			case model.UserLocked:
				toExpireFromLocked = append(toExpireFromLocked, u.Username)
			}
			continue
		}
		switch u.Status {
		case model.UserLocked:
			toRestoreFromLocked = append(toRestoreFromLocked, u.Username)
		case model.UserPasswordExpired:
			if su.PasswordExpiresAt != nil && now.Before(*su.PasswordExpiresAt) {
				toRestoreFromExpired = append(toRestoreFromExpired, u.Username)
			}
		}
	}

	var frozen int64
	if n, err := s.userRepo.ChangeStatusIf(ctx, toFreezeFromActive, model.UserActive, model.UserLocked); err != nil {
		logger.Error("user sync: failed to freeze active users", "count", len(toFreezeFromActive), "error", err)
	} else {
		frozen += n
		s.evictFrozenSessions(ctx, logger, toFreezeFromActive, n)
	}
	if n, err := s.userRepo.ChangeStatusIf(ctx, toFreezeFromExpired, model.UserPasswordExpired, model.UserLocked); err != nil {
		logger.Error("user sync: failed to freeze expired users", "count", len(toFreezeFromExpired), "error", err)
	} else {
		frozen += n
		s.evictFrozenSessions(ctx, logger, toFreezeFromExpired, n)
	}

	var restored int64
	if n, err := s.userRepo.ChangeStatusIf(ctx, toRestoreFromLocked, model.UserLocked, model.UserActive); err != nil {
		logger.Error("user sync: failed to restore locked users", "count", len(toRestoreFromLocked), "error", err)
	} else {
		restored += n
	}
	if n, err := s.userRepo.ChangeStatusIf(ctx, toRestoreFromExpired, model.UserPasswordExpired, model.UserActive); err != nil {
		logger.Error("user sync: failed to restore password-expired users", "count", len(toRestoreFromExpired), "error", err)
	} else {
		restored += n
	}

	var expired int64
	if n, err := s.userRepo.ChangeStatusIf(ctx, toExpireFromActive, model.UserActive, model.UserPasswordExpired); err != nil {
		logger.Error("user sync: failed to mark active users passwordExpired", "count", len(toExpireFromActive), "error", err)
	} else {
		expired += n
		s.evictFrozenSessions(ctx, logger, toExpireFromActive, n)
	}
	if n, err := s.userRepo.ChangeStatusIf(ctx, toExpireFromLocked, model.UserLocked, model.UserPasswordExpired); err != nil {
		logger.Error("user sync: failed to mark locked users passwordExpired", "count", len(toExpireFromLocked), "error", err)
	} else {
		expired += n
	}

	logger.Info("user sync: completed",
		"system_users", len(systemUsers),
		"db_normal_users", len(dbUsers),
		"frozen", frozen,
		"restored", restored,
		"expired", expired,
	)

	s.invalidateIDMappingsFromSystemUsers(systemUsers)
	// 集群清空信号：他节点丢掉 idMapping 副本后按 miss 回源身份源，不是从 DB 拉齐名字。
	// 列表失败或空列表中止的路径在上方已 return，不 Bump。
	if err := s.bumpIDMappingCache(ctx); err != nil {
		logger.Error("user sync: identity cache bump failed", "error", err)
	}

	if frozen > 0 || restored > 0 || expired > 0 {
		s.auditSvc.LogSystem(ctx, "USER_BATCH_SYNC", slog.LevelInfo, map[string]any{
			"frozen":   frozen,
			"restored": restored,
			"expired":  expired,
		})
	}
}

func (s *Service) evictFrozenSessions(ctx context.Context, logger *slog.Logger, usernames []string, changed int64) {
	if changed <= 0 || s.deleteSessionsByUsername == nil {
		return
	}
	for _, username := range usernames {
		if delErr := s.deleteSessionsByUsername(ctx, username); delErr != nil {
			logger.Warn("user sync: failed to delete sessions after status change",
				"username", username, "error", delErr)
		}
	}
}

// StartSyncScheduler starts a background goroutine that waits for the configured
// sync interval between runs. Interval changes take effect on the next cycle.
// If interval <= 0, the scheduler pauses but periodically checks for updates.
func (s *Service) StartSyncScheduler(ctx context.Context, logger *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	s.syncTaskMu.Lock()
	s.syncTaskCtx = ctx
	s.syncTasksStopped = false
	s.syncTaskMu.Unlock()
	logger.Info("user sync: scheduler started (dynamic interval mode)")

	concurrency.SafeGo(logger, "user-sync-scheduler", func() {
		defer close(done)
		const idlePollInterval = 2 * time.Minute

		for {
			interval, enabled, err := s.syncInterval(ctx)
			if err != nil {
				logger.Warn("user sync: failed to get ID mapping settings, retrying later", "error", err)
				interval = idlePollInterval
				enabled = false
			}

			var sleepDur time.Duration
			if !enabled {
				// Sleep and check DB again later
				sleepDur = idlePollInterval
			} else {
				// We do the sync first, then sleep
				func() {
					defer concurrency.SafeRecover(logger, "user-sync-pass")()
					s.trySyncUsers(ctx, logger, interval)
				}()
				sleepDur = interval
			}

			timer := time.NewTimer(sleepDur)
			select {
			case <-timer.C:
				// continue loop
			case <-ctx.Done():
				timer.Stop()
				logger.Info("user sync: scheduler stopped")
				return
			}
		}
	})
	return done
}

// TriggerSyncAsync starts an API-requested sync on the service lifecycle context.
// The task is tracked so shutdown can cancel and wait for it before closing the DB.
func (s *Service) TriggerSyncAsync(logger *slog.Logger) bool {
	s.syncTaskMu.Lock()
	if s.syncTasksStopped || s.syncTaskCtx == nil {
		s.syncTaskMu.Unlock()
		return false
	}
	ctx := s.syncTaskCtx
	s.syncTasks.Add(1)
	s.syncTaskMu.Unlock()

	concurrency.SafeGo(logger, "user-sync-api", func() {
		defer s.syncTasks.Done()
		s.TriggerSync(ctx, logger)
	})
	return true
}

// StopAsyncSyncs prevents new API syncs and waits for accepted tasks to finish.
// The parent lifecycle context should be canceled before calling this method.
func (s *Service) StopAsyncSyncs() {
	s.syncTaskMu.Lock()
	s.syncTasksStopped = true
	s.syncTaskMu.Unlock()
	s.syncTasks.Wait()
}

func (s *Service) syncInterval(ctx context.Context) (time.Duration, bool, error) {
	authSettings, err := s.authSettings.GetAuthSettings(ctx)
	if err != nil {
		return 0, false, err
	}
	intervalSec := authSettings.UserSyncInterval
	if intervalSec <= 0 {
		return 0, false, nil
	}
	// Clamp interval between allowed min/max
	intervalSec = min(max(intervalSec, 300), 604800)
	return time.Duration(intervalSec) * time.Second, true, nil
}

// TriggerSync runs LDAP/OS user sync when this instance holds the user_sync lease
// (same path as the background scheduler). Safe for concurrent API calls.
func (s *Service) TriggerSync(ctx context.Context, logger *slog.Logger) {
	interval := time.Minute
	if d, _, err := s.syncInterval(ctx); err != nil {
		logger.Warn("user sync: failed to read auth settings, using default lease", "error", err)
	} else if d > 0 {
		interval = d
	}
	s.trySyncUsers(ctx, logger, interval)
}

func (s *Service) trySyncUsers(ctx context.Context, logger *slog.Logger, interval time.Duration) {
	acquired, _, err := s.leaseRepo.TryAcquire(ctx, "user_sync", s.cfg.SyncLeaseOwnerID, max(time.Minute, interval))
	if err != nil {
		logger.Warn("user sync: failed to acquire lease", "error", err)
		return
	}
	if !acquired {
		logger.Debug("user sync: skipped on this server (lease held elsewhere)")
		return
	}
	s.SyncUsers(ctx, logger)
}
