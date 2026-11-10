package ticket

import (
	"context"
	"log/slog"
	"slices"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

// minSortGap 低于此间距时整列重排，避免浮点精度耗尽。
const minSortGap = 1e-6

// Move 把工单移动到目标列（项目 + 状态）中的指定位置，排序值由服务端计算。
// afterID：放在该工单之后；beforeID：放在该工单之前；都给时须相邻；都不给放到末尾。
func (s *Service) Move(ctx context.Context, actor Actor, id uint, req MoveTicketRequest) (TicketDTO, error) {
	version, err := requireVersion(req.Version)
	if err != nil {
		return TicketDTO{}, err
	}
	cur, err := s.ticketRepo.Get(ctx, id, false)
	if err != nil {
		return TicketDTO{}, mapRepoErr(err, ErrTicketNotFound)
	}
	if !canEdit(actor, cur) {
		return TicketDTO{}, ErrPermissionDenied
	}
	if cur.Version != version {
		return TicketDTO{}, ErrVersionConflict
	}
	status := cur.Status
	if req.Status != nil {
		if status, err = parseStatus(*req.Status); err != nil {
			return TicketDTO{}, err
		}
	}
	if (req.AfterID != nil && *req.AfterID == id) || (req.BeforeID != nil && *req.BeforeID == id) {
		return TicketDTO{}, invalid("invalid position")
	}

	err = s.ticketRepo.WithTx(ctx, func(tx *db.TicketRepo) error {
		col, err := tx.Column(ctx, cur.ProjectID, status)
		if err != nil {
			return err
		}
		col = slices.DeleteFunc(col, func(p db.TicketPosition) bool { return p.ID == id })
		idx, err := insertIndex(col, req.AfterID, req.BeforeID)
		if err != nil {
			return err
		}
		sortOrder, rebalanced := positionAt(col, idx)
		if rebalanced != nil {
			if err := tx.SetSortOrders(ctx, rebalanced); err != nil {
				return err
			}
		}
		fields := map[string]any{"sort_order": sortOrder}
		ev := model.TicketEvent{TicketID: id, ActorID: actor.ID, Action: model.TicketEventMoved}
		if status != cur.Status {
			fields["status"] = status
			fields["completed_at"] = completedAtFor(status)
			ev.Field = "status"
			ev.FromValue = new(cur.Status.String())
			ev.ToValue = new(status.String())
		}
		if err := tx.UpdateVersioned(ctx, id, version, fields); err != nil {
			return err
		}
		return tx.CreateEvents(ctx, []model.TicketEvent{ev})
	})
	if err != nil {
		return TicketDTO{}, mapRepoErr(err, ErrTicketNotFound)
	}
	moved, err := s.ticketRepo.Get(ctx, id, false)
	if err != nil {
		return TicketDTO{}, mapRepoErr(err, ErrTicketNotFound)
	}
	s.audit(ctx, actor, "TICKET_MOVE", slog.LevelInfo, map[string]any{"ticket_id": id, "status": status.String()})
	s.notify(ctx, actor, id, moved.Reporter.Username, derefUsername(moved.Assignee))
	return toTicketDTO(moved), nil
}

// insertIndex 返回工单插入 col 后所处的下标。
func insertIndex(col []db.TicketPosition, afterID, beforeID *uint) (int, error) {
	find := func(id uint) int {
		return slices.IndexFunc(col, func(p db.TicketPosition) bool { return p.ID == id })
	}
	switch {
	case afterID != nil && beforeID != nil:
		a, b := find(*afterID), find(*beforeID)
		if a < 0 || b < 0 || b != a+1 {
			return 0, invalid("invalid position")
		}
		return b, nil
	case afterID != nil:
		a := find(*afterID)
		if a < 0 {
			return 0, invalid("invalid position")
		}
		return a + 1, nil
	case beforeID != nil:
		b := find(*beforeID)
		if b < 0 {
			return 0, invalid("invalid position")
		}
		return b, nil
	default:
		return len(col), nil
	}
}

// positionAt 计算插入到 col[idx] 之前的排序值；间距不足时返回整列重排后的位置
// （不含被移动的工单）以及被移动工单的新排序值。
func positionAt(col []db.TicketPosition, idx int) (float64, []db.TicketPosition) {
	switch {
	case len(col) == 0:
		return sortGap, nil
	case idx == len(col):
		return col[idx-1].SortOrder + sortGap, nil
	case idx == 0:
		return col[0].SortOrder - sortGap, nil
	default:
		prev, next := col[idx-1].SortOrder, col[idx].SortOrder
		if next-prev >= minSortGap {
			return prev + (next-prev)/2, nil
		}
	}
	rebalanced := make([]db.TicketPosition, 0, len(col))
	pos := sortGap
	var mine float64
	for i, p := range col {
		if i == idx {
			mine = pos
			pos += sortGap
		}
		rebalanced = append(rebalanced, db.TicketPosition{ID: p.ID, SortOrder: pos})
		pos += sortGap
	}
	return mine, rebalanced
}
