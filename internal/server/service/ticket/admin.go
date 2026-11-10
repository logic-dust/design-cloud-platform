package ticket

import (
	"context"
	"log/slog"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

func checkBatch(ids []uint) ([]uint, error) {
	ids = dedupe(ids)
	if len(ids) == 0 || len(ids) > MaxBatchIDs {
		return nil, invalid("ids must contain 1 to 100 ticket ids")
	}
	return ids, nil
}

func notFoundErr(ids, missing []uint) error {
	if len(ids) == 1 {
		return ErrTicketNotFound
	}
	return batchNotFound(missing)
}

// AdminDelete 软删除未删除的工单；全有或全无。
func (s *Service) AdminDelete(ctx context.Context, actor Actor, ids ...uint) error {
	ids, err := checkBatch(ids)
	if err != nil {
		return err
	}
	var ts []model.Ticket
	err = s.ticketRepo.WithTx(ctx, func(tx *db.TicketRepo) error {
		if ts, err = tx.GetMany(ctx, ids, false); err != nil {
			return err
		}
		if missing := missingIDs(ids, ts); len(missing) > 0 {
			return notFoundErr(ids, missing)
		}
		if err := tx.SoftDelete(ctx, ids); err != nil {
			return err
		}
		return tx.CreateEvents(ctx, eventsFor(ids, actor.ID, model.TicketEventDeleted))
	})
	if err != nil {
		return err
	}
	for _, id := range ids {
		s.audit(ctx, actor, "TICKET_ADMIN_DELETE", slog.LevelWarn, map[string]any{"ticket_id": id})
	}
	s.notifyParticipants(ctx, actor, ts)
	return nil
}

// AdminRestore 恢复软删工单；工单未删除返回 409，所在项目已删除返回 409。
func (s *Service) AdminRestore(ctx context.Context, actor Actor, id uint) (TicketDTO, error) {
	t, err := s.ticketRepo.Get(ctx, id, true)
	if err != nil {
		return TicketDTO{}, mapRepoErr(err, ErrTicketNotFound)
	}
	if !t.DeletedAt.Valid {
		return TicketDTO{}, ErrTicketNotDeleted
	}
	if _, err := s.projectRepo.Get(ctx, t.ProjectID, false); err != nil {
		if isNotFound(err) {
			return TicketDTO{}, ErrProjectDeleted
		}
		return TicketDTO{}, err
	}
	err = s.ticketRepo.WithTx(ctx, func(tx *db.TicketRepo) error {
		if err := tx.Restore(ctx, []uint{id}); err != nil {
			return err
		}
		return tx.CreateEvents(ctx, eventsFor([]uint{id}, actor.ID, model.TicketEventRestored))
	})
	if err != nil {
		return TicketDTO{}, err
	}
	restored, err := s.ticketRepo.Get(ctx, id, false)
	if err != nil {
		return TicketDTO{}, mapRepoErr(err, ErrTicketNotFound)
	}
	s.audit(ctx, actor, "TICKET_RESTORE", slog.LevelWarn, map[string]any{"ticket_id": id})
	s.notifyParticipants(ctx, actor, []model.Ticket{restored})
	return toTicketDTO(restored), nil
}

// AdminPermanentDelete 永久删除工单（含已软删）及其评论与变更记录；全有或全无。
func (s *Service) AdminPermanentDelete(ctx context.Context, actor Actor, ids ...uint) error {
	ids, err := checkBatch(ids)
	if err != nil {
		return err
	}
	ts, err := s.ticketRepo.GetMany(ctx, ids, true)
	if err != nil {
		return err
	}
	if missing := missingIDs(ids, ts); len(missing) > 0 {
		return notFoundErr(ids, missing)
	}
	if err := s.ticketRepo.HardDelete(ctx, ids); err != nil {
		return err
	}
	for _, id := range ids {
		s.audit(ctx, actor, "TICKET_PERMANENT_DELETE", slog.LevelWarn, map[string]any{"ticket_id": id})
	}
	s.notifyParticipants(ctx, actor, ts)
	return nil
}
