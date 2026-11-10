package ticket

import (
	"context"
	"errors"
	"log/slog"
	"strconv"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/server/push"
)

// Service 工单系统业务逻辑。
type Service struct {
	ticketRepo        *db.TicketRepo
	projectRepo       *db.TicketProjectRepo
	userRepo          *db.UserRepo
	auditSvc          *audit.Service
	logger            *slog.Logger
	publisher         push.Publisher
	resolveAttachment AttachmentResolver
}

// Option 配置 Service。
type Option func(*Service)

// WithAttachmentResolver 设置附件引用的校验函数；未设置时拒绝所有附件。
func WithAttachmentResolver(fn AttachmentResolver) Option {
	return func(s *Service) { s.resolveAttachment = fn }
}

// NewService 创建工单服务。
func NewService(
	ticketRepo *db.TicketRepo,
	projectRepo *db.TicketProjectRepo,
	userRepo *db.UserRepo,
	auditSvc *audit.Service,
	logger *slog.Logger,
	publisher push.Publisher,
	opts ...Option,
) *Service {
	if publisher == nil {
		publisher = push.NoopPublisher{}
	}
	s := &Service{
		ticketRepo:  ticketRepo,
		projectRepo: projectRepo,
		userRepo:    userRepo,
		auditSvc:    auditSvc,
		logger:      logger.With("component", "ticket"),
		publisher:   publisher,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Service) audit(ctx context.Context, actor Actor, action string, level slog.Level, details map[string]any) {
	details["operator_username"] = actor.Username
	s.auditSvc.LogUser(ctx, action, "success", strconv.FormatUint(uint64(actor.ID), 10), level, details)
}

// notify 通知相关用户刷新（排除操作者本人）。
func (s *Service) notify(ctx context.Context, actor Actor, targetID uint, usernames ...string) {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(usernames))
	for _, u := range usernames {
		if u == "" || u == actor.Username {
			continue
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	if len(out) == 0 {
		return
	}
	payload := push.EventPayload{Action: "refresh"}
	if targetID != 0 {
		payload.TargetID = strconv.FormatUint(uint64(targetID), 10)
	}
	s.publisher.PublishToUser(ctx, out, push.Message{Name: push.EventTicket, Payload: payload})
}

// mapRepoErr 把仓库层的"不存在"与"版本不符"转为对外错误。
func mapRepoErr(err error, notFound error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, db.ErrStaleVersion):
		return ErrVersionConflict
	case isNotFound(err):
		return notFound
	default:
		return err
	}
}
