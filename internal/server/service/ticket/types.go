package ticket

import (
	"bytes"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"time"

	"design-cloud-engine/internal/model"
)

// Nullable 区分 PATCH 请求中"未传"（Set=false）与"传 null"（Set=true, Value=nil）。
type Nullable[T any] struct {
	Set   bool
	Value *T
}

func (n *Nullable[T]) UnmarshalJSON(data []byte) error {
	n.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		n.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	n.Value = &v
	return nil
}

// Actor 是发起操作的当前用户。
type Actor struct {
	ID       uint
	Username string
	Role     model.Role
	Scopes   model.AdminScopes
}

// IsTicketAdmin 与 /api/admin/system/tickets 的路由守卫一致：
// superAdmin、systemAdmin，或持有 tickets 管理范围的普通用户。
func (a Actor) IsTicketAdmin() bool {
	switch a.Role {
	case model.RoleSuperAdmin, model.RoleSystemAdmin:
		return true
	case model.RoleNormal:
		return a.Scopes.Has(model.ScopeTickets)
	default:
		return false
	}
}

// --- 请求 ---

type CreateTicketRequest struct {
	ProjectID          uint                            `json:"projectId"`
	Type               *string                         `json:"type"`
	Title              string                          `json:"title"`
	Description        string                          `json:"description"`
	AssigneeID         *uint                           `json:"assigneeId"`
	Priority           *string                         `json:"priority"`
	Status             *string                         `json:"status"`
	DueDate            *time.Time                      `json:"dueDate"`
	AcceptanceCriteria *model.TicketAcceptanceCriteria `json:"acceptanceCriteria"`
	Attachments        []model.TicketAttachment        `json:"attachments"`
}

type UpdateTicketRequest struct {
	Version            *int                                     `json:"version"`
	Type               *string                                  `json:"type"`
	Title              *string                                  `json:"title"`
	Description        *string                                  `json:"description"`
	AssigneeID         Nullable[uint]                           `json:"assigneeId"`
	Priority           *string                                  `json:"priority"`
	Status             *string                                  `json:"status"`
	DueDate            Nullable[time.Time]                      `json:"dueDate"`
	AcceptanceCriteria Nullable[model.TicketAcceptanceCriteria] `json:"acceptanceCriteria"`
	Attachments        *[]model.TicketAttachment                `json:"attachments"`
}

type MoveTicketRequest struct {
	Version  *int    `json:"version"`
	Status   *string `json:"status"`
	AfterID  *uint   `json:"afterId"`
	BeforeID *uint   `json:"beforeId"`
}

// ListQuery 是已解析、已校验的列表条件（由 handler 从查询参数构造）。
type ListQuery struct {
	ProjectID      *uint
	ReporterID     *uint
	AssigneeID     *uint
	Unassigned     bool
	Types          []model.TicketType
	Statuses       []model.TicketStatus
	Priorities     []model.TicketPriority
	Q              string
	Sort           string
	Order          string
	Limit          int
	Offset         int
	IncludeDeleted bool
	// AfterSort / AfterID 为 sortOrder keyset 续页游标；成对出现时优先于 Offset。
	AfterSort *float64
	AfterID   *uint
}

// HasKeyset 表示请求携带了完整的 keyset 游标。
func (q ListQuery) HasKeyset() bool {
	return q.AfterSort != nil && q.AfterID != nil
}

type CreateProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type UpdateProjectRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	IsArchived  *bool   `json:"isArchived"`
}

type CommentRequest struct {
	Body string `json:"body"`
}

// --- 响应 ---

type UserRef struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
}

type ProjectRef struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type TicketDTO struct {
	ID                 uint                            `json:"id"`
	Project            ProjectRef                      `json:"project"`
	Type               model.TicketType                `json:"type"`
	Title              string                          `json:"title"`
	Description        string                          `json:"description"`
	Reporter           UserRef                         `json:"reporter"`
	Assignee           *UserRef                        `json:"assignee"`
	Priority           model.TicketPriority            `json:"priority"`
	Status             model.TicketStatus              `json:"status"`
	DueDate            *time.Time                      `json:"dueDate"`
	AcceptanceCriteria *model.TicketAcceptanceCriteria `json:"acceptanceCriteria"`
	Attachments        []model.TicketAttachment        `json:"attachments"`
	SortOrder          float64                         `json:"sortOrder"`
	Version            int                             `json:"version"`
	CompletedAt        *time.Time                      `json:"completedAt"`
	CreatedAt          time.Time                       `json:"createdAt"`
	UpdatedAt          time.Time                       `json:"updatedAt"`
	DeletedAt          *time.Time                      `json:"deletedAt,omitempty"`
}

