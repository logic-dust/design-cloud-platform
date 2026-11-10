// Package identity contains identity-related application use cases that coordinate cross-domain writes.
//
// 领域服务（service/*）保留各自的写操作；跨域副作用（清连接、踢会话）由本包
// 按用例组合。本包不拥有这些领域的持久化状态；依赖经消费方窄接口注入，业务
// 服务不反向依赖本包（由 arch-rules 强制）。
package identity

import (
	"context"
	"fmt"
	"log/slog"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/user"
)

// Service combines atomic business-service operations into identity use cases.
type Service struct {
	user    UserWriter
	role    RoleWriter
	conn    ConnectionPurger
	session SessionRevoker
	logger  *slog.Logger
}

// New creates the identity use-case service from its required business capabilities.
func New(userSvc UserWriter, roleSvc RoleWriter, connPurger ConnectionPurger, sessionRevoker SessionRevoker, logger *slog.Logger) *Service {
	return &Service{
		user:    userSvc,
		role:    roleSvc,
		conn:    connPurger,
		session: sessionRevoker,
		logger:  logger,
	}
}

// DeleteUser 删除用户：先清连接行与代理会话，再删门户会话，最后删用户行。
// 连接清理失败则中止（用户行仍在，可重试）；会话删除失败仅告警——
// 用户行删除后残留会话会在下次校验时自然失效。
func (s *Service) DeleteUser(ctx context.Context, username string) error {
	if err := s.conn.PurgeUserConnections(ctx, username); err != nil {
		return err
	}
	if err := s.session.Delete(ctx, username); err != nil {
		s.logger.Warn("delete sessions before user deletion failed", "username", username, "error", err)
	}
	return s.user.Delete(ctx, username)
}

// UpdateUserStatus 管理员更新用户记录：原子写用户字段后处理会话副作用——
// 状态改为非激活时同步踢掉会话，并始终使用户会话缓存失效。
func (s *Service) UpdateUserStatus(ctx context.Context, operator *model.Session, username string, req user.AdminUserUpdateRequest) (*model.User, error) {
	updated, err := s.user.AdminUpdate(ctx, operator, username, req)
	if err != nil {
		return nil, err
	}
	if req.Status != nil && model.UserStatusCode(*req.Status) != model.UserActive {
		if err := s.session.Delete(ctx, username); err != nil {
			s.logger.Warn("delete sessions after user status change failed", "username", username, "error", err)
		}
	}
	s.session.InvalidateByUsername(username)
	return updated, nil
}

// AssignRole 授予角色并踢掉目标用户会话。
// 角色已更新但会话删除失败时返回错误（与迁移前 role.SetUserRole 语义一致）。
func (s *Service) AssignRole(ctx context.Context, username string, role model.Role) (model.User, error) {
	updated, err := s.role.SetUserRole(ctx, username, role)
	if err != nil {
		return model.User{}, err
	}
	if err := s.session.Delete(ctx, username); err != nil {
		return model.User{}, fmt.Errorf("delete sessions after role update: %w", err)
	}
	return updated, nil
}
