// 消费方契约：由领域服务实现，本包只声明自己需要的那部分。
package identity

import (
	"context"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/user"
)

// UserWriter is the user-service write contract required by these use cases.
type UserWriter interface {
	// Delete 仅删除用户行（原子操作，不含连接/会话清理）。
	Delete(ctx context.Context, username string) error
	// AdminUpdate 仅写用户聚合字段（原子操作，不含会话副作用）。
	AdminUpdate(ctx context.Context, operator *model.Session, username string, req user.AdminUserUpdateRequest) (*model.User, error)
}

// RoleWriter is the role-service write contract required by these use cases.
type RoleWriter interface {
	// SetUserRole 仅改角色（原子操作，不含会话副作用）。
	SetUserRole(ctx context.Context, username string, role model.Role) (model.User, error)
}

// ConnectionPurger removes a user's connection rows and closes proxies before user deletion.
// Implemented by connection.Service.
type ConnectionPurger interface {
	PurgeUserConnections(ctx context.Context, username string) error
}

// SessionRevoker is the narrow session-service contract required by these use cases.
//
// Defined by the consumer: the use cases declare only the two operations they need,
// allowing the session package to evolve independently.
type SessionRevoker interface {
	// Delete 同步删除该用户的全部门户会话，失败必须返回错误。
	Delete(ctx context.Context, username string) error
	// InvalidateByUsername 使该用户的会话缓存失效（无返回值，不阻塞）。
	InvalidateByUsername(username string)
}