type ProjectDTO struct {
	ID          uint       `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	IsArchived  bool       `json:"isArchived"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	DeletedAt   *time.Time `json:"deletedAt,omitempty"`
}

type CommentDTO struct {
	ID        uint      `json:"id"`
	TicketID  uint      `json:"ticketId"`
	Author    UserRef   `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type EventDTO struct {
	ID        uint                    `json:"id"`
	Actor     UserRef                 `json:"actor"`
	Action    model.TicketEventAction `json:"action"`
	Field     string                  `json:"field,omitempty"`
	From      *string                 `json:"from"`
	To        *string                 `json:"to"`
	CreatedAt time.Time               `json:"createdAt"`
}

type Page[T any] struct {
	Items []T `json:"items"`
	// TotalCount 为匹配过滤条件的总数；keyset 续页不重算 COUNT 时为 -1。
	TotalCount    int64    `json:"totalCount"`
	Limit         int      `json:"limit"`
	Offset        int      `json:"offset"`
	HasMore       bool     `json:"hasMore"`
	NextAfterSort *float64 `json:"nextAfterSort,omitempty"`
	NextAfterID   *uint    `json:"nextAfterId,omitempty"`
}

func newPage[T any](items []T, total int64, limit, offset int) Page[T] {
	if items == nil {
		items = []T{}
	}
	return Page[T]{Items: items, TotalCount: total, Limit: limit, Offset: offset, HasMore: int64(offset+len(items)) < total}
}

type SummaryDTO struct {
	Timezone              string         `json:"timezone"`
	TotalTasks            int            `json:"totalTasks"`
	CompletedTasks        int            `json:"completedTasks"`
	OverallCompletionRate float64        `json:"overallCompletionRate"`
	ByStatus              map[string]int `json:"byStatus"`
	Trends                []TrendPoint   `json:"trends"`
}

type TrendPoint struct {
	Date      string `json:"date"`
	Created   int    `json:"created"`
	Completed int    `json:"completed"`
}

// --- 映射 ---

func toTicketDTO(t model.Ticket) TicketDTO {
	d := TicketDTO{
		ID:                 t.ID,
		Project:            ProjectRef{ID: t.ProjectID, Name: t.Project.Name},
		Type:               t.Type,
		Title:              t.Title,
		Description:        t.Description,
		Reporter:           UserRef{ID: t.ReporterID, Username: t.Reporter.Username},
		Priority:           t.Priority,
		Status:             t.Status,
		DueDate:            t.DueDate,
		AcceptanceCriteria: t.AcceptanceCriteria,
		Attachments:        t.Attachments,
		SortOrder:          t.SortOrder,
		Version:            t.Version,
		CompletedAt:        t.CompletedAt,
		CreatedAt:          t.CreatedAt,
		UpdatedAt:          t.UpdatedAt,
	}
	if t.AssigneeID != nil {
		ref := UserRef{ID: *t.AssigneeID}
		if t.Assignee != nil {
			ref.Username = t.Assignee.Username
		}
		d.Assignee = &ref
	}
	if d.Attachments == nil {
		d.Attachments = []model.TicketAttachment{}
	}
	if t.DeletedAt.Valid {
		d.DeletedAt = &t.DeletedAt.Time
	}
	return d
}

func toProjectDTO(p model.TicketProject) ProjectDTO {
	d := ProjectDTO{ID: p.ID, Name: p.Name, Description: p.Description, IsArchived: p.IsArchived, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
	if p.DeletedAt.Valid {
		d.DeletedAt = &p.DeletedAt.Time
	}
	return d
}

func toCommentDTO(c model.TicketComment) CommentDTO {
	return CommentDTO{ID: c.ID, TicketID: c.TicketID, Author: UserRef{ID: c.AuthorID, Username: c.Author.Username}, Body: c.Body, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func toEventDTO(e model.TicketEvent) EventDTO {
	return EventDTO{ID: e.ID, Actor: UserRef{ID: e.ActorID, Username: e.Actor.Username}, Action: e.Action, Field: e.Field, From: e.FromValue, To: e.ToValue, CreatedAt: e.CreatedAt}
}
