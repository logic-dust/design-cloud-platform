package ticket

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/push"
)

var (
	ErrTitleRequired       = errors.New("title is required")
	ErrProjectNameRequired = errors.New("project name is required")
	ErrProjectNameTooLong  = errors.New("project name cannot exceed 128 chars")
	ErrPermissionDenied    = errors.New("permission denied")
	ErrProjectHasTickets   = errors.New("cannot delete project with active tickets")
	ErrProjectNameConflict = errors.New("project name already exists")
)

// Service 工单系统业务逻辑
type Service struct {
	ticketRepo  *db.TicketRepo
	projectRepo *db.TicketProjectRepo
	userRepo    *db.UserRepo
	auditSvc    *audit.Service
	logger      *slog.Logger
	publisher   push.Publisher
}

// NewService 创建工单服务
func NewService(
	ticketRepo *db.TicketRepo,
	projectRepo *db.TicketProjectRepo,
	userRepo *db.UserRepo,
	auditSvc *audit.Service,
	logger *slog.Logger,
	publisher push.Publisher,
) *Service {
	if publisher == nil {
		publisher = push.NoopPublisher{}
	}
	return &Service{
		ticketRepo:  ticketRepo,
		projectRepo: projectRepo,
		userRepo:    userRepo,
		auditSvc:    auditSvc,
		logger:      logger.With("component", "ticket"),
		publisher:   publisher,
	}
}

// --- 工单 CRUD ---

// Create 创建工单
func (s *Service) Create(ctx context.Context, req CreateTicketRequest, currentUserID uint, currentUsername string) (*model.Ticket, error) {
	// 1. 校验请求
	if err := s.validateCreateRequest(ctx, req); err != nil {
		return nil, err
	}

	// 2. 计算 sortOrder
	sortOrder := s.calculateInitialSortOrder(ctx, req.ProjectID, model.TicketStatus(req.Status))

	// 3. 序列化 JSON 字段
	criteriaJSON := serializeAcceptanceCriteria(req.AcceptanceCriteria)
	attachmentsJSON := serializeAttachments(req.Attachments)

	// 4. 创建工单
	ticket := &model.Ticket{
		ProjectID:          req.ProjectID,
		Type:               model.TicketType(req.Type),
		Title:              req.Title,
		Description:        req.Description,
		ReporterID:         currentUserID,
		AssigneeID:         req.AssigneeID,
		DueDate:            req.DueDate,
		Priority:           model.TicketPriority(req.Priority),
		Status:             model.TicketStatus(req.Status),
		AcceptanceCriteria: criteriaJSON,
		Attachments:        attachmentsJSON,
		SortOrder:          sortOrder,
	}

	if err := s.ticketRepo.Create(ctx, ticket); err != nil {
		return nil, fmt.Errorf("create ticket: %w", err)
	}

	// 5. 重新加载关联对象
	created, err := s.ticketRepo.GetByID(ctx, ticket.ID)
	if err != nil {
		return nil, fmt.Errorf("load created ticket: %w", err)
	}

	// 6. 审计日志
	s.auditSvc.LogUser(ctx, "TICKET_CREATE", "success", formatUint(currentUserID), slog.LevelInfo, map[string]any{
		"ticket_id":   created.ID,
		"project_id":  req.ProjectID,
		"assignee_id": req.AssigneeID,
		"title":       req.Title,
	})

	// 7. 推送事件：通知相关用户刷新列表（排除操作者，被动方=assignee+reporter 中的其他人）
	s.publisher.PublishToUser(ctx, formatUserIDs(currentUsername, created.Reporter.Username, created.Assignee.Username), push.Message{
		Name: push.EventTicket,
		Payload: push.EventPayload{
			Action: "refresh",
		},
	})

	return &created, nil
}

// GetByID 获取工单详情
func (s *Service) GetByID(ctx context.Context, ticketID uint) (*model.Ticket, error) {
	ticket, err := s.ticketRepo.GetByID(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("get ticket by id(%d): %w", ticketID, err)
	}
	return &ticket, nil
}

