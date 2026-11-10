package ticket

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

// sortGap 是看板列内相邻工单的初始排序间距。
const sortGap = 1024.0

// Create 创建工单；报告人为当前用户，新工单排在所在列末尾。
func (s *Service) Create(ctx context.Context, actor Actor, req CreateTicketRequest) (TicketDTO, error) {
	t := model.Ticket{
		ReporterID:  actor.ID,
		Description: req.Description,
		DueDate:     req.DueDate,
		Type:        model.TicketTypeTask,
		Priority:    model.TicketPriorityMedium,
		Status:      model.TicketStatusToDo,
		Version:     1,
	}
	var err error
	if _, err = s.activeProject(ctx, req.ProjectID); err != nil {
		return TicketDTO{}, err
	}
	t.ProjectID = req.ProjectID
	if t.Title, err = validateTitle(req.Title); err != nil {
		return TicketDTO{}, err
	}
	if err = validateDescription(req.Description); err != nil {
		return TicketDTO{}, err
	}
	if req.Type != nil {
		if t.Type, err = parseType(*req.Type); err != nil {
			return TicketDTO{}, err
		}
	}
	if req.Priority != nil {
		if t.Priority, err = parsePriority(*req.Priority); err != nil {
			return TicketDTO{}, err
		}
	}
	if req.Status != nil {
		if t.Status, err = parseStatus(*req.Status); err != nil {
			return TicketDTO{}, err
		}
	}
	if req.AssigneeID != nil {
		if _, err = s.validateAssignee(ctx, *req.AssigneeID); err != nil {
			return TicketDTO{}, err
		}
		t.AssigneeID = req.AssigneeID
	}
	if err = validateCriteria(req.AcceptanceCriteria); err != nil {
		return TicketDTO{}, err
	}
	t.AcceptanceCriteria = req.AcceptanceCriteria
	if err = s.validateAttachments(ctx, req.Attachments, actor.Username); err != nil {
		return TicketDTO{}, err
	}
	t.Attachments = req.Attachments
	if t.Status == model.TicketStatusDone {
		now := time.Now()
		t.CompletedAt = &now
	}

	err = s.ticketRepo.WithTx(ctx, func(tx *db.TicketRepo) error {
		maxSort, err := tx.MaxSortOrder(ctx, t.ProjectID, t.Status)
		if err != nil {
			return err
		}
		t.SortOrder = maxSort + sortGap
		if err := tx.Create(ctx, &t); err != nil {
			return err
		}
		return tx.CreateEvents(ctx, []model.TicketEvent{{TicketID: t.ID, ActorID: actor.ID, Action: model.TicketEventCreated}})
	})
	if err != nil {
		return TicketDTO{}, fmt.Errorf("create ticket: %w", err)
	}

	created, err := s.ticketRepo.Get(ctx, t.ID, false)
	if err != nil {
		return TicketDTO{}, fmt.Errorf("load created ticket: %w", err)
	}
	s.audit(ctx, actor, "TICKET_CREATE", slog.LevelInfo, map[string]any{
		"ticket_id": created.ID, "project_id": created.ProjectID, "title": created.Title,
	})
	s.notify(ctx, actor, 0, created.Reporter.Username, derefUsername(created.Assignee))
	return toTicketDTO(created), nil
}

// Get 返回未删除的工单。
func (s *Service) Get(ctx context.Context, id uint) (TicketDTO, error) {
	t, err := s.ticketRepo.Get(ctx, id, false)
	if err != nil {
		return TicketDTO{}, mapRepoErr(err, ErrTicketNotFound)
	}
	return toTicketDTO(t), nil
}

// AdminGet 返回工单（含已软删）。
func (s *Service) AdminGet(ctx context.Context, id uint) (TicketDTO, error) {
	t, err := s.ticketRepo.Get(ctx, id, true)
	if err != nil {
		return TicketDTO{}, mapRepoErr(err, ErrTicketNotFound)
	}
	return toTicketDTO(t), nil
}

