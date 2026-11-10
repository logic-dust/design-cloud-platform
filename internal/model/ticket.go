// noinspection GoMixedReceiver
package model

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// TicketType 工单类型
type TicketType int

const (
	TicketTypeBug     TicketType = 0 // 缺陷
	TicketTypeFeature TicketType = 1 // 功能
	TicketTypeTask    TicketType = 2 // 任务
)

// TicketPriority 工单优先级
type TicketPriority int

const (
	TicketPriorityLow      TicketPriority = 0
	TicketPriorityMedium   TicketPriority = 1
	TicketPriorityHigh     TicketPriority = 2
	TicketPriorityCritical TicketPriority = 3
)

// TicketStatus 工单状态
type TicketStatus int

const (
	TicketStatusToDo       TicketStatus = 0
	TicketStatusInProgress TicketStatus = 1
	TicketStatusInReview   TicketStatus = 2
	TicketStatusDone       TicketStatus = 3
)

func (t TicketType) String() string {
	switch t {
	case TicketTypeBug:
		return "bug"
	case TicketTypeFeature:
		return "feature"
	case TicketTypeTask:
		return "task"
	default:
		return fmt.Sprintf("ticketType(%d)", t)
	}
}

func ParseTicketType(s string) (TicketType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "bug":
		return TicketTypeBug, nil
	case "feature":
		return TicketTypeFeature, nil
	case "task":
		return TicketTypeTask, nil
	default:
		return 0, fmt.Errorf("unknown ticket type: %q", s)
	}
}

func (t TicketType) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

func (t *TicketType) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid TicketType: %s", string(data))
	}
	parsed, err := ParseTicketType(strVal)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

func (p TicketPriority) String() string {
	switch p {
	case TicketPriorityLow:
		return "low"
	case TicketPriorityMedium:
		return "medium"
	case TicketPriorityHigh:
		return "high"
	case TicketPriorityCritical:
		return "critical"
	default:
		return fmt.Sprintf("ticketPriority(%d)", p)
	}
}

func ParseTicketPriority(s string) (TicketPriority, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return TicketPriorityLow, nil
	case "medium":
		return TicketPriorityMedium, nil
	case "high":
		return TicketPriorityHigh, nil
	case "critical":
		return TicketPriorityCritical, nil
	default:
		return 0, fmt.Errorf("unknown ticket priority: %q", s)
	}
}

func (p TicketPriority) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.String())
}

func (p *TicketPriority) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid TicketPriority: %s", string(data))
	}
	parsed, err := ParseTicketPriority(strVal)
	if err != nil {
		return err
	}
	*p = parsed
	return nil
}

func (s TicketStatus) String() string {
	switch s {
	case TicketStatusToDo:
		return "todo"
	case TicketStatusInProgress:
		return "inProgress"
	case TicketStatusInReview:
		return "inReview"
	case TicketStatusDone:
		return "done"
	default:
		return fmt.Sprintf("ticketStatus(%d)", s)
	}
}

func ParseTicketStatus(s string) (TicketStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "todo":
		return TicketStatusToDo, nil
	case "inprogress":
		return TicketStatusInProgress, nil
	case "inreview":
		return TicketStatusInReview, nil
	case "done":
		return TicketStatusDone, nil
	default:
		return 0, fmt.Errorf("unknown ticket status: %q", s)
	}
}

func (s TicketStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *TicketStatus) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid TicketStatus: %s", string(data))
	}
	parsed, err := ParseTicketStatus(strVal)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

// TicketAcceptanceCriteria 验收标准（以 JSON 存储在 text 列中）。
type TicketAcceptanceCriteria struct {
	Description string                `json:"description"`
	Checklist   []TicketChecklistItem `json:"checklist"`
}

// TicketChecklistItem 验收清单中的一项。
type TicketChecklistItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// TicketAttachment 工单附件：对文件服务中某个位置内文件的引用，不保存文件内容，
// 也不授予访问权；下载时按查看者自己的文件权限判断。
type TicketAttachment struct {
	Location string `json:"location"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType"`
}

