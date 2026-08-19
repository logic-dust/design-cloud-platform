package user

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/config"
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

	// Build lookup set of system usernames
	systemSet := make(map[string]struct{}, len(systemUsers))
	for _, su := range systemUsers {
		systemSet[su.Username] = struct{}{}
	}

	// Load all System-type (LDAP/OS) users from DB for sync check.
	// Abort on error to prevent misidentifying all DB users as "not in system"
	// which would cause mass-freezing of every user account.
	dbUsers, err := s.userRepo.ListSystem(ctx)
	if err != nil {
		logger.Error("user sync: failed to list DB system users, aborting to prevent mass freeze", "error", err)
		return
	}

	var frozen, restored int
	for i := range dbUsers {
		u := &dbUsers[i]
		_, existsInSystem := systemSet[u.Username]

		switch {
		// Case 1: DB user not found in system → freeze
		// ✅ ChangeUserStatus now also invalidates all sessions when user is locked
		case !existsInSystem && u.Status == model.UserActive:
			err := s.ChangeStatus(ctx, u.Username, model.UserLocked)
			if errors.Is(err, common.ErrNotFound) {
				// User was deleted between ListSystemUsers and now — safe to ignore.
				logger.Debug("user sync: user already removed from DB, skip freeze", "username", u.Username)
			} else if err != nil {
				logger.Error("user sync: failed to freeze user", "username", u.Username, "error", err)
			} else {
				frozen++
				logger.Debug("user sync: froze user (no longer in system/LDAP)", "username", u.Username)
			}
		// Case 2: DB user was frozen but now exists in system → restore
		// Note: Activation doesn't invalidate sessions (user was locked, had no active sessions)
		case existsInSystem && u.Status == model.UserLocked:
			err := s.ChangeStatus(ctx, u.Username, model.UserActive)
			if errors.Is(err, common.ErrNotFound) {
				// User was present in DB listing but now missing — data inconsistency.
				logger.Error("user sync: user vanished from DB during restore (data inconsistency)", "username", u.Username)
			} else if err != nil {
				logger.Error("user sync: failed to restore user", "username", u.Username, "error", err)
			} else {
				restored++
				logger.Debug("user sync: restored user (found again in system/LDAP)", "username", u.Username)
			}
		}
	}

	logger.Info("user sync: completed",
		"system_users", len(systemUsers),
		"db_normal_users", len(dbUsers),
		"frozen", frozen,
		"restored", restored,
	)

	s.invalidateIDMappingsFromSystemUsers(systemUsers)

	if frozen > 0 || restored > 0 {
		s.auditSvc.LogSystem(ctx, "USER_BATCH_SYNC", slog.LevelInfo, map[string]any{
			"frozen":   frozen,
			"restored": restored,
		})
	}
}

// StartSyncScheduler starts a background goroutine that waits for the configured
// sync interval between runs. Interval changes take effect on the next cycle.
// If interval <= 0, the scheduler pauses but periodically checks for updates.
func (s *Service) StartSyncScheduler(ctx context.Context, logger *slog.Logger) {
	logger.Info("user sync: scheduler started (dynamic interval mode)")

	common.SafeGo(logger, "user-sync-scheduler", func() {
		const idlePollInterval = 1 * time.Minute

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
					defer common.SafeRecover(logger, "user-sync-pass")()
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
}

func (s *Service) syncInterval(ctx context.Context) (time.Duration, bool, error) {
	authSettings, err := s.settingsSvc.GetAuthSettings(ctx)
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

func (s *Service) trySyncUsers(ctx context.Context, logger *slog.Logger, interval time.Duration) {
	acquired, err := s.leaseRepo.TryAcquire(ctx, "user_sync", s.cfg.SyncLeaseOwnerID, max(time.Minute, interval))
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
