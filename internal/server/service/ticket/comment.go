package ticket

import (
	"context"
	"fmt"
	"log/slog"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

// ListComments 返回未删除工单的评论（按发表顺序）。
func (s *Service) ListComments(ctx context.Context, ticketID uint, limit, offset int) (Page[CommentDTO], error) {
	if _, err := s.ticketRepo.Get(ctx, ticketID, false); err != nil {
		return Page[CommentDTO]{}, mapRepoErr(err, ErrTicketNotFound)
	}
	cs, total, err := s.ticketRepo.ListComments(ctx, ticketID, limit, offset)
	if err != nil {
		return Page[CommentDTO]{}, fmt.Errorf("list comments: %w", err)
	}
	items := make([]CommentDTO, 0, len(cs))
	for _, c := range cs {
		items = append(items, toCommentDTO(c))
	}
	return newPage(items, total, limit, offset), nil
}

// AddComment 任意已登录用户可评论未删除的工单。
func (s *Service) AddComment(ctx context.Context, actor Actor, ticketID uint, req CommentRequest) (CommentDTO, error) {
	body, err := validateCommentBody(req.Body)
	if err != nil {
		return CommentDTO{}, err
	}
	t, err := s.ticketRepo.Get(ctx, ticketID, false)
	if err != nil {
		return CommentDTO{}, mapRepoErr(err, ErrTicketNotFound)
	}
	c := model.TicketComment{TicketID: ticketID, AuthorID: actor.ID, Body: body}
	err = s.ticketRepo.WithTx(ctx, func(tx *db.TicketRepo) error {
		if err := tx.CreateComment(ctx, &c); err != nil {
			return err
		}
		return tx.CreateEvents(ctx, []model.TicketEvent{{TicketID: ticketID, ActorID: actor.ID, Action: model.TicketEventCommented, ToValue: new(uintStr(c.ID))}})
	})
	if err != nil {
		return CommentDTO{}, fmt.Errorf("add comment: %w", err)
	}
	created, err := s.ticketRepo.GetComment(ctx, ticketID, c.ID)
	if err != nil {
		return CommentDTO{}, mapRepoErr(err, ErrCommentNotFound)
	}
	s.audit(ctx, actor, "TICKET_COMMENT_CREATE", slog.LevelInfo, map[string]any{"ticket_id": ticketID, "comment_id": c.ID})
	s.notify(ctx, actor, ticketID, t.Reporter.Username, derefUsername(t.Assignee))
	return toCommentDTO(created), nil
}

func (s *Service) loadComment(ctx context.Context, ticketID, commentID uint) (model.TicketComment, error) {
	if _, err := s.ticketRepo.Get(ctx, ticketID, false); err != nil {
		return model.TicketComment{}, mapRepoErr(err, ErrTicketNotFound)
	}
	c, err := s.ticketRepo.GetComment(ctx, ticketID, commentID)
	if err != nil {
		return c, mapRepoErr(err, ErrCommentNotFound)
	}
	return c, nil
}

// EditComment 仅评论作者可编辑。
func (s *Service) EditComment(ctx context.Context, actor Actor, ticketID, commentID uint, req CommentRequest) (CommentDTO, error) {
	body, err := validateCommentBody(req.Body)
	if err != nil {
		return CommentDTO{}, err
	}
	c, err := s.loadComment(ctx, ticketID, commentID)
	if err != nil {
		return CommentDTO{}, err
	}
	if c.AuthorID != actor.ID {
		return CommentDTO{}, ErrPermissionDenied
	}
	if err := s.ticketRepo.UpdateCommentBody(ctx, commentID, body); err != nil {
		return CommentDTO{}, fmt.Errorf("edit comment: %w", err)
	}
	updated, err := s.ticketRepo.GetComment(ctx, ticketID, commentID)
	if err != nil {
		return CommentDTO{}, mapRepoErr(err, ErrCommentNotFound)
	}
	s.audit(ctx, actor, "TICKET_COMMENT_UPDATE", slog.LevelInfo, map[string]any{"ticket_id": ticketID, "comment_id": commentID})
	return toCommentDTO(updated), nil
}

// DeleteComment 评论作者或工单管理员可删除。
func (s *Service) DeleteComment(ctx context.Context, actor Actor, ticketID, commentID uint) error {
	c, err := s.loadComment(ctx, ticketID, commentID)
	if err != nil {
		return err
	}
	if c.AuthorID != actor.ID && !actor.IsTicketAdmin() {
		return ErrPermissionDenied
	}
	if err := s.ticketRepo.DeleteComment(ctx, commentID); err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	s.audit(ctx, actor, "TICKET_COMMENT_DELETE", slog.LevelInfo, map[string]any{"ticket_id": ticketID, "comment_id": commentID})
	return nil
}

// ListEvents 返回未删除工单的变更记录（按发生顺序）。
func (s *Service) ListEvents(ctx context.Context, ticketID uint, limit, offset int) (Page[EventDTO], error) {
	if _, err := s.ticketRepo.Get(ctx, ticketID, false); err != nil {
		return Page[EventDTO]{}, mapRepoErr(err, ErrTicketNotFound)
	}
	es, total, err := s.ticketRepo.ListEvents(ctx, ticketID, limit, offset)
	if err != nil {
		return Page[EventDTO]{}, fmt.Errorf("list events: %w", err)
	}
	items := make([]EventDTO, 0, len(es))
	for _, e := range es {
		items = append(items, toEventDTO(e))
	}
	return newPage(items, total, limit, offset), nil
}