// Ticket 工单数据模型。枚举字段在库中存整数，JSON 中为字符串。
type Ticket struct {
	// id 纳入 board / sort_id 复合索引末列，支撑 sortOrder keyset（col,id）续页。
	ID          uint       `gorm:"primarykey;index:idx_tickets_board,priority:4;index:idx_tickets_sort_id,priority:2"`
	ProjectID   uint       `gorm:"not null;index:idx_tickets_board,priority:1"`
	Type        TicketType `gorm:"not null"`
	Title       string     `gorm:"type:varchar(256);not null"`
	Description string     `gorm:"type:text"`

	ReporterID uint  `gorm:"not null;index:idx_tickets_reporter_status,priority:1"`
	AssigneeID *uint `gorm:"index:idx_tickets_assignee_status,priority:1"`

	DueDate  *time.Time
	Priority TicketPriority `gorm:"not null"`
	Status   TicketStatus   `gorm:"not null;default:0;index:idx_tickets_board,priority:2;index:idx_tickets_assignee_status,priority:2;index:idx_tickets_reporter_status,priority:2"`

	AcceptanceCriteria *TicketAcceptanceCriteria `gorm:"type:text;serializer:json"`
	Attachments        []TicketAttachment        `gorm:"type:text;serializer:json"`

	// SortOrder 为看板列内的排序位置，由服务端计算。
	SortOrder float64 `gorm:"not null;default:0;index:idx_tickets_board,priority:3;index:idx_tickets_sort_id,priority:1"`
	// Version 为乐观锁版本号，每次修改加 1。
	Version int `gorm:"not null;default:1"`

	CompletedAt *time.Time `gorm:"index"`
	CreatedAt   time.Time  `gorm:"index"`
	UpdatedAt   time.Time
	DeletedAt   gorm.DeletedAt `gorm:"index"`

	Project  TicketProject `gorm:"foreignKey:ProjectID;constraint:false"`
	Reporter User          `gorm:"foreignKey:ReporterID;constraint:false"`
	Assignee *User         `gorm:"foreignKey:AssigneeID;constraint:false"`
}

// TicketComment 工单评论。
type TicketComment struct {
	ID        uint   `gorm:"primarykey"`
	TicketID  uint   `gorm:"not null;index:idx_ticket_comments_ticket,priority:1"`
	AuthorID  uint   `gorm:"not null"`
	Body      string `gorm:"type:text;not null"`
	CreatedAt time.Time
	UpdatedAt time.Time

	Author User `gorm:"foreignKey:AuthorID;constraint:false"`
}

// TicketEventAction 变更记录动作。
type TicketEventAction string

const (
	TicketEventCreated   TicketEventAction = "created"
	TicketEventUpdated   TicketEventAction = "updated"
	TicketEventMoved     TicketEventAction = "moved"
	TicketEventDeleted   TicketEventAction = "deleted"
	TicketEventRestored  TicketEventAction = "restored"
	TicketEventCommented TicketEventAction = "commented"
)

// TicketEvent 工单变更记录。updated 动作按字段一行；长文本字段只记录字段名，不存值。
type TicketEvent struct {
	ID        uint              `gorm:"primarykey"`
	TicketID  uint              `gorm:"not null;index:idx_ticket_events_ticket,priority:1"`
	ActorID   uint              `gorm:"not null"`
	Action    TicketEventAction `gorm:"type:varchar(32);not null"`
	Field     string            `gorm:"type:varchar(64)"`
	FromValue *string           `gorm:"type:text"`
	ToValue   *string           `gorm:"type:text"`
	CreatedAt time.Time

	Actor User `gorm:"foreignKey:ActorID;constraint:false"`
}

// TicketProject 工单项目。名称在未删除的项目中唯一。
type TicketProject struct {
	ID          uint   `gorm:"primarykey"`
	Name        string `gorm:"type:varchar(128);not null;uniqueIndex:idx_ticket_projects_name_active,where:deleted_at IS NULL"`
	Description string `gorm:"type:text"`
	IsArchived  bool   `gorm:"not null;default:false;index"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   gorm.DeletedAt `gorm:"index"`
}