// List 返回工单分页列表。
// 携带 AfterSort+AfterID 时走 keyset 续页（仅 sort=sortOrder），忽略 Offset；仍返回准确 totalCount。
func (s *Service) List(ctx context.Context, q ListQuery) (Page[TicketDTO], error) {
	sort := cmp.Or(db.TicketSortKey(q.Sort), db.TicketSortOrder)
	desc := sort != db.TicketSortOrder
	switch q.Order {
	case "asc":
		desc = false
	case "desc":
		desc = true
	}
	keyset := q.HasKeyset()
	if keyset && sort != db.TicketSortOrder {
		return Page[TicketDTO]{}, ErrKeysetSortUnsupported
	}
	offset := q.Offset
	limit := q.Limit
	fetchLimit := limit
	if keyset {
		offset = 0
		fetchLimit = limit + 1 // 多取一条判定 hasMore
	}
	ts, total, err := s.ticketRepo.List(ctx, db.TicketFilter{
		ProjectID: q.ProjectID, ReporterID: q.ReporterID, AssigneeID: q.AssigneeID, Unassigned: q.Unassigned,
		Types: q.Types, Statuses: q.Statuses, Priorities: q.Priorities, TitleQuery: q.Q,
		Sort: sort, Desc: desc, Limit: fetchLimit, Offset: offset, IncludeDeleted: q.IncludeDeleted,
		AfterSort: q.AfterSort, AfterID: q.AfterID,
	})
	if err != nil {
		return Page[TicketDTO]{}, fmt.Errorf("list tickets: %w", err)
	}
	var hasMore bool
	if keyset {
		hasMore = len(ts) > limit
		if hasMore {
			ts = ts[:limit]
		}
	} else {
		hasMore = int64(offset+min(len(ts), limit)) < total
	}
	items := make([]TicketDTO, 0, len(ts))
	for _, t := range ts {
		items = append(items, toTicketDTO(t))
	}
	page := Page[TicketDTO]{
		Items: items, TotalCount: total, Limit: limit, Offset: offset, HasMore: hasMore,
	}
	if sort == db.TicketSortOrder && len(items) > 0 {
		last := items[len(items)-1]
		sortVal, id := last.SortOrder, last.ID
		page.NextAfterSort = &sortVal
		page.NextAfterID = &id
	}
	return page, nil
}

func canEdit(actor Actor, t model.Ticket) bool {
	return actor.IsTicketAdmin() || t.ReporterID == actor.ID || (t.AssigneeID != nil && *t.AssigneeID == actor.ID)
}

func canManage(actor Actor, t model.Ticket) bool {
	return actor.IsTicketAdmin() || t.ReporterID == actor.ID
}

// Update 按字段部分更新工单（乐观锁）。字段值与当前一致时不产生变更。
func (s *Service) Update(ctx context.Context, actor Actor, id uint, req UpdateTicketRequest) (TicketDTO, error) {
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

	fields := map[string]any{}
	var events []model.TicketEvent
	change := func(field string, from, to *string) {
		events = append(events, model.TicketEvent{TicketID: id, ActorID: actor.ID, Action: model.TicketEventUpdated, Field: field, FromValue: from, ToValue: to})
	}

	if req.Title != nil {
		title, err := validateTitle(*req.Title)
		if err != nil {
			return TicketDTO{}, err
		}
		if title != cur.Title {
			fields["title"] = title
			change("title", new(cur.Title), new(title))
		}
	}
	if req.Description != nil {
		if err := validateDescription(*req.Description); err != nil {
			return TicketDTO{}, err
		}
		if *req.Description != cur.Description {
			fields["description"] = *req.Description
			change("description", nil, nil)
		}
	}
	if req.Type != nil {
		v, err := parseType(*req.Type)
		if err != nil {
			return TicketDTO{}, err
		}
		if v != cur.Type {
			fields["type"] = v
			change("type", new(cur.Type.String()), new(v.String()))
		}
	}
	if req.Priority != nil {
		v, err := parsePriority(*req.Priority)
		if err != nil {
			return TicketDTO{}, err
		}
		if v != cur.Priority {
			fields["priority"] = v
			change("priority", new(cur.Priority.String()), new(v.String()))
		}
	}
	newStatus := cur.Status
	if req.Status != nil {
		if newStatus, err = parseStatus(*req.Status); err != nil {
			return TicketDTO{}, err
		}
	}
	if req.AssigneeID.Set {
		curID := cur.AssigneeID
		changed := (curID == nil) != (req.AssigneeID.Value == nil) || (curID != nil && *curID != *req.AssigneeID.Value)
		if changed {
			if !canManage(actor, cur) {
				return TicketDTO{}, ErrPermissionDenied
			}
			var to *string
			if req.AssigneeID.Value != nil {
				u, err := s.validateAssignee(ctx, *req.AssigneeID.Value)
				if err != nil {
					return TicketDTO{}, err
				}
				to = new(u.Username)
				fields["assignee_id"] = *req.AssigneeID.Value
			} else {
				fields["assignee_id"] = nil
			}
			change("assignee", usernameOf(cur.Assignee), to)
		}
	}
	if req.DueDate.Set && !sameTime(cur.DueDate, req.DueDate.Value) {
		fields["due_date"] = req.DueDate.Value
		change("dueDate", timeStr(cur.DueDate), timeStr(req.DueDate.Value))
	}
	if req.AcceptanceCriteria.Set {
		if err := validateCriteria(req.AcceptanceCriteria.Value); err != nil {
			return TicketDTO{}, err
		}
		if !reflect.DeepEqual(cur.AcceptanceCriteria, req.AcceptanceCriteria.Value) {
			fields["acceptance_criteria"] = jsonColumn(req.AcceptanceCriteria.Value)
			change("acceptanceCriteria", nil, nil)
		}
	}
	if req.Attachments != nil {
		atts := *req.Attachments
		if err := s.validateAttachments(ctx, atts, actor.Username); err != nil {
			return TicketDTO{}, err
		}
		if !(len(atts) == 0 && len(cur.Attachments) == 0) && !reflect.DeepEqual(atts, cur.Attachments) {
			fields["attachments"] = jsonColumn(atts)
			change("attachments", nil, nil)
		}
	}

	err = s.ticketRepo.WithTx(ctx, func(tx *db.TicketRepo) error {
		if newStatus != cur.Status {
			maxSort, err := tx.MaxSortOrder(ctx, cur.ProjectID, newStatus)
			if err != nil {
				return err
			}
			fields["status"] = newStatus
			fields["sort_order"] = maxSort + sortGap
			fields["completed_at"] = completedAtFor(newStatus)
			change("status", new(cur.Status.String()), new(newStatus.String()))
		}
		if len(fields) == 0 {
			return nil
		}
		if err := tx.UpdateVersioned(ctx, id, version, fields); err != nil {
			return err
		}
		return tx.CreateEvents(ctx, events)
	})
	if err != nil {
		return TicketDTO{}, mapRepoErr(err, ErrTicketNotFound)
	}
	if len(fields) == 0 {
		return toTicketDTO(cur), nil
	}

	updated, err := s.ticketRepo.Get(ctx, id, false)
	if err != nil {
		return TicketDTO{}, mapRepoErr(err, ErrTicketNotFound)
	}
	changed := make([]string, 0, len(events))
	for _, e := range events {
		changed = append(changed, e.Field)
	}
	s.audit(ctx, actor, "TICKET_UPDATE", slog.LevelInfo, map[string]any{"ticket_id": id, "fields": changed})
	s.notify(ctx, actor, id, updated.Reporter.Username, derefUsername(updated.Assignee), derefUsername(cur.Assignee))
	return toTicketDTO(updated), nil
}

