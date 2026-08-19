package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/security"
	"design-cloud-engine/internal/server/service/user/provider"

	"golang.org/x/crypto/bcrypt"
)

type passwordVerifier interface {
	VerifyPassword(ctx context.Context, username, password string) error
}

// securityChecker is the subset of security.Service used by AuthManager.
type securityChecker interface {
	IsBanned(ctx context.Context, IP, username string) (bool, security.BannedRecord)
	AddFailedCount(ctx context.Context, keyName string, recordType model.BanType, IP, username string)
	DeleteByKey(ctx context.Context, keyName string)
}

type AuthManager struct {
	store    *db.UserRepo
	verifier passwordVerifier
	security securityChecker
	logger   *slog.Logger
	audit    *audit.Service
	ldapAuth bool
}

type Result struct {
	PasswordMustChange bool
}

// BlockRootAndAdmin rejects login attempts from system-protected accounts.
// Returns ErrLoginBlocked with details; records auth failure for IP ban tracking.
func (s *AuthManager) blockRootAndAdmin(ctx context.Context, username, IP string) error {
	isBlocked := username == "root" || (username == "admin" && s.ldapAuth)
	if !isBlocked {
		return nil
	}
	s.recordUserAndIPFailure(ctx, IP, username)
	action := "AUTH_ATTEMPT_ROOT"
	if username == "admin" {
		action = "AUTH_ATTEMPT_ADMIN"
	}
	s.logSecurity(ctx, action, "blocked", slog.LevelWarn, map[string]any{
		"username":  username,
		"remote_ip": IP,
	})
	msg := "login as root is not permitted"
	if username == "admin" {
		msg = "this account is not allowed to login"
	}
	return fmt.Errorf("%w: %s", ErrLoginBlocked, msg)
}

const authSideEffectTimeout = 5 * time.Second

