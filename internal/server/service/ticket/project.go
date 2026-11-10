package ticket

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

func validateProjectName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", invalid("name is required")
	case runeLen(name) > maxProjectNameRunes:
		return "", invalid("name is too long")
	}
	return name, nil
}

func validateProjectDescription(s string) error {
	if runeLen(s) > maxProjectDescRunes {
		return invalid("description is too long")
	}
	return nil
}

// CreateProject 创建项目；名称在未删除项目中唯一。
func (s *Service) CreateProject(ctx context.Context, actor Actor, req CreateProjectRequest) (ProjectDTO, error) {
	name, err := validateProjectName(req.Name)
	if err != nil {
		return ProjectDTO{}, err
	}
	if err := validateProjectDescription(req.Description); err != nil {
		return ProjectDTO{}, err
	}
	p := modelProject(name, req.Description)
	if err := s.projectRepo.Create(ctx, &p); err != nil {
		if db.IsUniqueConstraintError(err) {
			return ProjectDTO{}, ErrProjectNameConflict
		}
		return ProjectDTO{}, fmt.Errorf("create ticket project: %w", err)
	}
	s.audit(ctx, actor, "PROJECT_CREATE", slog.LevelInfo, map[string]any{"project_id": p.ID, "project_name": name})
	return toProjectDTO(p), nil
}

// ListProjects 返回项目分页列表。
func (s *Service) ListProjects(ctx context.Context, limit, offset int, includeArchived, includeDeleted bool) (Page[ProjectDTO], error) {
	ps, total, err := s.projectRepo.List(ctx, limit, offset, includeArchived, includeDeleted)
	if err != nil {
		return Page[ProjectDTO]{}, fmt.Errorf("list ticket projects: %w", err)
	}
	items := make([]ProjectDTO, 0, len(ps))
	for _, p := range ps {
		items = append(items, toProjectDTO(p))
	}
	return newPage(items, total, limit, offset), nil
}

// UpdateProject 部分更新未删除的项目（名称、描述、归档状态）。
func (s *Service) UpdateProject(ctx context.Context, actor Actor, id uint, req UpdateProjectRequest) (ProjectDTO, error) {
	fields := map[string]any{}
	if req.Name != nil {
		name, err := validateProjectName(*req.Name)
		if err != nil {
			return ProjectDTO{}, err
		}
		fields["name"] = name
	}
	if req.Description != nil {
		if err := validateProjectDescription(*req.Description); err != nil {
			return ProjectDTO{}, err
		}
		fields["description"] = *req.Description
	}
	if req.IsArchived != nil {
		fields["is_archived"] = *req.IsArchived
	}
	if _, err := s.projectRepo.Get(ctx, id, false); err != nil {
		return ProjectDTO{}, mapRepoErr(err, ErrProjectNotFound)
	}
	if err := s.projectRepo.UpdateFields(ctx, id, fields); err != nil {
		if db.IsUniqueConstraintError(err) {
			return ProjectDTO{}, ErrProjectNameConflict
		}
		return ProjectDTO{}, mapRepoErr(err, ErrProjectNotFound)
	}
	p, err := s.projectRepo.Get(ctx, id, false)
	if err != nil {
		return ProjectDTO{}, mapRepoErr(err, ErrProjectNotFound)
	}
	keys := slices.Collect(maps.Keys(fields))
	s.audit(ctx, actor, "PROJECT_UPDATE", slog.LevelInfo, map[string]any{"project_id": id, "fields": keys})
	return toProjectDTO(p), nil
}

// DeleteProject 软删除项目；仍有未删除工单时拒绝。
func (s *Service) DeleteProject(ctx context.Context, actor Actor, id uint) error {
	if _, err := s.projectRepo.Get(ctx, id, false); err != nil {
		return mapRepoErr(err, ErrProjectNotFound)
	}
	n, err := s.ticketRepo.CountActiveByProject(ctx, id)
	if err != nil {
		return fmt.Errorf("count tickets of project %d: %w", id, err)
	}
	if n > 0 {
		return ErrProjectHasTickets
	}
	if err := s.projectRepo.SoftDelete(ctx, id); err != nil {
		return mapRepoErr(err, ErrProjectNotFound)
	}
	s.audit(ctx, actor, "PROJECT_DELETE", slog.LevelInfo, map[string]any{"project_id": id})
	return nil
}

func modelProject(name, description string) model.TicketProject {
	return model.TicketProject{Name: name, Description: description}
}