func completedAtFor(status model.TicketStatus) any {
	if status == model.TicketStatusDone {
		return time.Now()
	}
	return nil
}

// jsonColumn 把 JSON 列的新值包装为 GORM 可写入 map 更新的值（nil 写 NULL）。
func jsonColumn(v any) any {
	switch x := v.(type) {
	case *model.TicketAcceptanceCriteria:
		if x == nil {
			return nil
		}
	case []model.TicketAttachment:
		if len(x) == 0 {
			return nil
		}
	}
	data, err := jsonMarshal(v)
	if err != nil {
		return nil
	}
	return string(data)
}

func derefUsername(u *model.User) string {
	if u == nil {
		return ""
	}
	return u.Username
}

// Delete 软删除工单（报告人或工单管理员）；全有或全无。
func (s *Service) Delete(ctx context.Context, actor Actor, ids ...uint) error {
	ids = dedupe(ids)
	if len(ids) == 0 || len(ids) > MaxBatchIDs {
		return invalid("ids must contain 1 to 100 ticket ids")
	}
	var deleted []model.Ticket
	err := s.ticketRepo.WithTx(ctx, func(tx *db.TicketRepo) error {
		ts, err := tx.GetMany(ctx, ids, false)
		if err != nil {
			return err
		}
		if missing := missingIDs(ids, ts); len(missing) > 0 {
			if len(ids) == 1 {
				return ErrTicketNotFound
			}
			return batchNotFound(missing)
		}
		var forbidden []uint
		for _, t := range ts {
			if !canManage(actor, t) {
				forbidden = append(forbidden, t.ID)
			}
		}
		if len(forbidden) > 0 {
			if len(ids) == 1 {
				return ErrPermissionDenied
			}
			return batchForbidden(forbidden)
		}
		if err := tx.SoftDelete(ctx, ids); err != nil {
			return err
		}
		deleted = ts
		return tx.CreateEvents(ctx, eventsFor(ids, actor.ID, model.TicketEventDeleted))
	})
	if err != nil {
		return err
	}
	for _, id := range ids {
		s.audit(ctx, actor, "TICKET_DELETE", slog.LevelInfo, map[string]any{"ticket_id": id})
	}
	s.notifyParticipants(ctx, actor, deleted)
	return nil
}

func eventsFor(ids []uint, actorID uint, action model.TicketEventAction) []model.TicketEvent {
	out := make([]model.TicketEvent, 0, len(ids))
	for _, id := range ids {
		out = append(out, model.TicketEvent{TicketID: id, ActorID: actorID, Action: action})
	}
	return out
}

func missingIDs(ids []uint, found []model.Ticket) []uint {
	have := make(map[uint]struct{}, len(found))
	for _, t := range found {
		have[t.ID] = struct{}{}
	}
	var out []uint
	for _, id := range ids {
		if _, ok := have[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}

// notifyParticipants 通知一组工单的报告人与指派人刷新列表。
func (s *Service) notifyParticipants(ctx context.Context, actor Actor, ts []model.Ticket) {
	userIDs := map[uint]struct{}{}
	for _, t := range ts {
		userIDs[t.ReporterID] = struct{}{}
		if t.AssigneeID != nil {
			userIDs[*t.AssigneeID] = struct{}{}
		}
	}
	var names []string
	for id := range userIDs {
		if u, err := s.userRepo.GetByID(ctx, id); err == nil {
			names = append(names, u.Username)
		}
	}
	s.notify(ctx, actor, 0, names...)
}