func NewAuthManager(store *db.UserRepo, verifier passwordVerifier, securitySvc securityChecker, logger *slog.Logger, auditSvc *audit.Service, ldapAuth bool) (*AuthManager, error) {
	if store == nil {
		return nil, fmt.Errorf("session auth: user repo is required")
	}
	if verifier == nil {
		return nil, fmt.Errorf("session auth: password verifier is required")
	}
	if securitySvc == nil {
		return nil, fmt.Errorf("session auth: security manager is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("session auth: logger is required")
	}
	if auditSvc == nil {
		return nil, fmt.Errorf("session auth: audit service is required")
	}
	return &AuthManager{
		store:    store,
		verifier: verifier,
		security: securitySvc,
		logger:   logger,
		audit:    auditSvc,
		ldapAuth: ldapAuth,
	}, nil
}

func (s *AuthManager) Authenticate(ctx context.Context, username, password, IP string) (Result, error) {
	startTime := time.Now()

	if isBanned, bannedRecord := s.security.IsBanned(ctx, IP, username); isBanned {
		s.logSecurity(ctx, "AUTH_ATTEMPT_BANNED", "blocked", slog.LevelWarn, map[string]any{
			"username":   username,
			"remote_ip":  IP,
			"ban_reason": bannedRecord.Reason,
		})
		return Result{}, errors.New(bannedRecord.Reason)
	}

	userRecord, err := s.store.GetByName(ctx, username)
	if err != nil {
		if !errors.Is(err, common.ErrNotFound) {
			s.logAuth(ctx, "DB_USER_LOOKUP", "error", username, IP, slog.LevelWarn, map[string]any{
				"error": err.Error(),
			}, time.Since(startTime))
			return Result{}, err
		}
		userRecord = model.User{}
	}
	if userRecord.ID != 0 && userRecord.Type == model.UserTypeApp {
		if userRecord.Status != model.UserActive && userRecord.Status != model.UserPasswordExpired {
			s.recordUserAndIPFailure(ctx, IP, username)
			s.logAuth(ctx, "APP_USER_AUTH", "failed", username, IP, slog.LevelWarn, map[string]any{
				"user_type": "app",
				"error":     provider.ErrUserNotActive.Error(),
			}, time.Since(startTime))
			return Result{}, provider.ErrUserNotActive
		}
		if err := bcrypt.CompareHashAndPassword([]byte(userRecord.Password), []byte(password)); err != nil {
			s.recordUserAndIPFailure(ctx, IP, username)
			s.logAuth(ctx, "APP_USER_AUTH", "failed", username, IP, slog.LevelWarn, map[string]any{
				"user_type": "app",
				"error":     provider.ErrUserPasswordIncorrect.Error(),
			}, time.Since(startTime))
			return Result{}, provider.ErrUserPasswordIncorrect
		}

		s.clearUserFailures(ctx, IP, username)
		s.logAuth(ctx, "APP_USER_AUTH", "success", username, IP, slog.LevelInfo, map[string]any{
			"user_type": "app",
		}, time.Since(startTime))
		if userRecord.Status == model.UserPasswordExpired {
			return Result{PasswordMustChange: true}, nil
		}
		return Result{}, nil
	}

	if userRecord.ID != 0 && userRecord.Status != model.UserActive && userRecord.Status != model.UserPasswordExpired {
		s.recordUserAndIPFailure(ctx, IP, username)
		s.logAuth(ctx, "OS_USER_AUTH", "failed", username, IP, slog.LevelWarn, map[string]any{
			"user_type": "os",
			"error":     "account_not_active",
			"status":    userRecord.Status,
		}, time.Since(startTime))
		return Result{}, provider.ErrUserNotActive
	}

	err = s.verifier.VerifyPassword(ctx, username, password)

	if errors.Is(err, provider.ErrUserPasswordExpired) {
		s.clearUserFailures(ctx, IP, username)
		s.logAuth(ctx, "OS_USER_AUTH", "password_expired", username, IP, slog.LevelInfo, map[string]any{}, time.Since(startTime))
		return Result{PasswordMustChange: true}, nil
	}

	if err != nil {
		if errors.Is(err, provider.ErrUserNotFound) {
			s.addFailedCount(ctx, IP, model.BanTypeIP, IP, "")
			s.logAuth(ctx, "OS_USER_AUTH", "failed", username, IP, slog.LevelWarn, map[string]any{
				"error": "user_not_found",
			}, time.Since(startTime))
			return Result{}, provider.ErrUserPasswordIncorrect
		}
		if errors.Is(err, provider.ErrUserPasswordIncorrect) {
			s.recordUserAndIPFailure(ctx, IP, username)
		}
		s.logAuth(ctx, "OS_USER_AUTH", "failed", username, IP, slog.LevelWarn, map[string]any{
			"error": "auth_failed",
		}, time.Since(startTime))
		return Result{}, provider.ErrUserPasswordIncorrect
	}

	if userRecord.ID != 0 && userRecord.Status == model.UserPasswordExpired {
		s.clearUserFailures(ctx, IP, username)
		s.logAuth(ctx, "OS_USER_AUTH", "password_expired", username, IP, slog.LevelInfo, map[string]any{}, time.Since(startTime))
		return Result{PasswordMustChange: true}, nil
	}

	s.clearUserFailures(ctx, IP, username)
	s.logAuth(ctx, "OS_USER_AUTH", "success", username, IP, slog.LevelInfo, map[string]any{}, time.Since(startTime))
	return Result{}, nil
}

func (s *AuthManager) logAuth(ctx context.Context, action, result, username, ip string, level slog.Level, details map[string]any, duration time.Duration) {
	auditCtx, cancel := authSideEffectContext(ctx)
	defer cancel()
	s.audit.LogAuth(auditCtx, action, result, username, ip, level, details, duration)
}

func (s *AuthManager) logSecurity(ctx context.Context, action, result string, level slog.Level, details map[string]any) {
	auditCtx, cancel := authSideEffectContext(ctx)
	defer cancel()
	s.audit.LogSecurity(auditCtx, action, result, level, details)
}

func (s *AuthManager) recordUserAndIPFailure(ctx context.Context, IP, username string) {
	s.addFailedCount(ctx, IP+":"+username, model.BanTypeUser, IP, username)
	s.addFailedCount(ctx, IP, model.BanTypeIP, IP, "")
}

func (s *AuthManager) addFailedCount(ctx context.Context, keyName string, recordType model.BanType, IP, username string) {
	securityCtx, cancel := authSideEffectContext(ctx)
	defer cancel()
	s.security.AddFailedCount(securityCtx, keyName, recordType, IP, username)
}

func (s *AuthManager) clearUserFailures(ctx context.Context, IP, username string) {
	securityCtx, cancel := authSideEffectContext(ctx)
	defer cancel()
	s.security.DeleteByKey(securityCtx, IP+":"+username)
}

func authSideEffectContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), authSideEffectTimeout)
}
