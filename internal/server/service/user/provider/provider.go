package provider

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/platform/config"
)

var (
	ErrUserNotFound             = contract.NewNotFoundError("user not found")
	ErrGroupNotFound            = contract.NewNotFoundError("Group not found")
	ErrUserNotActive            = errors.New("this user account is not active")
	ErrUserPasswordIncorrect    = errors.New("user or password is incorrect")
	ErrUserPasswordExpired      = errors.New("user password has expired and must be changed")
	ErrUserPasswordSameAsOld    = errors.New("new password cannot be the same as the old password")
	ErrLDAPPasswordNotSupported = contract.NewInvalidParamError("password change is not supported for LDAP users; please use your organization's LDAP management portal")
)

type SystemUser struct {
	ID                uint       `json:"id,omitempty"`
	Username          string     `json:"username"`
	Uid               string     `json:"uid"`
	Gid               string     `json:"gid"`
	Home              string     `json:"home"`
	Shell             string     `json:"shell"`
	Groups            []string   `json:"groups"`
	Status            string     `json:"status"`
	Password          string     `json:"-"`
	Group             string     `json:"group,omitempty"`
	Comment           string     `json:"comment,omitempty"`
	PasswordExpiresAt *time.Time `json:"passwordExpiresAt,omitempty"`
}

type Provider interface {
	GetUser(ctx context.Context, username string) (*SystemUser, error)
	ListUsers(ctx context.Context) ([]SystemUser, error)
	ListGroups(ctx context.Context) (map[string][]string, error)
	ListUserGroups(ctx context.Context) (map[string][]string, error)
	GetGroupMembers(ctx context.Context, groupName string) ([]string, error)
	// UsersByIDs resolves posix uidNumber → username. Missing IDs are omitted (not an error).
	// On ID-source failure the error must be returned; callers must not treat misses as notFound.
	UsersByIDs(ctx context.Context, uids []uint32) (map[uint32]string, error)
	// GroupsByIDs resolves posix gidNumber → group cn. Missing IDs are omitted (not an error).
	GroupsByIDs(ctx context.Context, gids []uint32) (map[uint32]string, error)
	VerifyPassword(ctx context.Context, username, password string) error
	ChangePassword(ctx context.Context, username, oldPassword, newPassword string) error
	ResetPassword(ctx context.Context, username, newPassword string) error
}

func New(execSvc execution.Executor, cfg config.User, store *db.UserRepo, logger *slog.Logger) (Provider, error) {
	if store == nil {
		return nil, errors.New("user provider: user repo is required")
	}
	if logger == nil {
		return nil, errors.New("user provider: logger is required")
	}
	switch cfg.AuthMode {
	case config.UserAuthModeLDAP:
		return &ldapProvider{db: store, logger: logger, cfg: cfg.Ldap}, nil
	default:
		if execSvc == nil {
			return nil, errors.New("user provider: execution service is required for local auth")
		}
		return &localProvider{db: store, execSvc: execSvc, logger: logger}, nil
	}
}

// Bundle groups the provider set used by user.Service for UID/GID → name resolution:
//
//	Primary — login / listing / password flows (LDAP or Local per AuthMode)
//	Local   — ID-mapping resolution base (passwd/group); nil when this platform
//	          has no local user actions registered
//	LDAP    — ID-mapping resolution overlay; nil unless AuthMode is LDAP
type Bundle struct {
	Primary Provider
	Local   Provider
	LDAP    Provider
}

// NewBundle builds the primary provider plus the ID-mapping resolution sources
// (Local base + LDAP overlay) that Service merges per the resolution policy.
func NewBundle(execSvc execution.Executor, cfg config.User, store *db.UserRepo, logger *slog.Logger) (*Bundle, error) {
	primary, err := New(execSvc, cfg, store, logger)
	if err != nil {
		return nil, err
	}
	bundle := &Bundle{Primary: primary}
	// 静态能力判定：本二进制是否注册了 POSIX 用户/组动作（linux 构建注册）。
	// 未注册 = 该平台不存在本机身份源（而非暂态失败），上层据此区分"确认空集" vs "不可用"。
	if execSvc != nil && LocalActionsAvailable() {
		bundle.Local = &localProvider{db: store, execSvc: execSvc, logger: logger}
	}
	if cfg.AuthMode == config.UserAuthModeLDAP {
		bundle.LDAP = &ldapProvider{db: store, logger: logger, cfg: cfg.Ldap}
	}
	return bundle, nil
}

// LocalActionsAvailable reports whether this binary has the POSIX user/group
// query actions registered (true on linux builds).
func LocalActionsAvailable() bool {
	_, ok := execution.Actions[execution.ActionUserList]
	return ok
}
