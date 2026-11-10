package db

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrStaleVersion 表示乐观锁版本号与库中不一致。
var ErrStaleVersion = errors.New("stale version")

// TicketSortKey 列表排序键（已映射到列名的白名单）。
type TicketSortKey string

const (
	TicketSortOrder     TicketSortKey = "sortOrder"
	TicketSortCreatedAt TicketSortKey = "createdAt"
	TicketSortUpdatedAt TicketSortKey = "updatedAt"
	TicketSortDueDate   TicketSortKey = "dueDate"
	TicketSortPriority  TicketSortKey = "priority"
)

var ticketSortColumns = map[TicketSortKey]string{
	TicketSortOrder:     "sort_order",
	TicketSortCreatedAt: "created_at",
	TicketSortUpdatedAt: "updated_at",
	TicketSortDueDate:   "due_date",
	TicketSortPriority:  "priority",
}

// TicketFilter 工单列表查询条件。
type TicketFilter struct {
	ProjectID      *uint
	ReporterID     *uint
	AssigneeID     *uint
	Unassigned     bool
	Types          []model.TicketType
	Statuses       []model.TicketStatus
	Priorities     []model.TicketPriority
	TitleQuery     string
	Sort           TicketSortKey
	Desc           bool
	Limit          int
	Offset         int
	IncludeDeleted bool
	// AfterSort / AfterID 启用 keyset 续页（仅 Sort=sortOrder）；同时设置时忽略 Offset。
	AfterSort *float64
	AfterID   *uint
}

// TicketPosition 看板列内一张工单的位置。
type TicketPosition struct {
	ID        uint
	SortOrder float64
}

// TicketRepo 管理工单、评论与变更记录。
type TicketRepo struct {
	store *Store
}

func (r *TicketRepo) db(ctx context.Context) *gorm.DB {
	return r.store.db.WithContext(ctx)
}

// WithTx 在一个事务内执行 fn，fn 收到绑定该事务的仓库。
func (r *TicketRepo) WithTx(ctx context.Context, fn func(tx *TicketRepo) error) error {
	return r.db(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&TicketRepo{store: &Store{db: tx, log: r.store.log}})
	})
}

func preloadTicketRefs(q *gorm.DB) *gorm.DB {
	return q.
		Preload("Project", func(db *gorm.DB) *gorm.DB { return db.Unscoped().Select("id", "name") }).
		Preload("Reporter", func(db *gorm.DB) *gorm.DB { return db.Select("id", "username") }).
		Preload("Assignee", func(db *gorm.DB) *gorm.DB { return db.Select("id", "username") })
}

func (r *TicketRepo) Create(ctx context.Context, t *model.Ticket) error {
	return r.db(ctx).Omit(clause.Associations).Create(t).Error
}

// Get 读取一张工单及其项目、报告人、指派人；includeDeleted 为 false 时已软删视为不存在。
func (r *TicketRepo) Get(ctx context.Context, id uint, includeDeleted bool) (model.Ticket, error) {
	var t model.Ticket
	q := preloadTicketRefs(r.db(ctx))
	if includeDeleted {
		q = q.Unscoped()
	}
	if err := q.First(&t, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Ticket{}, fmt.Errorf("ticket(id=%d): %w", id, contract.ErrNotFound)
		}
		return model.Ticket{}, err
	}
	return t, nil
}

// GetMany 按 ID 读取工单（不预加载关联），用于批量操作的存在性与权限判断。
func (r *TicketRepo) GetMany(ctx context.Context, ids []uint, includeDeleted bool) ([]model.Ticket, error) {
	var ts []model.Ticket
	if len(ids) == 0 {
		return ts, nil
	}
	q := r.db(ctx).Select("id", "project_id", "reporter_id", "assignee_id", "deleted_at").Where("id IN ?", ids)
	if includeDeleted {
		q = q.Unscoped()
	}
	return ts, q.Find(&ts).Error
}