// List 获取工单列表（分页 + 过滤）
func (s *Service) List(ctx context.Context, filter Filter) (*PagedResponse, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 200)
	offset := max(filter.Offset, 0)

	dbFilter := db.TicketFilter{
		ProjectID:      filter.ProjectID,
		ReporterID:     filter.ReporterID,
		AssigneeID:     filter.AssigneeID,
		Limit:          limit,
		Offset:         offset,
		IncludeDeleted: filter.IncludeDeleted,
	}

	if filter.Type != nil {
		dbFilter.Type = new(model.TicketType(*filter.Type))
	}
	if filter.Status != nil {
		dbFilter.Status = new(model.TicketStatus(*filter.Status))
	}
	if filter.Priority != nil {
		dbFilter.Priority = new(model.TicketPriority(*filter.Priority))
	}

	tickets, total, err := s.ticketRepo.List(ctx, dbFilter)
	if err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}

	hasMore := int64(offset+limit) < total

	return &PagedResponse{
		Items:      tickets,
		TotalCount: total,
		Limit:      limit,
		Offset:     offset,
		HasMore:    hasMore,
	}, nil
}

// Update 更新工单
func (s *Service) Update(ctx context.Context, ticketID uint, req UpdateTicketRequest, currentUsername string) (*model.Ticket, error) {
	// 1. 获取现有工单
	ticket, err := s.ticketRepo.GetByID(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("get ticket for update(id=%d): %w", ticketID, err)
	}

	// 2. 构建更新字段
	updates := make(map[string]any)

	if req.Type != nil {
		updates["type"] = *req.Type
	}
	if req.Title != nil {
		updates["title"] = *req.Title
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.AssigneeID != nil {
		updates["assignee_id"] = *req.AssigneeID
	}
	if req.DueDate != nil {
		updates["due_date"] = *req.DueDate
	}
	if req.Priority != nil {
		updates["priority"] = *req.Priority
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if req.SortOrder != nil {
		updates["sort_order"] = *req.SortOrder
	}
	if req.AcceptanceCriteria != nil {
		updates["acceptance_criteria"] = serializeAcceptanceCriteria(req.AcceptanceCriteria)
	}
	if req.Attachments != nil {
		updates["attachments"] = serializeAttachments(*req.Attachments)
	}

	if len(updates) == 0 {
		return &ticket, nil
	}

	// 3. 执行更新
	if err := s.ticketRepo.UpdateFields(ctx, ticketID, updates); err != nil {
		return nil, fmt.Errorf("update ticket: %w", err)
	}

	// 4. 重新加载
	updated, err := s.ticketRepo.GetByID(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("load updated ticket: %w", err)
	}

	// 5. 审计日志
	s.auditSvc.LogUser(ctx, "TICKET_UPDATE", "success", formatUint(ticket.ReporterID), slog.LevelInfo, map[string]any{
		"ticket_id": ticketID,
		"updates":   updates,
	})

	// 6. 推送事件：带 targetId 局部刷新（排除操作者）
	s.publisher.PublishToUser(ctx, formatUserIDs(currentUsername, updated.Reporter.Username, updated.Assignee.Username), push.Message{
		Name: push.EventTicket,
		Payload: push.EventPayload{
			Action:   "refresh",
			TargetID: formatUint(updated.ID),
		},
	})

	return &updated, nil
}

// Delete 软删除工单（只有 Reporter 可删除自己的）
func (s *Service) Delete(ctx context.Context, currentUserID uint, currentUsername string, ticketIDs ...uint) error {
	if len(ticketIDs) == 0 {
		return nil
	}
	tickets, err := s.ticketRepo.GetByIDs(ctx, ticketIDs)
	if err != nil {
		return fmt.Errorf("get tickets for delete: %w", err)
	}
	if len(tickets) == 0 {
		return nil
	}

	actualIDs := make([]uint, 0, len(tickets))
	usernames := make(map[string]struct{})
	for _, ticket := range tickets {
		if ticket.ReporterID != currentUserID {
			return ErrPermissionDenied
		}
		actualIDs = append(actualIDs, ticket.ID)
		if ticket.Reporter.Username != "" {
			usernames[ticket.Reporter.Username] = struct{}{}
		}
		if ticket.Assignee.Username != "" {
			usernames[ticket.Assignee.Username] = struct{}{}
		}
	}

	if err := s.ticketRepo.SoftDeleteByIDs(ctx, actualIDs); err != nil {
		return fmt.Errorf("soft delete tickets %v: %w", actualIDs, err)
	}

	for _, id := range actualIDs {
		s.auditSvc.LogUser(ctx, "TICKET_DELETE", "success", formatUint(currentUserID), slog.LevelInfo, map[string]any{
			"ticket_id": id,
		})
	}

	delete(usernames, currentUsername)
	userList := slices.Collect(maps.Keys(usernames))

	// 推送事件：通知相关用户刷新列表（排除操作者）
	s.publisher.PublishToUser(ctx, userList, push.Message{
		Name: push.EventTicket,
		Payload: push.EventPayload{
			Action: "refresh",
		},
	})
	return nil
}

// --- 管理员操作 ---

// AdminDelete 管理员软删除任意工单
func (s *Service) AdminDelete(ctx context.Context, ticketIDs ...uint) error {
	if len(ticketIDs) == 0 {
		return nil
	}
	tickets, err := s.ticketRepo.GetByIDs(ctx, ticketIDs)
	if err != nil {
		return fmt.Errorf("get tickets for admin delete: %w", err)
	}
	if len(tickets) == 0 {
		return nil
	}

	actualIDs := make([]uint, 0, len(tickets))
	usernames := make(map[string]struct{})
	for _, ticket := range tickets {
		actualIDs = append(actualIDs, ticket.ID)
		if ticket.Reporter.Username != "" {
			usernames[ticket.Reporter.Username] = struct{}{}
		}
		if ticket.Assignee.Username != "" {
			usernames[ticket.Assignee.Username] = struct{}{}
		}
	}

	if err := s.ticketRepo.SoftDeleteByIDs(ctx, actualIDs); err != nil {
		return fmt.Errorf("admin soft delete tickets %v: %w", actualIDs, err)
	}

	for _, id := range actualIDs {
		s.auditSvc.LogUser(ctx, "TICKET_ADMIN_DELETE", "success", "0", slog.LevelWarn, map[string]any{
			"ticket_id": id,
		})
	}

	userList := slices.Collect(maps.Keys(usernames))

	// 推送事件：管理员删除工单，通知相关用户刷新列表
	s.publisher.PublishToUser(ctx, userList, push.Message{
		Name: push.EventTicket,
		Payload: push.EventPayload{
			Action: "refresh",
		},
	})
	return nil
}

// AdminPermanentDelete 管理员硬删除
func (s *Service) AdminPermanentDelete(ctx context.Context, ticketIDs ...uint) error {
	if len(ticketIDs) == 0 {
		return nil
	}
	tickets, err := s.ticketRepo.GetByIDs(ctx, ticketIDs) // GetByIDs 为 Unscoped，软删记录也能取到
	if err != nil {
		return fmt.Errorf("get tickets for admin permanent delete: %w", err)
	}
	if len(tickets) == 0 {
		return nil
	}

	actualIDs := make([]uint, 0, len(tickets))
	usernames := make(map[string]struct{})
	for _, ticket := range tickets {
		actualIDs = append(actualIDs, ticket.ID)
		if ticket.Reporter.Username != "" {
			usernames[ticket.Reporter.Username] = struct{}{}
		}
		if ticket.Assignee.Username != "" {
			usernames[ticket.Assignee.Username] = struct{}{}
		}
	}

	if err := s.ticketRepo.HardDeleteByIDs(ctx, actualIDs); err != nil {
		return fmt.Errorf("admin hard delete tickets %v: %w", actualIDs, err)
	}

	for _, id := range actualIDs {
		s.auditSvc.LogUser(ctx, "TICKET_PERMANENT_DELETE", "success", "0", slog.LevelWarn, map[string]any{
			"ticket_id": id,
			"warning":   "permanent deletion",
		})
	}

	userList := slices.Collect(maps.Keys(usernames))

	// 推送事件：管理员硬删除工单，通知相关用户刷新列表
	s.publisher.PublishToUser(ctx, userList, push.Message{
		Name: push.EventTicket,
		Payload: push.EventPayload{
			Action: "refresh",
		},
	})
	return nil
}

// --- 辅助方法 ---

// validateCreateRequest 验证创建工单请求
func (s *Service) validateCreateRequest(ctx context.Context, req CreateTicketRequest) error {
	if req.Title == "" {
		return ErrTitleRequired
	}

	if _, err := s.projectRepo.GetByID(ctx, req.ProjectID); err != nil {
		return fmt.Errorf("project(id=%d): %w", req.ProjectID, common.ErrNotFound)
	}

	if _, err := s.userRepo.GetByID(ctx, req.AssigneeID); err != nil {
		return fmt.Errorf("assignee(id=%d): %w", req.AssigneeID, common.ErrNotFound)
	}

	return nil
}

// calculateInitialSortOrder 新工单的初始排序
func (s *Service) calculateInitialSortOrder(ctx context.Context, projectID uint, status model.TicketStatus) float64 {
	maxSort, err := s.ticketRepo.GetMaxSortOrderInStatus(ctx, projectID, status)
	if err != nil || maxSort == 0 {
		return 1000.0
	}
	return maxSort + 1000.0
}
