package ticket

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/model"
)

// --- 项目管理 ---

// CreateProject 创建项目
func (s *Service) CreateProject(ctx context.Context, req CreateProjectRequest, operatorID uint, operatorUsername string) (*model.TicketProject, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, ErrProjectNameRequired
	}
	if len(name) > 128 {
		return nil, ErrProjectNameTooLong
	}

	project := &model.TicketProject{
		Name:        name,
		Description: req.Description,
	}

	if err := s.projectRepo.Create(ctx, project); err != nil {
		if common.IsUniqueConstraintError(err) {
			return nil, fmt.Errorf("%w: %w", ErrProjectNameConflict, err)
		}
		return nil, fmt.Errorf("create ticket project: %w", err)
	}

	s.auditSvc.LogUser(ctx, "PROJECT_CREATE", "success", formatUint(operatorID), slog.LevelInfo, map[string]any{
		"project_id":        project.ID,
		"project_name":      name,
		"operator_username": operatorUsername,
	})

	return project, nil
}

// ListProjects 获取项目列表
func (s *Service) ListProjects(ctx context.Context, limit, offset int, includeArchived bool) (*PagedResponse, error) {
	projects, total, err := s.projectRepo.List(ctx, limit, offset, includeArchived, false)
	if err != nil {
		return nil, fmt.Errorf("list ticket projects: %w", err)
	}

	hasMore := int64(offset+limit) < total

	return &PagedResponse{
		Items:      projects,
		TotalCount: total,
		Limit:      limit,
		Offset:     offset,
		HasMore:    hasMore,
	}, nil
}

// AdminListProjects 管理员获取项目列表（含归档和已删除）
func (s *Service) AdminListProjects(ctx context.Context, limit, offset int, includeArchived, includeDeleted bool) (*PagedResponse, error) {
	projects, total, err := s.projectRepo.List(ctx, limit, offset, includeArchived, includeDeleted)
	if err != nil {
		return nil, fmt.Errorf("list ticket projects: %w", err)
	}

	hasMore := int64(offset+limit) < total

	return &PagedResponse{
		Items:      projects,
		TotalCount: total,
		Limit:      limit,
		Offset:     offset,
		HasMore:    hasMore,
	}, nil
}

// UpdateProject 更新项目
func (s *Service) UpdateProject(ctx context.Context, id uint, req UpdateProjectRequest, operatorID uint, operatorUsername string) (*model.TicketProject, error) {
	project, err := s.projectRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get project for update(id=%d): %w", id, err)
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, ErrProjectNameRequired
		}
		if len(name) > 128 {
			return nil, ErrProjectNameTooLong
		}
		project.Name = name
	}
	if req.Description != nil {
		project.Description = *req.Description
	}

	if err := s.projectRepo.Update(ctx, &project); err != nil {
		if common.IsUniqueConstraintError(err) {
			return nil, fmt.Errorf("%w: %w", ErrProjectNameConflict, err)
		}
		return nil, fmt.Errorf("update ticket project: %w", err)
	}

	s.auditSvc.LogUser(ctx, "PROJECT_UPDATE", "success", formatUint(operatorID), slog.LevelInfo, map[string]any{
		"project_id":        id,
		"operator_username": operatorUsername,
	})

	return &project, nil
}

// DeleteProject 删除项目（需检查是否有活跃工单）
func (s *Service) DeleteProject(ctx context.Context, projectID uint, operatorID uint, operatorUsername string) error {
	count, err := s.ticketRepo.CountByProject(ctx, projectID, false)
	if err != nil {
		return fmt.Errorf("count tickets for project(id=%d): %w", projectID, err)
	}

	if count > 0 {
		return fmt.Errorf("%w: %d active tickets", ErrProjectHasTickets, count)
	}

	if err := s.projectRepo.SoftDelete(ctx, projectID); err != nil {
		return fmt.Errorf("soft delete project(id=%d): %w", projectID, err)
	}

	s.auditSvc.LogUser(ctx, "PROJECT_DELETE", "success", formatUint(operatorID), slog.LevelInfo, map[string]any{
		"project_id":        projectID,
		"operator_username": operatorUsername,
	})
	return nil
}

// ArchiveProject 归档/取消归档项目
func (s *Service) ArchiveProject(ctx context.Context, projectID uint, archived bool, operatorID uint, operatorUsername string) (*model.TicketProject, error) {
	if err := s.projectRepo.Archive(ctx, projectID, archived); err != nil {
		return nil, fmt.Errorf("archive ticket project: %w", err)
	}

	project, err := s.projectRepo.GetByID(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("get project after archive(id=%d): %w", projectID, err)
	}

	s.auditSvc.LogUser(ctx, "PROJECT_ARCHIVE", "success", formatUint(operatorID), slog.LevelInfo, map[string]any{
		"project_id":        projectID,
		"archived":          archived,
		"operator_username": operatorUsername,
	})

	return &project, nil
}