// UpdateVersioned 在版本号匹配时更新字段并把版本号加 1。
// 工单不存在（含已软删）返回 contract.ErrNotFound；版本不符返回 ErrStaleVersion。
func (r *TicketRepo) UpdateVersioned(ctx context.Context, id uint, version int, fields map[string]any) error {
	updates := make(map[string]any, len(fields)+1)
	maps.Copy(updates, fields)
	updates["version"] = gorm.Expr("version + 1")
	res := r.db(ctx).Model(&model.Ticket{}).Where("id = ? AND version = ?", id, version).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 1 {
		return nil
	}
	var n int64
	if err := r.db(ctx).Model(&model.Ticket{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("ticket(id=%d): %w", id, contract.ErrNotFound)
	}
	return ErrStaleVersion
}

func (r *TicketRepo) SoftDelete(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db(ctx).Where("id IN ?", ids).Delete(&model.Ticket{}).Error
}

// Restore 恢复软删工单，并把版本号加 1。
func (r *TicketRepo) Restore(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db(ctx).Unscoped().Model(&model.Ticket{}).Where("id IN ?", ids).
		Updates(map[string]any{"deleted_at": nil, "version": gorm.Expr("version + 1")}).Error
}

// HardDelete 永久删除工单及其评论和变更记录。
func (r *TicketRepo) HardDelete(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return r.WithTx(ctx, func(tx *TicketRepo) error {
		if err := tx.db(ctx).Where("ticket_id IN ?", ids).Delete(&model.TicketComment{}).Error; err != nil {
			return err
		}
		if err := tx.db(ctx).Where("ticket_id IN ?", ids).Delete(&model.TicketEvent{}).Error; err != nil {
			return err
		}
		return tx.db(ctx).Unscoped().Where("id IN ?", ids).Delete(&model.Ticket{}).Error
	})
}

func (r *TicketRepo) List(ctx context.Context, f TicketFilter) ([]model.Ticket, int64, error) {
	if err := validateLimit(f.Limit); err != nil {
		return nil, 0, err
	}
	keyset := f.AfterSort != nil && f.AfterID != nil
	if keyset {
		sort := cmp.Or(f.Sort, TicketSortOrder)
		if sort != TicketSortOrder {
			return nil, 0, fmt.Errorf("keyset pagination only supports sort=%s", TicketSortOrder)
		}
	}

	q := r.db(ctx).Model(&model.Ticket{})
	if f.IncludeDeleted {
		q = q.Unscoped()
	}
	if f.ProjectID != nil {
		q = q.Where("project_id = ?", *f.ProjectID)
	}
	if f.ReporterID != nil {
		q = q.Where("reporter_id = ?", *f.ReporterID)
	}
	if f.Unassigned {
		q = q.Where("assignee_id IS NULL")
	} else if f.AssigneeID != nil {
		q = q.Where("assignee_id = ?", *f.AssigneeID)
	}
	if len(f.Types) > 0 {
		q = q.Where("type IN ?", f.Types)
	}
	if len(f.Statuses) > 0 {
		q = q.Where("status IN ?", f.Statuses)
	}
	if len(f.Priorities) > 0 {
		q = q.Where("priority IN ?", f.Priorities)
	}
	if f.TitleQuery != "" {
		q = q.Where("LOWER(title) LIKE ? ESCAPE '\\'", "%"+likeEscaper.Replace(strings.ToLower(f.TitleQuery))+"%")
	}

	// keyset 续页跳过 COUNT：瀑布流用 hasMore/nextAfter*，totalCount 返回 -1（未重算）。
	var total int64 = -1
	if !keyset {
		if err := q.Count(&total).Error; err != nil {
			return nil, 0, err
		}
	}

	col, ok := ticketSortColumns[f.Sort]
	if !ok {
		col = ticketSortColumns[TicketSortOrder]
	}
	dir := "ASC"
	if f.Desc {
		dir = "DESC"
	}
	if keyset {
		cmp := ">"
		if f.Desc {
			cmp = "<"
		}
		q = q.Where(
			fmt.Sprintf("(%s %s ?) OR (%s = ? AND id %s ?)", col, cmp, col, cmp),
			*f.AfterSort, *f.AfterSort, *f.AfterID,
		)
	}
	if col == "due_date" {
		q = q.Order("CASE WHEN due_date IS NULL THEN 1 ELSE 0 END")
	}
	q = q.Order(col + " " + dir).Order("id " + dir)

	offset := f.Offset
	if keyset {
		offset = 0
	}
	var ts []model.Ticket
	if err := preloadTicketRefs(q).Limit(f.Limit).Offset(offset).Find(&ts).Error; err != nil {
		return nil, 0, err
	}
	return ts, total, nil
}

// CountActiveByProject 统计项目中未删除的工单数。
func (r *TicketRepo) CountActiveByProject(ctx context.Context, projectID uint) (int64, error) {
	var n int64
	err := r.db(ctx).Model(&model.Ticket{}).Where("project_id = ?", projectID).Count(&n).Error
	return n, err
}

// CountByStatus 按状态统计未删除的工单；projectID 为 0 表示全部项目。
func (r *TicketRepo) CountByStatus(ctx context.Context, projectID uint) (map[model.TicketStatus]int, error) {
	var rows []struct {
		Status model.TicketStatus
		Count  int
	}
	q := r.db(ctx).Model(&model.Ticket{}).Select("status, COUNT(*) AS count")
	if projectID != 0 {
		q = q.Where("project_id = ?", projectID)
	}
	if err := q.Group("status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[model.TicketStatus]int, len(rows))
	for _, row := range rows {
		out[row.Status] = row.Count
	}
	return out, nil
}

// TimesInRange 返回 [start, end) 内未删除工单的 column（created_at 或 completed_at）取值，
// 由调用方按时区分桶。
func (r *TicketRepo) TimesInRange(ctx context.Context, column string, projectID uint, start, end time.Time) ([]time.Time, error) {
	if column != "created_at" && column != "completed_at" {
		return nil, fmt.Errorf("unsupported time column %q", column)
	}
	q := r.db(ctx).Model(&model.Ticket{}).Where(column+" >= ? AND "+column+" < ?", start, end)
	if projectID != 0 {
		q = q.Where("project_id = ?", projectID)
	}
	var out []time.Time
	return out, q.Pluck(column, &out).Error
}

// MaxSortOrder 返回列（项目 + 状态）中未删除工单的最大排序值，空列为 0。
func (r *TicketRepo) MaxSortOrder(ctx context.Context, projectID uint, status model.TicketStatus) (float64, error) {
	var v float64
	err := r.db(ctx).Model(&model.Ticket{}).
		Where("project_id = ? AND status = ?", projectID, status).
		Select("COALESCE(MAX(sort_order), 0)").Scan(&v).Error
	return v, err
}

// Column 按看板顺序返回列（项目 + 状态）中未删除工单的位置。
func (r *TicketRepo) Column(ctx context.Context, projectID uint, status model.TicketStatus) ([]TicketPosition, error) {
	var out []TicketPosition
	err := r.db(ctx).Model(&model.Ticket{}).
		Where("project_id = ? AND status = ?", projectID, status).
		Order("sort_order ASC").Order("id ASC").
		Select("id", "sort_order").Scan(&out).Error
	return out, err
}

// SetSortOrders 直接写入排序值（列重排用），不改变版本号。
func (r *TicketRepo) SetSortOrders(ctx context.Context, positions []TicketPosition) error {
	for _, p := range positions {
		if err := r.db(ctx).Model(&model.Ticket{}).Where("id = ?", p.ID).
			UpdateColumn("sort_order", p.SortOrder).Error; err != nil {
			return err
		}
	}
	return nil
}

// --- 评论 ---

func (r *TicketRepo) CreateComment(ctx context.Context, c *model.TicketComment) error {
	return r.db(ctx).Omit(clause.Associations).Create(c).Error
}

func (r *TicketRepo) GetComment(ctx context.Context, ticketID, commentID uint) (model.TicketComment, error) {
	var c model.TicketComment
	err := r.db(ctx).
		Preload("Author", func(db *gorm.DB) *gorm.DB { return db.Select("id", "username") }).
		Where("ticket_id = ?", ticketID).First(&c, commentID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c, fmt.Errorf("ticket comment(id=%d): %w", commentID, contract.ErrNotFound)
	}
	return c, err
}

func (r *TicketRepo) UpdateCommentBody(ctx context.Context, commentID uint, body string) error {
	return r.db(ctx).Model(&model.TicketComment{}).Where("id = ?", commentID).Update("body", body).Error
}

func (r *TicketRepo) DeleteComment(ctx context.Context, commentID uint) error {
	return r.db(ctx).Delete(&model.TicketComment{}, commentID).Error
}

func (r *TicketRepo) ListComments(ctx context.Context, ticketID uint, limit, offset int) ([]model.TicketComment, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	q := r.db(ctx).Model(&model.TicketComment{}).Where("ticket_id = ?", ticketID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []model.TicketComment
	err := q.Preload("Author", func(db *gorm.DB) *gorm.DB { return db.Select("id", "username") }).
		Order("id ASC").Limit(limit).Offset(offset).Find(&out).Error
	return out, total, err
}

// --- 变更记录 ---

func (r *TicketRepo) CreateEvents(ctx context.Context, events []model.TicketEvent) error {
	if len(events) == 0 {
		return nil
	}
	return r.db(ctx).Omit(clause.Associations).Create(&events).Error
}

func (r *TicketRepo) ListEvents(ctx context.Context, ticketID uint, limit, offset int) ([]model.TicketEvent, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	q := r.db(ctx).Model(&model.TicketEvent{}).Where("ticket_id = ?", ticketID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []model.TicketEvent
	err := q.Preload("Actor", func(db *gorm.DB) *gorm.DB { return db.Select("id", "username") }).
		Order("id ASC").Limit(limit).Offset(offset).Find(&out).Error
	return out, total, err
}
